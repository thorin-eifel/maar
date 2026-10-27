package obsd

import (
	"context"
	"os"
	"regexp"
	"sort"
	"strings"
)

const doasPath = "/etc/doas.conf"

// DoasRule ist eine Zeile von doas.conf(5): permit|deny [Optionen] Identität [as Ziel] [cmd Befehl [args …]].
// Zeilen, die das Formular nicht abbilden kann (z. B. setenv), bleiben als Raw erhalten.
type DoasRule struct {
	Action   string   `json:"action"` // permit, deny
	NoPass   bool     `json:"nopass"`
	NoLog    bool     `json:"nolog"`
	Persist  bool     `json:"persist"`
	KeepEnv  bool     `json:"keepenv"`
	Identity string   `json:"identity"` // Benutzer oder :Gruppe
	As       string   `json:"as"`       // Zielbenutzer, leer = root
	Cmd      string   `json:"cmd"`      // leer = jeder Befehl
	Args     []string `json:"args"`
	Raw      string   `json:"raw,omitempty"`
	Comment  string   `json:"comment"` // Kommentarzeilen direkt vor der Regel
}

type DoasConf struct {
	Rules []DoasRule `json:"rules"`
}

type DoasView struct {
	Conf     DoasConf `json:"conf"`
	Header   []string `json:"header"` // Kommentare vor der ersten Regel
	Users    []string `json:"users"`
	Groups   []string `json:"groups"`
	Commands []string `json:"commands"` // häufige Programme, die es auf dem Host gibt
}

var (
	reDoasArg = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]{1,200}$`)
	doasCmds  = []string{"/sbin/reboot", "/sbin/shutdown", "/usr/sbin/rcctl", "/usr/sbin/pkg_add", "/usr/sbin/pkg_delete", "/usr/sbin/syspatch", "/usr/sbin/sysupgrade", "/usr/sbin/vmctl", "/usr/sbin/vmd", "/usr/sbin/useradd", "/usr/sbin/usermod", "/sbin/pfctl", "/usr/sbin/rcctl", "/usr/bin/su", "/bin/ksh", "/usr/bin/vi", "/usr/bin/less", "/bin/cat", "/usr/local/bin/qemu-img"}
)

func parseDoasLine(l string) (DoasRule, bool) {
	f := strings.Fields(l)
	if len(f) < 2 || (f[0] != "permit" && f[0] != "deny") {
		return DoasRule{}, false
	}
	r := DoasRule{Action: f[0], Args: []string{}}
	i := 1
	for i < len(f) {
		switch f[i] {
		case "nopass":
			r.NoPass = true
		case "nolog":
			r.NoLog = true
		case "persist":
			r.Persist = true
		case "keepenv":
			r.KeepEnv = true
		case "setenv":
			return DoasRule{}, false
		default:
			goto ident
		}
		i++
	}
ident:
	if i >= len(f) {
		return DoasRule{}, false
	}
	r.Identity = f[i]
	i++
	if i+1 < len(f) && f[i] == "as" {
		r.As = f[i+1]
		i += 2
	}
	if i < len(f) && f[i] == "cmd" {
		if i+1 >= len(f) {
			return DoasRule{}, false
		}
		r.Cmd = f[i+1]
		i += 2
		if i < len(f) && f[i] == "args" {
			i++
		}
		r.Args = append(r.Args, f[i:]...)
		i = len(f)
	}
	if i != len(f) {
		return DoasRule{}, false
	}
	return r, true
}

func (r DoasRule) line() string {
	if r.Raw != "" {
		return r.Raw
	}
	parts := []string{r.Action}
	for _, o := range []struct {
		on   bool
		name string
	}{{r.NoPass, "nopass"}, {r.NoLog, "nolog"}, {r.Persist, "persist"}, {r.KeepEnv, "keepenv"}} {
		if o.on {
			parts = append(parts, o.name)
		}
	}
	parts = append(parts, r.Identity)
	if r.As != "" {
		parts = append(parts, "as", r.As)
	}
	if r.Cmd != "" {
		parts = append(parts, "cmd", r.Cmd)
		if len(r.Args) > 0 {
			parts = append(parts, "args")
			parts = append(parts, r.Args...)
		}
	}
	return strings.Join(parts, " ")
}

func parseDoasFile(src string) (header []string, rules []DoasRule) {
	var pending []string
	seenRule := false
	for _, l := range splitFileLines(src) {
		t := strings.TrimSpace(l)
		switch {
		case t == "":
			if !seenRule {
				header = append(header, pending...)
				header = append(header, "")
				pending = nil
			}
		case strings.HasPrefix(t, "#"):
			pending = append(pending, strings.TrimSpace(strings.TrimPrefix(t, "#")))
		default:
			r, ok := parseDoasLine(t)
			if !ok {
				r = DoasRule{Raw: t, Args: []string{}}
			}
			r.Comment = strings.Join(pending, " ")
			pending = nil
			seenRule = true
			rules = append(rules, r)
		}
	}
	if !seenRule {
		header = append(header, pending...)
	} else if len(pending) > 0 {
		rules = append(rules, DoasRule{Raw: "", Comment: strings.Join(pending, " ")}) // Schlusskommentar
	}
	return
}

func (h *Host) Doas(ctx context.Context) (*DoasView, error) {
	src, err := h.readFile(doasPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	hd, rules := parseDoasFile(src)
	v := &DoasView{Conf: DoasConf{Rules: []DoasRule{}}, Header: hd, Users: []string{}, Groups: []string{}, Commands: []string{}}
	for _, r := range rules {
		if r.Raw == "" && r.Action == "" {
			continue // Schlusskommentar
		}
		v.Conf.Rules = append(v.Conf.Rules, r)
	}
	if us, err := h.Users(); err == nil {
		for _, u := range us {
			if !u.System || u.UID == 0 {
				v.Users = append(v.Users, u.Name)
			}
		}
	}
	if gs, err := h.Groups(); err == nil {
		for _, g := range gs {
			if g.GID >= 1000 || g.Name == "wheel" || g.Name == "staff" || g.GID == 0 {
				v.Groups = append(v.Groups, g.Name)
			}
		}
	}
	seen := map[string]bool{}
	for _, c := range doasCmds {
		if _, err := os.Stat(h.P(c)); (err == nil || h.Demo) && !seen[c] {
			seen[c] = true
			v.Commands = append(v.Commands, c)
		}
	}
	sort.Strings(v.Commands)
	return v, nil
}

func (h *Host) SaveDoas(ctx context.Context, actor string, c *DoasConf, preview bool) (*ConfResult, error) {
	cur, err := h.Doas(ctx)
	if err != nil {
		return nil, err
	}
	if len(c.Rules) > 200 {
		return nil, invalid("zu viele Regeln")
	}
	users, groups := map[string]bool{"root": true}, map[string]bool{}
	for _, u := range cur.Users {
		users[u] = true
	}
	for _, g := range cur.Groups {
		groups[g] = true
	}
	var relative []string
	keepRaw := map[string]bool{}
	for _, r := range cur.Conf.Rules {
		if r.Raw != "" {
			keepRaw[r.Raw] = true
		}
	}
	for i := range c.Rules {
		r := &c.Rules[i]
		n := i + 1
		if r.Raw != "" {
			if !keepRaw[r.Raw] {
				return nil, invalid("Regel %d: Rohtext-Regeln lassen sich nur übernehmen, nicht neu anlegen", n)
			}
			r.Comment = strings.TrimSpace(r.Comment)
			continue
		}
		if r.Action != "permit" && r.Action != "deny" {
			return nil, invalid("Regel %d: Aktion permit oder deny", n)
		}
		id := r.Identity
		if strings.HasPrefix(id, ":") {
			if !groups[id[1:]] {
				return nil, invalid("Regel %d: Gruppe %q gibt es nicht", n, id[1:])
			}
		} else if !users[id] {
			return nil, invalid("Regel %d: Benutzer %q gibt es nicht", n, id)
		}
		if r.As != "" && !users[r.As] {
			return nil, invalid("Regel %d: Zielbenutzer %q gibt es nicht", n, r.As)
		}
		if r.Cmd == "" && len(r.Args) > 0 {
			return nil, invalid("Regel %d: Argumente brauchen einen Befehl", n)
		}
		if r.Cmd != "" {
			if strings.HasPrefix(r.Cmd, "/") {
				if strings.ContainsAny(r.Cmd, " \t\n\"'\\;|&$`") {
					return nil, invalid("Regel %d: Befehl %q (Pfad ohne Sonderzeichen)", n, r.Cmd)
				}
				if _, err := os.Stat(h.P(r.Cmd)); err != nil && !h.Demo {
					return nil, invalid("Regel %d: %s gibt es auf diesem Host nicht", n, r.Cmd)
				}
			} else if !regexp.MustCompile(`^[A-Za-z0-9._+-]{1,64}$`).MatchString(r.Cmd) {
				return nil, invalid("Regel %d: Befehl %q (absoluter Pfad oder einfacher Name)", n, r.Cmd)
			} else {
				relative = append(relative, r.Cmd)
			}
		}
		for _, a := range r.Args {
			if !reDoasArg.MatchString(a) {
				return nil, invalid("Regel %d: Argument %q (Buchstaben, Ziffern und @%%+=:,./-)", n, a)
			}
		}
		if r.Action == "deny" && (r.NoPass || r.Persist || r.KeepEnv) {
			return nil, invalid("Regel %d: Optionen gibt es nur bei permit (außer nolog)", n)
		}
		if r.NoPass && r.Persist {
			return nil, invalid("Regel %d: nopass und persist schließen sich aus", n)
		}
		if strings.ContainsAny(r.Comment, "\n\r") || len(r.Comment) > 200 {
			return nil, invalid("Regel %d: Beschreibung", n)
		}
		r.Args = append([]string{}, r.Args...)
	}
	// Datei neu aufbauen: Kopf, dann alle Regeln mit ihrer Beschreibung in der Reihenfolge des Formulars.
	var out []string
	out = append(out, cur.Header...)
	for len(out) > 0 && out[len(out)-1] == "" && len(c.Rules) == 0 {
		out = out[:len(out)-1]
	}
	for i, h := range out {
		if h != "" && !strings.HasPrefix(h, "#") {
			out[i] = "# " + h
		}
	}
	for _, r := range c.Rules {
		if r.Comment != "" {
			out = append(out, "# "+r.Comment)
		}
		out = append(out, r.line())
	}
	content := strings.Join(out, "\n") + "\n"
	res := &ConfResult{Warnings: []string{}}
	for _, cmd := range relative {
		res.Warnings = append(res.Warnings, "Der Befehl „"+cmd+"“ ist nicht als Pfad angegeben; doas vergleicht ihn buchstäblich und sucht ihn nicht im PATH. Ein absoluter Pfad ist sicherer.")
	}
	// Hinweis: Was gilt für den angemeldeten Benutzer? doas wertet von oben nach unten, die letzte passende Regel gewinnt.
	if actor != "" {
		groupsOf := map[string]bool{}
		if us, err := h.Users(); err == nil {
			for _, u := range us {
				if u.Name == actor {
					for _, g := range u.Groups {
						groupsOf[":"+g] = true
					}
				}
			}
		}
		decision := ""
		for _, r := range c.Rules {
			if r.Raw == "" && (r.Identity == actor || groupsOf[r.Identity]) && r.Cmd == "" && (r.As == "" || r.As == "root") {
				decision = r.Action
			}
		}
		if users[actor] && decision != "permit" {
			res.Warnings = append(res.Warnings, "Nach dieser Änderung darf "+actor+" mit doas nicht mehr beliebige Befehle als root ausführen. maar selbst ist davon nicht betroffen.")
		}
	}
	for _, r := range c.Rules {
		if r.Action == "permit" && r.NoPass && r.Cmd == "" && r.Raw == "" {
			res.Warnings = append(res.Warnings, "„"+r.Identity+"“ darf ohne Passwort beliebige Befehle ausführen (nopass ohne Befehl) – das ist faktisch ein Root-Zugang ohne Schutz.")
			break
		}
	}
	if err := h.putConf(ctx, doasPath, content, preview, res); err != nil {
		return nil, err
	}
	if !preview {
		res.Applied = "Gespeichert. doas liest die Datei bei jedem Aufruf neu."
	}
	return res, nil
}
