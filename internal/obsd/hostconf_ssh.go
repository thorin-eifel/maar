package obsd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const sshdPath = "/etc/ssh/sshd_config"

// SSHConf: leere Werte bedeuten „nicht gesetzt“ – dann gilt der Standard von sshd.
type SSHConf struct {
	Ports               []int    `json:"ports"`
	ListenAddress       []string `json:"listenAddress"`
	PermitRootLogin     string   `json:"permitRootLogin"`
	PasswordAuth        string   `json:"passwordAuthentication"`
	PubkeyAuth          string   `json:"pubkeyAuthentication"`
	KbdInteractive      string   `json:"kbdInteractiveAuthentication"`
	AllowUsers          []string `json:"allowUsers"`
	AllowGroups         []string `json:"allowGroups"`
	MaxAuthTries        int      `json:"maxAuthTries"`
	LoginGraceTime      int      `json:"loginGraceTime"`
	X11Forwarding       string   `json:"x11Forwarding"`
	AllowTCPForwarding  string   `json:"allowTcpForwarding"`
	ClientAliveInterval int      `json:"clientAliveInterval"`
	ClientAliveCountMax int      `json:"clientAliveCountMax"`
	LogLevel            string   `json:"logLevel"`
}

type SSHView struct {
	Conf      SSHConf           `json:"conf"`
	Defaults  map[string]string `json:"defaults"`
	Running   bool              `json:"running"`
	Users     []string          `json:"users"`
	Groups    []string          `json:"groups"`
	Addresses []string          `json:"addresses"`
	Preserved []string          `json:"preserved"` // z. B. Match-Blöcke, Subsystem
	Matches   int               `json:"matchBlocks"`
	Keys      map[string]int    `json:"authorizedKeys"` // Konto → Anzahl hinterlegter Schlüssel
}

var sshDefaults = map[string]string{
	"port": "22", "permitRootLogin": "prohibit-password", "passwordAuthentication": "yes", "pubkeyAuthentication": "yes",
	"kbdInteractiveAuthentication": "yes", "maxAuthTries": "6", "loginGraceTime": "120", "x11Forwarding": "no",
	"allowTcpForwarding": "yes", "clientAliveInterval": "0", "clientAliveCountMax": "3", "logLevel": "INFO",
}

var (
	sshYesNo   = []string{"yes", "no"}
	sshRoot    = []string{"yes", "prohibit-password", "forced-commands-only", "no"}
	sshForward = []string{"yes", "no", "local", "remote"}
	sshLog     = []string{"QUIET", "FATAL", "ERROR", "INFO", "VERBOSE", "DEBUG"}
	reSSHName  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9._-]{0,31}$`)
	sshManaged = map[string]bool{"port": true, "listenaddress": true, "permitrootlogin": true, "passwordauthentication": true, "pubkeyauthentication": true,
		"kbdinteractiveauthentication": true, "allowusers": true, "allowgroups": true, "maxauthtries": true, "logingracetime": true, "x11forwarding": true,
		"allowtcpforwarding": true, "clientaliveinterval": true, "clientalivecountmax": true, "loglevel": true}
)

// globalPart trennt die Anweisungen vor dem ersten Match-Block ab.
func sshSplit(src string) (global []string, matches int) {
	for _, l := range splitFileLines(src) {
		if !isCommentOrBlank(l) && firstWord(strings.TrimSpace(l)) == "match" {
			matches++
		}
		if matches == 0 {
			global = append(global, l)
		}
	}
	return
}

func parseSSH(src string) (c SSHConf, preserved []string, matches int) {
	c = SSHConf{Ports: []int{}, ListenAddress: []string{}, AllowUsers: []string{}, AllowGroups: []string{}}
	global, matches := sshSplit(src)
	for _, l := range global {
		if isCommentOrBlank(l) {
			continue
		}
		f := strings.Fields(strings.SplitN(l, "#", 2)[0])
		if len(f) < 2 {
			preserved = append(preserved, strings.TrimSpace(l))
			continue
		}
		key, val := strings.ToLower(f[0]), f[1:]
		one := val[0]
		switch key {
		case "port":
			c.Ports = append(c.Ports, atoiSafe(one))
		case "listenaddress":
			c.ListenAddress = append(c.ListenAddress, one)
		case "permitrootlogin":
			c.PermitRootLogin = strings.ToLower(one)
		case "passwordauthentication":
			c.PasswordAuth = strings.ToLower(one)
		case "pubkeyauthentication":
			c.PubkeyAuth = strings.ToLower(one)
		case "kbdinteractiveauthentication":
			c.KbdInteractive = strings.ToLower(one)
		case "allowusers":
			c.AllowUsers = append(c.AllowUsers, val...)
		case "allowgroups":
			c.AllowGroups = append(c.AllowGroups, val...)
		case "maxauthtries":
			c.MaxAuthTries = atoiSafe(one)
		case "logingracetime":
			c.LoginGraceTime = atoiSafe(one)
		case "x11forwarding":
			c.X11Forwarding = strings.ToLower(one)
		case "allowtcpforwarding":
			c.AllowTCPForwarding = strings.ToLower(one)
		case "clientaliveinterval":
			c.ClientAliveInterval = atoiSafe(one)
		case "clientalivecountmax":
			c.ClientAliveCountMax = atoiSafe(one)
		case "loglevel":
			c.LogLevel = strings.ToUpper(one)
		default:
			preserved = append(preserved, strings.TrimSpace(l))
		}
	}
	return
}

// authorizedKeys zählt die Schlüssel eines Kontos.
func (h *Host) authorizedKeys(home string) int {
	b, err := os.ReadFile(h.P(filepath.Join(home, ".ssh", "authorized_keys")))
	if err != nil {
		return 0
	}
	n := 0
	for _, l := range lines(string(b)) {
		if !strings.HasPrefix(strings.TrimSpace(l), "#") {
			n++
		}
	}
	return n
}

func (h *Host) SSH(ctx context.Context) (*SSHView, error) {
	src, err := h.readFile(sshdPath)
	if err != nil {
		return nil, err
	}
	c, pres, m := parseSSH(src)
	v := &SSHView{Conf: c, Defaults: sshDefaults, Users: []string{}, Groups: []string{}, Preserved: append([]string{}, pres...), Matches: m, Keys: map[string]int{}}
	_, v.Running = h.rcActive(ctx, "sshd")
	if us, err := h.Users(); err == nil {
		for _, u := range us {
			if !u.Locked && (!u.System || u.UID == 0) {
				v.Users = append(v.Users, u.Name)
				v.Keys[u.Name] = h.authorizedKeys(u.Home)
			}
		}
	}
	if gs, err := h.Groups(); err == nil {
		for _, g := range gs {
			if g.GID >= 1000 || g.Name == "wheel" || g.Name == "staff" {
				v.Groups = append(v.Groups, g.Name)
			}
		}
	}
	v.Addresses = h.localAddrs(ctx)
	return v, nil
}

func oneOf(what, v string, ok []string) error {
	if v == "" || contains(ok, v) {
		return nil
	}
	return invalid("%s %q (erlaubt: %s)", what, v, strings.Join(ok, ", "))
}

func (c *SSHConf) lines() []string {
	var out []string
	add := func(k string, v any) { out = append(out, fmt.Sprintf("%s %v", k, v)) }
	for _, p := range c.Ports {
		add("Port", p)
	}
	for _, a := range c.ListenAddress {
		add("ListenAddress", a)
	}
	str := map[string]string{"PermitRootLogin": c.PermitRootLogin, "PasswordAuthentication": c.PasswordAuth, "PubkeyAuthentication": c.PubkeyAuth,
		"KbdInteractiveAuthentication": c.KbdInteractive, "X11Forwarding": c.X11Forwarding, "AllowTcpForwarding": c.AllowTCPForwarding, "LogLevel": c.LogLevel}
	for _, k := range []string{"PermitRootLogin", "PasswordAuthentication", "PubkeyAuthentication", "KbdInteractiveAuthentication", "X11Forwarding", "AllowTcpForwarding", "LogLevel"} {
		if str[k] != "" {
			add(k, str[k])
		}
	}
	for _, kv := range []struct {
		k string
		v int
	}{{"MaxAuthTries", c.MaxAuthTries}, {"LoginGraceTime", c.LoginGraceTime}, {"ClientAliveInterval", c.ClientAliveInterval}, {"ClientAliveCountMax", c.ClientAliveCountMax}} {
		if kv.v > 0 {
			add(kv.k, kv.v)
		}
	}
	if len(c.AllowUsers) > 0 {
		add("AllowUsers", strings.Join(c.AllowUsers, " "))
	}
	if len(c.AllowGroups) > 0 {
		add("AllowGroups", strings.Join(c.AllowGroups, " "))
	}
	return out
}

// SaveSSH schreibt sshd_config. actor ist das angemeldete Konto: Es darf sich
// per Einstellung nicht selbst den SSH-Zugang nehmen.
func (h *Host) SaveSSH(ctx context.Context, actor string, c *SSHConf, preview bool) (*ConfResult, error) {
	cur, err := h.SSH(ctx)
	if err != nil {
		return nil, err
	}
	for _, e := range []error{oneOf("PermitRootLogin", c.PermitRootLogin, sshRoot), oneOf("PasswordAuthentication", c.PasswordAuth, sshYesNo),
		oneOf("PubkeyAuthentication", c.PubkeyAuth, sshYesNo), oneOf("KbdInteractiveAuthentication", c.KbdInteractive, sshYesNo),
		oneOf("X11Forwarding", c.X11Forwarding, sshYesNo), oneOf("AllowTcpForwarding", c.AllowTCPForwarding, sshForward), oneOf("LogLevel", c.LogLevel, sshLog)} {
		if e != nil {
			return nil, e
		}
	}
	if len(c.Ports) > 5 || len(c.ListenAddress) > 10 {
		return nil, invalid("zu viele Ports oder Adressen")
	}
	seenP := map[int]bool{}
	for _, p := range c.Ports {
		if p < 1 || p > 65535 || seenP[p] {
			return nil, invalid("Port %d (1–65535, nicht doppelt)", p)
		}
		seenP[p] = true
	}
	local := map[string]bool{"0.0.0.0": true, "::": true}
	for _, a := range cur.Addresses {
		local[a] = true
	}
	for _, a := range cur.Conf.ListenAddress {
		local[a] = true
	}
	for _, a := range c.ListenAddress {
		if !local[a] {
			return nil, invalid("%q ist keine Adresse dieses Hosts", a)
		}
	}
	for _, n := range []struct {
		what      string
		v, lo, hi int
	}{{"MaxAuthTries", c.MaxAuthTries, 0, 20}, {"LoginGraceTime", c.LoginGraceTime, 0, 600}, {"ClientAliveInterval", c.ClientAliveInterval, 0, 3600}, {"ClientAliveCountMax", c.ClientAliveCountMax, 0, 20}} {
		if n.v < n.lo || n.v > n.hi {
			return nil, invalid("%s %d (%d–%d)", n.what, n.v, n.lo, n.hi)
		}
	}
	users, groups := map[string]bool{}, map[string]bool{}
	for _, u := range cur.Users {
		users[u] = true
	}
	for _, g := range cur.Groups {
		groups[g] = true
	}
	known := func(list, existing []string, set map[string]bool, what string) error {
		for _, x := range list {
			if !set[x] && !contains(existing, x) {
				return invalid("%s %q gibt es nicht", what, x)
			}
			if !reSSHName.MatchString(x) && !contains(existing, x) {
				return invalid("%s %q", what, x)
			}
		}
		return nil
	}
	if err := known(c.AllowUsers, cur.Conf.AllowUsers, users, "Benutzer"); err != nil {
		return nil, err
	}
	if err := known(c.AllowGroups, cur.Conf.AllowGroups, groups, "Gruppe"); err != nil {
		return nil, err
	}

	// Aussperr-Schutz
	pw, kbd, pub := c.PasswordAuth != "no", c.KbdInteractive != "no", c.PubkeyAuth != "no"
	if !pw && !kbd && !pub {
		return nil, invalid("Es bliebe keine Anmeldemethode übrig (Passwort, Tastatur-interaktiv und Schlüssel sind alle aus)")
	}
	if actor != "" {
		if len(c.AllowUsers) > 0 && !contains(c.AllowUsers, actor) {
			return nil, invalid("%s steht nicht in AllowUsers – du würdest dich selbst per SSH aussperren", actor)
		}
		if len(c.AllowGroups) > 0 {
			ok := false
			if us, err := h.Users(); err == nil {
				for _, u := range us {
					if u.Name == actor {
						for _, g := range u.Groups {
							ok = ok || contains(c.AllowGroups, g)
						}
					}
				}
			}
			if !ok {
				return nil, invalid("%s gehört keiner der Gruppen in AllowGroups an – du würdest dich selbst per SSH aussperren", actor)
			}
		}
		if !pw && !kbd && cur.Keys[actor] == 0 {
			return nil, invalid("Passwort-Anmeldung abzuschalten ist gefährlich: für %s ist kein Schlüssel in ~/.ssh/authorized_keys hinterlegt", actor)
		}
		if c.PermitRootLogin == "no" && actor == "root" && len(c.AllowUsers) == 0 {
			// harmlos: maar selbst läuft als root, SSH ist nur ein weiterer Weg
		}
	}

	src, _ := h.readFile(sshdPath)
	content := rewriteSSH(src, c.lines())
	res := &ConfResult{Warnings: []string{}}
	if len(c.Ports) > 0 && !(len(c.Ports) == 1 && c.Ports[0] == 22) {
		pf, _ := h.readFile("/etc/pf.conf")
		for _, p := range c.Ports {
			if p != 22 && !strings.Contains(pf, fmt.Sprint(p)) {
				res.Warnings = append(res.Warnings, fmt.Sprintf("Port %d kommt in /etc/pf.conf nicht vor – der Paketfilter blockiert ihn vermutlich.", p))
			}
		}
		res.Warnings = append(res.Warnings, "Mit geänderten Ports sofort eine zweite SSH-Sitzung testen, bevor du die alte schließt.")
	}
	if cur.Matches > 0 {
		res.Warnings = append(res.Warnings, fmt.Sprintf("%d Match-Block/Blöcke bleiben unverändert und können die globalen Einstellungen für bestimmte Konten oder Adressen übersteuern.", cur.Matches))
	}
	if err := h.putConf(ctx, sshdPath, content, preview, res); err != nil {
		return nil, err
	}
	if preview {
		return res, nil
	}
	if cur.Running {
		if err := h.run(ctx, "rcctl", "reload", "sshd"); err != nil {
			return nil, fmt.Errorf("Datei gespeichert, aber sshd ließ sich nicht neu laden: %w", err)
		}
		res.Applied = "sshd neu geladen. Bestehende Sitzungen bleiben offen – teste die Anmeldung, bevor du dich abmeldest."
	} else {
		res.Applied = "Gespeichert; sshd läuft nicht."
	}
	return res, nil
}

// rewriteSSH ersetzt verwaltete Anweisungen im globalen Teil (vor dem ersten Match).
func rewriteSSH(src string, repl []string) string {
	lines := splitFileLines(src)
	cut := len(lines)
	for i, l := range lines {
		if !isCommentOrBlank(l) && firstWord(strings.TrimSpace(l)) == "match" {
			cut = i
			break
		}
	}
	head := strings.Join(lines[:cut], "\n")
	if head != "" {
		head += "\n"
	}
	global := rewriteLines(head, func(t string) bool { return sshManaged[firstWord(t)] }, repl, nil)
	rest := ""
	if cut < len(lines) {
		// Leerzeile vor dem Match-Block, falls sie fehlt
		if !strings.HasSuffix(global, "\n\n") && global != "" {
			global += "\n"
		}
		rest = strings.Join(lines[cut:], "\n") + "\n"
	}
	return global + rest
}

var _ = strconv.Itoa
