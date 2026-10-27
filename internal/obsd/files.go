package obsd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ConfigSpec beschreibt eine über die Oberfläche bearbeitbare Datei.
type ConfigSpec struct {
	Path     string   `json:"path"`
	Label    string   `json:"label"`
	Group    string   `json:"group"`
	Check    []string `json:"check,omitempty"` // Prüfbefehl, {} = Kandidatendatei
	Apply    []string `json:"apply,omitempty"` // optional: Übernehmen nach dem Speichern
	ApplyTxt string   `json:"applyLabel,omitempty"`
	Mode     uint32   `json:"-"`
}

var configSpecs = []ConfigSpec{
	{Path: "/etc/rc.conf.local", Label: "Dienste & Flags", Group: "System", Mode: 0o644},
	{Path: "/etc/sysctl.conf", Label: "Kernel-Parameter", Group: "System", Mode: 0o644},
	{Path: "/etc/doas.conf", Label: "doas-Regeln", Group: "System", Check: []string{"doas", "-C", "{}"}, Mode: 0o600},
	{Path: "/etc/login.conf", Label: "Login-Klassen", Group: "System", Mode: 0o644},
	{Path: "/etc/fstab", Label: "Dateisysteme", Group: "System", Mode: 0o644},
	{Path: "/etc/rc.local", Label: "Lokales Startskript", Group: "System", Mode: 0o644},
	{Path: "/etc/installurl", Label: "Paket-Spiegel", Group: "System", Mode: 0o644},
	{Path: "/etc/syslog.conf", Label: "Syslog", Group: "System", Apply: []string{"rcctl", "reload", "syslogd"}, ApplyTxt: "syslogd neu laden", Mode: 0o644},
	{Path: "/etc/iscsi.conf", Label: "iSCSI-Initiator", Group: "Virtualisierung", Check: []string{"iscsid", "-nf", "{}"}, Apply: []string{"iscsictl", "reload"}, ApplyTxt: "iscsictl reload", Mode: 0o600},
	{Path: "/etc/vm.conf", Label: "Virtuelle Maschinen", Group: "Virtualisierung", Check: []string{"vmd", "-n", "-f", "{}"}, Apply: []string{"vmctl", "reload"}, ApplyTxt: "vmctl reload", Mode: 0o644},
	{Path: "/etc/pf.conf", Label: "Paketfilter", Group: "Netzwerk", Check: []string{"pfctl", "-nf", "{}"}, Apply: []string{"pfctl", "-f", "/etc/pf.conf"}, ApplyTxt: "Regelsatz laden", Mode: 0o600},
	{Path: "/etc/pf.maar.conf", Label: "VM-Firewall (erzeugt)", Group: "Netzwerk", Check: []string{"pfctl", "-nf", "{}"}, Apply: []string{"pfctl", "-a", "maar", "-f", "/etc/pf.maar.conf"}, ApplyTxt: "Anker laden", Mode: 0o600},
	{Path: "/var/unbound/etc/maar.conf", Label: "VM-DNS (erzeugt)", Group: "Netzwerk", Check: []string{"unbound-checkconf", "{}"}, Apply: []string{"rcctl", "restart", "unbound"}, ApplyTxt: "unbound neu starten", Mode: 0o644},
	{Path: "/etc/myname", Label: "Hostname", Group: "Netzwerk", Mode: 0o644},
	{Path: "/etc/mygate", Label: "Standard-Gateway", Group: "Netzwerk", Mode: 0o644},
	{Path: "/etc/resolv.conf", Label: "DNS-Resolver", Group: "Netzwerk", Mode: 0o644},
	{Path: "/etc/hosts", Label: "Hosts", Group: "Netzwerk", Mode: 0o644},
	{Path: "/etc/dhcpd.conf", Label: "DHCP-Server", Group: "Netzwerk", Check: []string{"dhcpd", "-n", "-c", "{}"}, Apply: []string{"rcctl", "restart", "dhcpd"}, ApplyTxt: "dhcpd neu starten", Mode: 0o644},
	{Path: "/var/unbound/etc/unbound.conf", Label: "Unbound DNS", Group: "Netzwerk", Check: []string{"unbound-checkconf", "{}"}, Apply: []string{"rcctl", "reload", "unbound"}, ApplyTxt: "unbound neu laden", Mode: 0o644},
	{Path: "/etc/ntpd.conf", Label: "Zeitsynchronisation", Group: "Netzwerk", Check: []string{"ntpd", "-n", "-f", "{}"}, Apply: []string{"rcctl", "restart", "ntpd"}, ApplyTxt: "ntpd neu starten", Mode: 0o644},
	{Path: "/etc/iked.conf", Label: "IKEv2 VPN", Group: "Netzwerk", Check: []string{"iked", "-n", "-f", "{}"}, Apply: []string{"rcctl", "reload", "iked"}, ApplyTxt: "iked neu laden", Mode: 0o600},
	{Path: "/etc/bgpd.conf", Label: "BGP", Group: "Netzwerk", Check: []string{"bgpd", "-n", "-f", "{}"}, Apply: []string{"rcctl", "reload", "bgpd"}, ApplyTxt: "bgpd neu laden", Mode: 0o600},
	{Path: "/etc/httpd.conf", Label: "Webserver httpd", Group: "Dienste", Check: []string{"httpd", "-n", "-f", "{}"}, Apply: []string{"rcctl", "reload", "httpd"}, ApplyTxt: "httpd neu laden", Mode: 0o644},
	{Path: "/etc/relayd.conf", Label: "Load-Balancer relayd", Group: "Dienste", Check: []string{"relayd", "-n", "-f", "{}"}, Apply: []string{"rcctl", "reload", "relayd"}, ApplyTxt: "relayd neu laden", Mode: 0o600},
	{Path: "/etc/mail/smtpd.conf", Label: "Mailserver smtpd", Group: "Dienste", Check: []string{"smtpd", "-n", "-f", "{}"}, Apply: []string{"rcctl", "restart", "smtpd"}, ApplyTxt: "smtpd neu starten", Mode: 0o644},
	{Path: "/etc/ssh/sshd_config", Label: "SSH-Server", Group: "Dienste", Check: []string{"sshd", "-t", "-f", "{}"}, Apply: []string{"rcctl", "reload", "sshd"}, ApplyTxt: "sshd neu laden", Mode: 0o644},
	{Path: "/etc/acme-client.conf", Label: "ACME-Zertifikate", Group: "Dienste", Check: []string{"acme-client", "-n", "-f", "{}"}, Mode: 0o644},
}

var reHostnameIf = regexp.MustCompile(`^/etc/hostname\.[a-z]{1,12}[0-9]{1,4}$`)

func (h *Host) spec(p string) (ConfigSpec, bool) {
	for _, s := range configSpecs {
		if s.Path == p {
			return s, true
		}
	}
	if reHostnameIf.MatchString(p) {
		ifname := strings.TrimPrefix(p, "/etc/hostname.")
		return ConfigSpec{Path: p, Label: "Interface " + ifname, Group: "Interfaces",
			Apply: []string{"sh", "/etc/netstart", ifname}, ApplyTxt: "netstart " + ifname, Mode: 0o640}, true
	}
	return ConfigSpec{}, false
}

type ConfigEntry struct {
	ConfigSpec
	Exists bool  `json:"exists"`
	Size   int64 `json:"size"`
	MTime  int64 `json:"mtime,string"` // Nanosekunden als String: JS-Zahlen haben nur 53 Bit
}

func (h *Host) ConfigFiles() []ConfigEntry {
	var list []ConfigEntry
	add := func(s ConfigSpec) {
		e := ConfigEntry{ConfigSpec: s}
		if st, err := os.Stat(h.P(s.Path)); err == nil {
			e.Exists, e.Size, e.MTime = true, st.Size(), st.ModTime().UnixNano()
		}
		list = append(list, e)
	}
	for _, s := range configSpecs {
		add(s)
	}
	if m, _ := filepath.Glob(h.P("/etc/hostname.*")); m != nil {
		sort.Strings(m)
		for _, f := range m {
			p := "/etc/" + filepath.Base(f)
			if s, ok := h.spec(p); ok {
				add(s)
			}
		}
	}
	return list
}

type ConfigFile struct {
	ConfigEntry
	Content string `json:"content"`
}

func (h *Host) ReadConfig(p string) (*ConfigFile, error) {
	s, ok := h.spec(p)
	if !ok {
		return nil, invalid("Datei %q ist nicht freigegeben", p)
	}
	cf := &ConfigFile{ConfigEntry: ConfigEntry{ConfigSpec: s}}
	b, err := os.ReadFile(h.P(p))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil {
		st, _ := os.Stat(h.P(p))
		cf.Exists, cf.Size, cf.MTime, cf.Content = true, st.Size(), st.ModTime().UnixNano(), string(b)
	}
	return cf, nil
}

type SaveResult struct {
	Backup string `json:"backup,omitempty"`
	Check  string `json:"check,omitempty"`
	MTime  int64  `json:"mtime,string"`
}

// ErrConflict meldet, dass die Datei seit dem Laden geändert wurde.
var ErrConflict = errors.New("die Datei wurde zwischenzeitlich geändert – bitte neu laden")

// CheckError transportiert die Ausgabe eines fehlgeschlagenen Syntaxchecks.
type CheckError struct{ Output string }

func (e *CheckError) Error() string { return "Syntaxprüfung fehlgeschlagen:\n" + e.Output }

// WriteConfig schreibt eine Konfigurationsdatei atomar: Kandidat schreiben,
// prüfen, Original sichern, Kandidat an seinen Platz verschieben.
func (h *Host) WriteConfig(ctx context.Context, p, content string, expectMTime int64) (*SaveResult, error) {
	s, ok := h.spec(p)
	if !ok {
		return nil, invalid("Datei %q ist nicht freigegeben", p)
	}
	if strings.ContainsRune(content, 0) {
		return nil, invalid("Datei enthält NUL-Bytes")
	}
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	real := h.P(p)
	mode := os.FileMode(s.Mode)
	st, err := os.Stat(real)
	if err == nil {
		if expectMTime != 0 && st.ModTime().UnixNano() != expectMTime {
			return nil, ErrConflict
		}
		mode = st.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if mode == 0 {
		mode = 0o644
	}
	tmpSys := p + ".maar.tmp"
	tmp := h.P(tmpSys)
	if err := os.WriteFile(tmp, []byte(content), mode); err != nil {
		return nil, err
	}
	defer os.Remove(tmp)
	os.Chmod(tmp, mode)
	res := &SaveResult{}
	if len(s.Check) > 0 {
		args := make([]string, len(s.Check)-1)
		for i, a := range s.Check[1:] {
			if a == "{}" {
				a = tmpSys
			}
			args[i] = a
		}
		r, err := h.R.Run(ctx, nil, s.Check[0], args...)
		out := strings.TrimSpace(string(r.Stdout) + string(r.Stderr))
		if err != nil {
			if out == "" {
				out = err.Error()
			}
			return nil, &CheckError{Output: strings.ReplaceAll(out, tmpSys, p)}
		}
		res.Check = out
		if res.Check == "" {
			res.Check = "Syntax in Ordnung."
		}
	}
	if st != nil {
		res.Backup, err = h.backup(p)
		if err != nil {
			return nil, fmt.Errorf("Sicherung fehlgeschlagen: %w", err)
		}
	}
	if err := os.Rename(tmp, real); err != nil {
		return nil, err
	}
	if st2, err := os.Stat(real); err == nil {
		res.MTime = st2.ModTime().UnixNano()
	}
	return res, nil
}

func backupKey(p string) string { return strings.ReplaceAll(strings.TrimPrefix(p, "/"), "/", "_") }

func (h *Host) backup(p string) (string, error) {
	dir := h.P(h.BackupDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	b, err := os.ReadFile(h.P(p))
	if err != nil {
		return "", err
	}
	name := backupKey(p) + "." + time.Now().Format("20060102-150405.000")
	if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
		return "", err
	}
	// Nur die letzten 20 Sicherungen pro Datei behalten.
	if old, _ := filepath.Glob(filepath.Join(dir, backupKey(p)+".*")); len(old) > 20 {
		sort.Strings(old)
		for _, f := range old[:len(old)-20] {
			os.Remove(f)
		}
	}
	return filepath.Join(h.BackupDir, name), nil
}

type Backup struct {
	ID   string `json:"id"`
	Time int64  `json:"time"`
	Size int64  `json:"size"`
}

func (h *Host) Backups(p string) ([]Backup, error) {
	if _, ok := h.spec(p); !ok {
		return nil, invalid("Datei %q ist nicht freigegeben", p)
	}
	m, _ := filepath.Glob(filepath.Join(h.P(h.BackupDir), backupKey(p)+".*"))
	sort.Sort(sort.Reverse(sort.StringSlice(m)))
	list := []Backup{}
	for _, f := range m {
		if st, err := os.Stat(f); err == nil {
			list = append(list, Backup{ID: filepath.Base(f), Time: st.ModTime().Unix(), Size: st.Size()})
		}
	}
	return list, nil
}

func (h *Host) ReadBackup(p, id string) (string, error) {
	if _, ok := h.spec(p); !ok || !strings.HasPrefix(id, backupKey(p)+".") || strings.ContainsAny(id, "/\\") {
		return "", invalid("Sicherung %q", id)
	}
	b, err := os.ReadFile(filepath.Join(h.P(h.BackupDir), id))
	return string(b), err
}

func (h *Host) ApplyConfig(ctx context.Context, p string) (string, error) {
	s, ok := h.spec(p)
	if !ok || len(s.Apply) == 0 {
		return "", invalid("für %q gibt es keine Übernahme-Aktion", p)
	}
	r, err := h.R.Run(ctx, nil, s.Apply[0], s.Apply[1:]...)
	return strings.TrimSpace(string(r.Stdout) + string(r.Stderr)), err
}
