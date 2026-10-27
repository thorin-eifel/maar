package obsd

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const smtpdPath = "/etc/mail/smtpd.conf"

// smtpd.conf(5) als Formular: Tabellen, PKI, listen, action und match. Zeilen, die
// das Formular nicht vollständig versteht (Makros, queue, filter, mda …), bleiben als
// „Rohzeile“ an ihrem Platz. Die Reihenfolge der match-Regeln ist wichtig: die erste passende gewinnt.

type SMTPTable struct {
	Name    string `json:"name"`
	Type    string `json:"type"` // file, db
	Path    string `json:"path"`
	Comment string `json:"comment"`
	Raw     string `json:"raw,omitempty"`
}

type SMTPPKI struct {
	Name    string `json:"name"`
	Cert    string `json:"cert"`
	Key     string `json:"key"`
	Comment string `json:"comment"`
	Raw     string `json:"raw,omitempty"`
}

type SMTPListen struct {
	On       string `json:"on"`     // Interface, Gruppe, Adresse oder „socket“
	Family   string `json:"family"` // "", inet4, inet6
	Port     int    `json:"port"`   // 0 = Standard
	TLS      string `json:"tls"`    // "", tls, tls-require, smtps
	PKI      string `json:"pki"`
	Auth     string `json:"auth"` // "", auth, auth-optional
	Hostname string `json:"hostname"`
	MaskSrc  bool   `json:"maskSrc"`
	Comment  string `json:"comment"`
	Raw      string `json:"raw,omitempty"`
}

type SMTPAction struct {
	Name     string `json:"name"`
	Method   string `json:"method"` // mbox, maildir, lmtp, relay, expand-only, forward-only
	Path     string `json:"path"`   // maildir-Pfad oder lmtp-Ziel
	Junk     bool   `json:"junk"`
	Alias    string `json:"alias"`
	Virtual  string `json:"virtual"`
	Userbase string `json:"userbase"`
	Host     string `json:"host"` // relay: URL
	TLS      string `json:"tls"`  // "", tls, no-verify
	Auth     string `json:"auth"`
	Helo     string `json:"helo"`
	Src      string `json:"src"`
	Comment  string `json:"comment"`
	Raw      string `json:"raw,omitempty"`
}

type SMTPMatch struct {
	From    string `json:"from"` // any, local, auth, socket, src
	FromArg string `json:"fromArg"`
	For     string `json:"for"` // any, local, domain
	ForArg  string `json:"forArg"`
	Action  string `json:"action"` // Aktionsname oder „reject“
	Comment string `json:"comment"`
	Raw     string `json:"raw,omitempty"`
}

type SMTPConf struct {
	General []string     `json:"general"` // Rohzeilen (nur Anzeige), bleiben unverändert
	Tables  []SMTPTable  `json:"tables"`
	PKIs    []SMTPPKI    `json:"pkis"`
	Listens []SMTPListen `json:"listens"`
	Actions []SMTPAction `json:"actions"`
	Matches []SMTPMatch  `json:"matches"`
}

type SMTPView struct {
	Conf       SMTPConf `json:"conf"`
	Running    bool     `json:"running"`
	Enabled    bool     `json:"enabled"`
	Interfaces []string `json:"interfaces"`
	TableFiles []string `json:"tableFiles"`
	Certs      []string `json:"certificates"`
	Keys       []string `json:"keys"`
}

var (
	reSMTPName   = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,40}$`)
	reSMTPHost   = regexp.MustCompile(`^((smtp|smtp\+tls|smtp\+notls|smtps|lmtp)://)?([A-Za-z0-9._-]{1,64}@)?[A-Za-z0-9.-]{1,253}(:[0-9]{1,5})?$`)
	reSMTPMail   = regexp.MustCompile(`^(~/)?[A-Za-z0-9._/-]{0,100}$`)
	reSMTPDomain = regexp.MustCompile(`^(\*\.)?[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
)

func fieldsQ(l string) []string {
	var out []string
	for _, t := range tokenize(l) {
		out = append(out, t.s)
	}
	return out
}

func tableRef(s string) (string, bool) {
	if strings.HasPrefix(s, "<") && strings.HasSuffix(s, ">") && len(s) > 2 {
		return s[1 : len(s)-1], true
	}
	return "", false
}

// parseSMTPLine liefert den Typ und fügt das Element ein. Rückgabe false = nicht verstanden.
func parseSMTPLine(l string, c *SMTPConf, comment string) bool {
	f := fieldsQ(l)
	if len(f) < 2 || strings.ContainsAny(l, "{}=$") {
		return false
	}
	switch f[0] {
	case "table":
		if len(f) == 3 {
			t := SMTPTable{Name: f[1], Type: "file", Path: f[2], Comment: comment}
			if i := strings.Index(f[2], ":"); i > 0 && (f[2][:i] == "file" || f[2][:i] == "db") {
				t.Type, t.Path = f[2][:i], f[2][i+1:]
			}
			c.Tables = append(c.Tables, t)
			return true
		}
	case "pki":
		if len(f) == 4 && (f[2] == "cert" || f[2] == "key") {
			for i := range c.PKIs {
				if c.PKIs[i].Name == f[1] {
					if f[2] == "cert" {
						c.PKIs[i].Cert = f[3]
					} else {
						c.PKIs[i].Key = f[3]
					}
					return true
				}
			}
			p := SMTPPKI{Name: f[1], Comment: comment}
			if f[2] == "cert" {
				p.Cert = f[3]
			} else {
				p.Key = f[3]
			}
			c.PKIs = append(c.PKIs, p)
			return true
		}
	case "listen":
		if f[1] != "on" || len(f) < 3 {
			return false
		}
		li := SMTPListen{On: f[2], Comment: comment}
		for i := 3; i < len(f); i++ {
			switch f[i] {
			case "inet4", "inet6":
				li.Family = f[i]
			case "port":
				if i+1 >= len(f) {
					return false
				}
				li.Port = atoiSafe(f[i+1])
				i++
			case "tls", "tls-require", "smtps":
				li.TLS = f[i]
			case "pki":
				if i+1 >= len(f) {
					return false
				}
				li.PKI = f[i+1]
				i++
			case "auth", "auth-optional":
				li.Auth = f[i]
			case "hostname":
				if i+1 >= len(f) {
					return false
				}
				li.Hostname = f[i+1]
				i++
			case "mask-src":
				li.MaskSrc = true
			default:
				return false
			}
		}
		c.Listens = append(c.Listens, li)
		return true
	case "action":
		if len(f) < 3 {
			return false
		}
		a := SMTPAction{Name: f[1], Method: f[2], Comment: comment}
		i := 3
		switch a.Method {
		case "mbox", "relay", "expand-only", "forward-only":
		case "maildir":
			if i < len(f) && f[i] != "junk" && f[i] != "alias" && f[i] != "virtual" && f[i] != "userbase" {
				a.Path = f[i]
				i++
			}
		case "lmtp":
			if i >= len(f) {
				return false
			}
			a.Path = f[i]
			i++
		default:
			return false
		}
		for ; i < len(f); i++ {
			ref := func() (string, bool) {
				if i+1 >= len(f) {
					return "", false
				}
				t, ok := tableRef(f[i+1])
				i++
				return t, ok
			}
			arg := func() (string, bool) {
				if i+1 >= len(f) {
					return "", false
				}
				i++
				return f[i], true
			}
			var ok bool
			switch f[i] {
			case "junk":
				a.Junk, ok = true, a.Method == "maildir"
			case "alias":
				a.Alias, ok = ref()
			case "virtual":
				a.Virtual, ok = ref()
			case "userbase":
				a.Userbase, ok = ref()
			case "host":
				a.Host, ok = arg()
				ok = ok && a.Method == "relay"
			case "auth":
				a.Auth, ok = ref()
				ok = ok && a.Method == "relay"
			case "helo":
				a.Helo, ok = arg()
			case "src":
				a.Src, ok = arg()
			case "tls":
				a.TLS, ok = "tls", a.Method == "relay"
				if i+1 < len(f) && f[i+1] == "no-verify" {
					a.TLS = "no-verify"
					i++
				}
			}
			if !ok {
				return false
			}
		}
		c.Actions = append(c.Actions, a)
		return true
	case "match":
		m := SMTPMatch{From: "local", For: "local", Comment: comment}
		i := 1
		for ; i < len(f); i++ {
			switch f[i] {
			case "from":
				if i+1 >= len(f) {
					return false
				}
				m.From = f[i+1]
				i++
				if m.From == "src" {
					if i+1 >= len(f) {
						return false
					}
					m.FromArg = f[i+1]
					i++
				}
				if m.From != "any" && m.From != "local" && m.From != "auth" && m.From != "socket" && m.From != "src" {
					return false
				}
			case "for":
				if i+1 >= len(f) {
					return false
				}
				m.For = f[i+1]
				i++
				if m.For == "domain" {
					if i+1 >= len(f) {
						return false
					}
					m.ForArg = f[i+1]
					i++
				}
				if m.For != "any" && m.For != "local" && m.For != "domain" {
					return false
				}
			case "action":
				if i+1 >= len(f) || i+2 != len(f) {
					return false
				}
				m.Action = f[i+1]
				c.Matches = append(c.Matches, m)
				return true
			case "reject":
				if i+1 != len(f) {
					return false
				}
				m.Action = "reject"
				c.Matches = append(c.Matches, m)
				return true
			default:
				return false
			}
		}
	}
	return false
}

type smtpItem struct {
	kind string // table, pki, listen, action, match, raw
	idx  int
	raw  string
	cmt  string
}

func parseSMTPD(src string) (SMTPConf, []smtpItem) {
	c := SMTPConf{General: []string{}, Tables: []SMTPTable{}, PKIs: []SMTPPKI{}, Listens: []SMTPListen{}, Actions: []SMTPAction{}, Matches: []SMTPMatch{}}
	var items []smtpItem
	var comments []string
	for _, l := range splitFileLines(src) {
		t := strings.TrimSpace(l)
		switch {
		case t == "":
			if len(comments) > 0 {
				c.General = append(c.General, comments...)
				items = append(items, smtpItem{kind: "raw", raw: strings.Join(comments, "\n")})
				comments = nil
			}
		case strings.HasPrefix(t, "#"):
			comments = append(comments, l)
		default:
			cmt := strings.TrimSpace(strings.TrimPrefix(strings.Join(comments, " "), "#"))
			cmt = strings.ReplaceAll(cmt, "# ", "")
			var probe SMTPConf
			if parseSMTPLine(t, &probe, cmt) {
				before := [5]int{len(c.Tables), len(c.PKIs), len(c.Listens), len(c.Actions), len(c.Matches)}
				parseSMTPLine(t, &c, cmt)
				for k, kind := range []string{"table", "pki", "listen", "action", "match"} {
					n := [5]int{len(c.Tables), len(c.PKIs), len(c.Listens), len(c.Actions), len(c.Matches)}
					if n[k] > before[k] {
						items = append(items, smtpItem{kind: kind, idx: n[k] - 1, cmt: cmt})
					}
				}
			} else {
				raw := strings.Join(append(append([]string{}, comments...), l), "\n")
				switch firstWord(t) {
				case "table":
					c.Tables = append(c.Tables, SMTPTable{Raw: raw})
				case "pki":
					c.PKIs = append(c.PKIs, SMTPPKI{Raw: raw})
				case "listen":
					c.Listens = append(c.Listens, SMTPListen{Raw: raw})
				case "action":
					c.Actions = append(c.Actions, SMTPAction{Raw: raw})
				case "match":
					c.Matches = append(c.Matches, SMTPMatch{Raw: raw})
				default:
					c.General = append(c.General, comments...)
					c.General = append(c.General, l)
					items = append(items, smtpItem{kind: "raw", raw: raw})
				}
			}
			comments = nil
		}
	}
	if len(comments) > 0 {
		c.General = append(c.General, comments...)
		items = append(items, smtpItem{kind: "raw", raw: strings.Join(comments, "\n")})
	}
	return c, items
}

func (h *Host) SMTPD(ctx context.Context) (*SMTPView, error) {
	src, err := h.readFile(smtpdPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	c, _ := parseSMTPD(src)
	v := &SMTPView{Conf: c, Interfaces: []string{"socket"}, TableFiles: []string{}, Certs: []string{}, Keys: []string{}}
	v.Enabled, v.Running = h.rcActive(ctx, "smtpd")
	if out, err := h.out(ctx, "ifconfig", "-A"); err == nil {
		seen := map[string]bool{"egress": true}
		v.Interfaces = append(v.Interfaces, "egress")
		for _, i := range ParseIfconfig(out) {
			if !strings.HasPrefix(i.Name, "pflog") && !seen[i.Name] {
				seen[i.Name] = true
				v.Interfaces = append(v.Interfaces, i.Name)
			}
		}
	}
	v.Interfaces = append(v.Interfaces, h.localAddrs(ctx)...)
	list := func(dir string, ok func(string) bool) []string {
		var out []string
		if ents, err := os.ReadDir(h.P(dir)); err == nil {
			for _, e := range ents {
				if !e.IsDir() && ok(e.Name()) {
					out = append(out, filepath.Join(dir, e.Name()))
				}
			}
		}
		return out
	}
	v.TableFiles = list("/etc/mail", func(n string) bool { return n != "smtpd.conf" && !strings.HasSuffix(n, ".db") })
	v.Certs = list("/etc/ssl", func(n string) bool { return strings.HasSuffix(n, ".crt") || strings.HasSuffix(n, ".pem") })
	v.Keys = list("/etc/ssl/private", func(n string) bool { return strings.HasSuffix(n, ".key") || strings.HasSuffix(n, ".pem") })
	return v, nil
}

// ---- Prüfen ----

func (h *Host) validateSMTPD(c *SMTPConf, cur *SMTPView) error {
	if len(c.Tables) > 30 || len(c.PKIs) > 20 || len(c.Listens) > 30 || len(c.Actions) > 50 || len(c.Matches) > 200 {
		return invalid("zu viele Einträge")
	}
	tables, pkis, actions := map[string]bool{}, map[string]bool{}, map[string]bool{}
	has := func(list []string, s string) bool {
		for _, x := range list {
			if x == s {
				return true
			}
		}
		return false
	}
	for _, t := range c.Tables {
		if t.Raw != "" {
			if f := fieldsQ(strings.TrimSpace(t.Raw[strings.LastIndex(t.Raw, "\n")+1:])); len(f) > 1 {
				tables[f[1]] = true
			}
			continue
		}
		if !reSMTPName.MatchString(t.Name) || tables[t.Name] {
			return invalid("Tabelle %q (Name ungültig oder doppelt)", t.Name)
		}
		tables[t.Name] = true
		if t.Type != "file" && t.Type != "db" {
			return invalid("Tabelle %s: Art file oder db", t.Name)
		}
		if !has(cur.TableFiles, t.Path) && !has(tableFilesOf(cur.Conf), t.Path) {
			return invalid("Tabelle %s: Datei %q gibt es in /etc/mail nicht", t.Name, t.Path)
		}
	}
	for _, p := range c.PKIs {
		if p.Raw != "" {
			continue
		}
		if !reSMTPName.MatchString(p.Name) || pkis[p.Name] {
			return invalid("Zertifikat %q (Name ungültig oder doppelt)", p.Name)
		}
		pkis[p.Name] = true
		if !has(cur.Certs, p.Cert) || !has(cur.Keys, p.Key) {
			return invalid("Zertifikat %s: Datei oder Schlüssel gibt es nicht in /etc/ssl", p.Name)
		}
	}
	ifs := map[string]bool{}
	for _, i := range cur.Interfaces {
		ifs[i] = true
	}
	for _, l := range c.Listens {
		if l.Raw != "" {
			continue
		}
		if !ifs[l.On] {
			if _, err := netip.ParseAddr(l.On); err != nil {
				return invalid("listen on %q: Interface oder Adresse gibt es nicht", l.On)
			}
		}
		if l.On == "socket" && (l.Family != "" || l.Port != 0 || l.TLS != "" || l.Auth != "" || l.PKI != "") {
			return invalid("Der Socket kennt weder Port, TLS noch Anmeldung")
		}
		if l.Family != "" && l.Family != "inet4" && l.Family != "inet6" {
			return invalid("Adressfamilie %q", l.Family)
		}
		if l.Port < 0 || l.Port > 65535 {
			return invalid("Port %d", l.Port)
		}
		switch l.TLS {
		case "", "tls", "tls-require", "smtps":
		default:
			return invalid("TLS-Modus %q", l.TLS)
		}
		if l.TLS != "" && !pkis[l.PKI] {
			return invalid("listen on %s: TLS braucht ein vorhandenes Zertifikat (pki)", l.On)
		}
		if l.TLS == "" && l.PKI != "" && !pkis[l.PKI] {
			return invalid("listen on %s: Zertifikat %q gibt es nicht", l.On, l.PKI)
		}
		if l.Auth != "" && l.Auth != "auth" && l.Auth != "auth-optional" {
			return invalid("Anmeldung %q", l.Auth)
		}
		if l.Auth != "" && l.TLS == "" && l.On != "socket" {
			return invalid("listen on %s: Anmeldung ohne TLS würde Passwörter im Klartext senden", l.On)
		}
		if l.Hostname != "" && !reHostLabel.MatchString(l.Hostname) {
			return invalid("Hostname %q", l.Hostname)
		}
	}
	for _, a := range c.Actions {
		actions[a.Name] = true
		if a.Raw != "" { // Rohaktionen: Namen aus der Zeile
			if f := fieldsQ(strings.TrimSpace(a.Raw[strings.LastIndex(a.Raw, "\n")+1:])); len(f) > 1 {
				actions[f[1]] = true
			}
		}
	}
	for _, a := range c.Actions {
		if a.Raw != "" {
			continue
		}
		if !reSMTPName.MatchString(a.Name) {
			return invalid("Aktion %q", a.Name)
		}
		switch a.Method {
		case "mbox", "maildir", "lmtp", "relay", "expand-only", "forward-only":
		default:
			return invalid("Aktion %s: Zustellart %q", a.Name, a.Method)
		}
		for _, r := range []struct{ what, t string }{{"alias", a.Alias}, {"virtual", a.Virtual}, {"userbase", a.Userbase}, {"auth", a.Auth}} {
			if r.t != "" && !tables[r.t] {
				return invalid("Aktion %s: Tabelle <%s> (%s) gibt es nicht", a.Name, r.t, r.what)
			}
		}
		if a.Method == "maildir" && a.Path != "" && !reSMTPMail.MatchString(a.Path) {
			return invalid("Aktion %s: Maildir-Pfad %q", a.Name, a.Path)
		}
		if a.Method == "lmtp" && !reSMTPHost.MatchString(a.Path) && !strings.HasPrefix(a.Path, "/") {
			return invalid("Aktion %s: LMTP-Ziel %q", a.Name, a.Path)
		}
		if a.Method == "relay" {
			if a.Host != "" && !reSMTPHost.MatchString(a.Host) {
				return invalid("Aktion %s: Relay-Adresse %q (z. B. smtp+tls://mail.example.org:587)", a.Name, a.Host)
			}
			if a.Auth != "" && a.Host == "" {
				return invalid("Aktion %s: Anmeldung am Relay braucht eine Relay-Adresse", a.Name)
			}
			if a.Auth != "" && a.TLS == "" && !strings.Contains(a.Host, "smtps://") && !strings.Contains(a.Host, "+tls://") {
				return invalid("Aktion %s: Anmeldung am Relay nur mit TLS (sonst Klartext-Passwort)", a.Name)
			}
		} else if a.Host != "" || a.TLS != "" || a.Auth != "" {
			return invalid("Aktion %s: Relay-Optionen gibt es nur bei relay", a.Name)
		}
		if a.Helo != "" && !reHostLabel.MatchString(a.Helo) {
			return invalid("Aktion %s: HELO-Name %q", a.Name, a.Helo)
		}
		if a.Src != "" {
			if _, err := netip.ParseAddr(a.Src); err != nil {
				return invalid("Aktion %s: Absenderadresse %q", a.Name, a.Src)
			}
		}
	}
	for i, m := range c.Matches {
		if m.Raw != "" {
			continue
		}
		n := i + 1
		switch m.From {
		case "any", "local", "auth", "socket":
		case "src":
			if _, e1 := netip.ParsePrefix(m.FromArg); e1 != nil {
				if _, e2 := netip.ParseAddr(m.FromArg); e2 != nil {
					return invalid("Regel %d: Quelladresse %q", n, m.FromArg)
				}
			}
		default:
			return invalid("Regel %d: Absender %q", n, m.From)
		}
		switch m.For {
		case "any", "local":
		case "domain":
			if !reSMTPDomain.MatchString(m.ForArg) {
				return invalid("Regel %d: Domain %q", n, m.ForArg)
			}
		default:
			return invalid("Regel %d: Empfänger %q", n, m.For)
		}
		if m.Action != "reject" && !actions[m.Action] {
			return invalid("Regel %d: Aktion %q gibt es nicht", n, m.Action)
		}
		if m.Action != "reject" {
			for _, a := range c.Actions {
				if a.Name == m.Action && a.Method == "relay" && (m.From == "any" || (m.From == "src" && (m.FromArg == "0.0.0.0/0" || m.FromArg == "::/0"))) && m.For == "any" {
					return invalid("Regel %d würde jedem Absender erlauben, über diesen Server an beliebige Empfänger zu senden (offenes Relay)", n)
				}
			}
		}
	}
	return nil
}

func tableFilesOf(c SMTPConf) []string {
	var out []string
	for _, t := range c.Tables {
		out = append(out, t.Path)
	}
	return out
}

// ---- Schreiben ----

func dq(s string) string { return `"` + s + `"` }

func (t SMTPTable) line() string { return fmt.Sprintf("table %s %s:%s", t.Name, t.Type, t.Path) }

func (p SMTPPKI) lines() []string {
	return []string{fmt.Sprintf("pki %s cert %s", p.Name, dq(p.Cert)), fmt.Sprintf("pki %s key %s", p.Name, dq(p.Key))}
}

func (l SMTPListen) line() string {
	s := "listen on " + l.On
	if l.Family != "" {
		s += " " + l.Family
	}
	if l.Port != 0 && !(l.TLS == "smtps" && l.Port == 465) {
		s += fmt.Sprintf(" port %d", l.Port)
	}
	if l.TLS != "" {
		s += " " + l.TLS
	}
	if l.PKI != "" {
		s += " pki " + l.PKI
	}
	if l.Auth != "" {
		s += " " + l.Auth
	}
	if l.Hostname != "" {
		s += " hostname " + l.Hostname
	}
	if l.MaskSrc {
		s += " mask-src"
	}
	return s
}

func (a SMTPAction) line() string {
	s := fmt.Sprintf("action %s %s", dq(a.Name), a.Method)
	switch a.Method {
	case "maildir":
		if a.Path != "" {
			s += " " + dq(a.Path)
		}
		if a.Junk {
			s += " junk"
		}
	case "lmtp":
		s += " " + dq(a.Path)
	}
	for _, o := range []struct{ k, v string }{{"alias", a.Alias}, {"virtual", a.Virtual}, {"userbase", a.Userbase}} {
		if o.v != "" {
			s += fmt.Sprintf(" %s <%s>", o.k, o.v)
		}
	}
	if a.Method == "relay" {
		if a.Host != "" {
			s += " host " + dq(a.Host)
		}
		switch a.TLS {
		case "tls":
			s += " tls"
		case "no-verify":
			s += " tls no-verify"
		}
		if a.Auth != "" {
			s += fmt.Sprintf(" auth <%s>", a.Auth)
		}
	}
	if a.Helo != "" {
		s += " helo " + dq(a.Helo)
	}
	if a.Src != "" {
		s += " src " + a.Src
	}
	return s
}

func (m SMTPMatch) line() string {
	s := "match from " + m.From
	if m.From == "src" {
		s += " " + m.FromArg
	}
	s += " for " + m.For
	if m.For == "domain" {
		s += " " + m.ForArg
	}
	if m.Action == "reject" {
		return s + " reject"
	}
	return s + " action " + dq(m.Action)
}

func cmtLines(c string) []string {
	if c == "" {
		return nil
	}
	return []string{"# " + c}
}

// SaveSMTPD schreibt /etc/mail/smtpd.conf aus dem Formular. Rohzeilen bleiben vorn stehen.
func (h *Host) SaveSMTPD(ctx context.Context, c *SMTPConf, activate, preview bool) (*ConfResult, error) {
	cur, err := h.SMTPD(ctx)
	if err != nil {
		return nil, err
	}
	// Rohzeilen gibt es nur, wenn sie so schon in der Datei stehen.
	keep := map[string]bool{}
	for _, t := range cur.Conf.Tables {
		keep[t.Raw] = true
	}
	for _, t := range cur.Conf.PKIs {
		keep[t.Raw] = true
	}
	for _, t := range cur.Conf.Listens {
		keep[t.Raw] = true
	}
	for _, t := range cur.Conf.Actions {
		keep[t.Raw] = true
	}
	for _, t := range cur.Conf.Matches {
		keep[t.Raw] = true
	}
	var raws []string
	for _, t := range c.Tables {
		raws = append(raws, t.Raw)
	}
	for _, t := range c.PKIs {
		raws = append(raws, t.Raw)
	}
	for _, t := range c.Listens {
		raws = append(raws, t.Raw)
	}
	for _, t := range c.Actions {
		raws = append(raws, t.Raw)
	}
	for _, t := range c.Matches {
		raws = append(raws, t.Raw)
	}
	for _, r := range raws {
		if r != "" && !keep[r] {
			return nil, invalid("Rohzeilen lassen sich nur übernehmen, nicht neu anlegen")
		}
	}
	if err := h.validateSMTPD(c, cur); err != nil {
		return nil, err
	}
	var out []string
	if len(cur.Conf.General) > 0 {
		out = append(out, cur.Conf.General...)
		out = append(out, "")
	}
	section := func(lines [][]string) {
		for _, l := range lines {
			out = append(out, l...)
		}
		if len(lines) > 0 {
			out = append(out, "")
		}
	}
	var tl, pl, ll, al, ml [][]string
	rawOr := func(raw string, cmt string, lines ...string) []string {
		if raw != "" {
			return strings.Split(raw, "\n")
		}
		return append(cmtLines(cmt), lines...)
	}
	for _, t := range c.Tables {
		tl = append(tl, rawOr(t.Raw, t.Comment, t.line()))
	}
	for _, p := range c.PKIs {
		pl = append(pl, rawOr(p.Raw, p.Comment, p.lines()...))
	}
	for _, l := range c.Listens {
		ll = append(ll, rawOr(l.Raw, l.Comment, l.line()))
	}
	for _, a := range c.Actions {
		al = append(al, rawOr(a.Raw, a.Comment, a.line()))
	}
	for _, m := range c.Matches {
		ml = append(ml, rawOr(m.Raw, m.Comment, m.line()))
	}
	section(tl)
	section(pl)
	section(ll)
	section(al)
	section(ml)
	content := strings.TrimRight(strings.Join(out, "\n"), "\n") + "\n"
	res := &ConfResult{Warnings: []string{}}
	if len(c.Listens) == 0 {
		res.Warnings = append(res.Warnings, "Ohne „listen on“ nimmt smtpd keine Mail an.")
	}
	if len(c.Matches) == 0 {
		res.Warnings = append(res.Warnings, "Ohne match-Regel wird keine Mail zugestellt oder weitergeleitet.")
	}
	var ports []int
	for _, l := range c.Listens {
		if l.On == "socket" || l.On == "lo0" || l.On == "127.0.0.1" || l.On == "::1" {
			continue
		}
		p := l.Port
		if p == 0 {
			p = 25
			if l.TLS == "smtps" {
				p = 465
			}
		}
		ports = append(ports, p)
	}
	sort.Ints(ports)
	if len(ports) > 0 {
		pf, _ := h.readFile(pfConfPath)
		for _, p := range ports {
			if !strings.Contains(pf, fmt.Sprint(p)) && !strings.Contains(pf, "smtp") {
				res.Warnings = append(res.Warnings, fmt.Sprintf("Port %d kommt in /etc/pf.conf nicht vor – der Paketfilter blockiert ihn vermutlich.", p))
			}
		}
	}
	if err := h.putConf(ctx, smtpdPath, content, preview, res); err != nil {
		return nil, err
	}
	if preview {
		return res, nil
	}
	enabled, running := h.rcActive(ctx, "smtpd")
	switch {
	case running:
		err = h.run(ctx, "rcctl", "restart", "smtpd")
		res.Applied = "smtpd neu gestartet."
	case activate && !enabled:
		if err = h.run(ctx, "rcctl", "enable", "smtpd"); err == nil {
			err = h.run(ctx, "rcctl", "start", "smtpd")
		}
		res.Applied = "smtpd aktiviert und gestartet."
	case activate:
		err = h.run(ctx, "rcctl", "start", "smtpd")
		res.Applied = "smtpd gestartet."
	default:
		res.Applied = "Gespeichert; smtpd läuft nicht und wurde nicht gestartet."
	}
	if err != nil {
		return nil, fmt.Errorf("Datei gespeichert, aber smtpd ließ sich nicht (neu) starten: %w", err)
	}
	return res, nil
}
