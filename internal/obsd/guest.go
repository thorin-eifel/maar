package obsd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Gastanpassung beim ersten Start. Die Daten landen auf einer ISO mit dem
// Label CIDATA (cloud-init „NoCloud“), die der VM als CD-ROM mitgegeben wird:
//
//	meta-data, user-data, network-config  → cloud-init (Linux, BSDs mit cloud-init)
//	maar.cfg                           → maar-guest (OpenBSD-Gäste, siehe web/guest)

type GuestConfig struct {
	OS       string   `json:"os"` // openbsd, linux, other
	Hostname string   `json:"hostname"`
	Network  string   `json:"network"` // dhcp, static, none
	Address  string   `json:"address"` // 10.0.0.20/24
	Gateway  string   `json:"gateway"`
	DNS      []string `json:"dns"`
	User     string   `json:"user"` // leer = root
	Password string   `json:"password"`
	SSHKey   string   `json:"sshKey"`
}

var (
	reHostname = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$`)
	reSSHKey   = regexp.MustCompile(`^(ssh-ed25519|ssh-rsa|ecdsa-sha2-nistp(256|384|521)|sk-ssh-ed25519@openssh\.com|sk-ecdsa-sha2-nistp256@openssh\.com) [A-Za-z0-9+/]+={0,3}( [ -~]{0,200})?$`)
)

func (g *GuestConfig) validate() error {
	if !templateOS[g.OS] {
		return invalid("Gast-Betriebssystem %q", g.OS)
	}
	if len(g.Hostname) > 253 || !reHostname.MatchString(g.Hostname) {
		return invalid("Hostname %q", g.Hostname)
	}
	switch g.Network {
	case "", "dhcp":
		g.Network = "dhcp"
	case "none":
	case "static":
		ip, _, err := net.ParseCIDR(g.Address)
		if err != nil || ip.To4() == nil {
			return invalid("Adresse %q (erwartet IPv4 mit Präfix, z. B. 10.0.0.20/24)", g.Address)
		}
		if g.Gateway != "" && net.ParseIP(g.Gateway).To4() == nil {
			return invalid("Gateway %q", g.Gateway)
		}
	default:
		return invalid("Netzmodus %q", g.Network)
	}
	if len(g.DNS) > 3 {
		return invalid("höchstens drei Nameserver")
	}
	for _, d := range g.DNS {
		if net.ParseIP(d) == nil {
			return invalid("Nameserver %q", d)
		}
	}
	if g.User != "" {
		if err := checkName("Benutzer", g.User, reUser); err != nil {
			return err
		}
	}
	if g.Password != "" && (len(g.Password) < 8 || strings.ContainsAny(g.Password, "\n\r\x00")) {
		return invalid("Passwort muss mindestens 8 Zeichen haben")
	}
	g.SSHKey = strings.TrimSpace(g.SSHKey)
	if g.SSHKey != "" && !reSSHKey.MatchString(g.SSHKey) {
		return invalid("SSH-Schlüssel (eine Zeile im authorized_keys-Format)")
	}
	if g.Password == "" && g.SSHKey == "" && g.User != "" {
		return invalid("für den neuen Benutzer Passwort oder SSH-Schlüssel angeben")
	}
	return nil
}

type seedFiles struct {
	files map[string]string
}

// y kodiert einen Wert als YAML-String (JSON ist gültiges YAML).
func y(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func (h *Host) buildSeed(ctx context.Context, vm string, g *GuestConfig) (*seedFiles, error) {
	if err := g.validate(); err != nil {
		return nil, err
	}
	rnd := make([]byte, 8)
	rand.Read(rnd)
	instance := vm + "-" + hex.EncodeToString(rnd)
	f := map[string]string{}

	f["meta-data"] = fmt.Sprintf("instance-id: %s\nlocal-hostname: %s\n", y(instance), y(g.Hostname))

	// cloud-init
	var u strings.Builder
	u.WriteString("#cloud-config\n")
	fmt.Fprintf(&u, "hostname: %s\nfqdn: %s\npreserve_hostname: false\n", y(strings.SplitN(g.Hostname, ".", 2)[0]), y(g.Hostname))
	user := g.User
	if user == "" {
		user = "root"
		u.WriteString("disable_root: false\n")
	}
	u.WriteString("users:\n")
	if g.User != "" {
		u.WriteString("  - default\n")
	}
	fmt.Fprintf(&u, "  - name: %s\n", y(user))
	if g.User != "" {
		u.WriteString("    groups: [wheel, sudo]\n    shell: /bin/bash\n    sudo: \"ALL=(ALL) ALL\"\n    lock_passwd: false\n")
	}
	if g.SSHKey != "" {
		fmt.Fprintf(&u, "    ssh_authorized_keys:\n      - %s\n", y(g.SSHKey))
	}
	if g.Password != "" {
		fmt.Fprintf(&u, "chpasswd:\n  expire: false\n  users:\n    - name: %s\n      password: %s\n      type: text\nssh_pwauth: %v\n", y(user), y(g.Password), g.SSHKey == "")
	}
	f["user-data"] = u.String()

	var n strings.Builder
	n.WriteString("version: 2\nethernets:\n  primary:\n    match:\n      name: \"e*\"\n")
	switch g.Network {
	case "dhcp":
		n.WriteString("    dhcp4: true\n    dhcp6: true\n")
	case "static":
		fmt.Fprintf(&n, "    addresses: [%s]\n", y(g.Address))
		if g.Gateway != "" {
			fmt.Fprintf(&n, "    routes:\n      - to: default\n        via: %s\n", y(g.Gateway))
		}
	}
	if len(g.DNS) > 0 && g.Network != "none" {
		q := make([]string, len(g.DNS))
		for i, d := range g.DNS {
			q[i] = y(d)
		}
		fmt.Fprintf(&n, "    nameservers:\n      addresses: [%s]\n", strings.Join(q, ", "))
	}
	if g.Network != "none" {
		f["network-config"] = n.String()
	}

	if g.OS != "openbsd" {
		return &seedFiles{files: f}, nil
	}
	// OpenBSD-Gäste: kein cloud-init, sondern maar-guest mit einfachen
	// Zeilen „schlüssel wert“ – und nur dem bcrypt-Hash des Passworts.
	delete(f, "user-data")
	delete(f, "network-config")
	var o strings.Builder
	fmt.Fprintf(&o, "instance %s\nhostname %s\nnet %s\n", instance, g.Hostname, g.Network)
	if g.Network == "static" {
		fmt.Fprintf(&o, "address %s\n", g.Address)
		if g.Gateway != "" {
			fmt.Fprintf(&o, "gateway %s\n", g.Gateway)
		}
	}
	for _, d := range g.DNS {
		fmt.Fprintf(&o, "dns %s\n", d)
	}
	fmt.Fprintf(&o, "user %s\n", user)
	if g.Password != "" {
		r, err := h.R.Run(ctx, []byte(g.Password+"\n"), "encrypt")
		if err != nil {
			return nil, err
		}
		hash := strings.TrimSpace(string(r.Stdout))
		if !strings.HasPrefix(hash, "$2") {
			return nil, fmt.Errorf("encrypt lieferte keinen bcrypt-Hash")
		}
		fmt.Fprintf(&o, "pwhash %s\n", hash)
	}
	if g.SSHKey != "" {
		fmt.Fprintf(&o, "sshkey %s\n", g.SSHKey)
	}
	f["maar.cfg"] = o.String()
	return &seedFiles{files: f}, nil
}

// writeSeedISO erzeugt die NoCloud-ISO mit makefs(8).
func (h *Host) writeSeedISO(ctx context.Context, s *seedFiles, iso string) error {
	if err := os.MkdirAll(h.P(h.seedDir()), 0o700); err != nil {
		return err
	}
	dir, err := os.MkdirTemp(h.P(h.seedDir()), ".build-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	for name, content := range s.files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			return err
		}
	}
	sysDir := filepath.Join(h.seedDir(), filepath.Base(dir))
	if _, err := h.R.Run(ctx, nil, "makefs", "-t", "cd9660", "-o", "label=CIDATA,rockridge", iso, sysDir); err != nil {
		return err
	}
	return os.Chmod(h.P(iso), 0o600)
}
