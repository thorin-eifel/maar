package obsd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const relaydPath = "/etc/relayd.conf"

// Lastverteilung mit relayd(8) als Formular: Tabellen (Zielhosts, aus den VM-Adressen wählbar),
// Weiterleitungen (redirect, Schicht 3) und Relays (Schicht 7). Protokolle bleiben unverändert;
// neue entstehen aus Vorlagen. Unbekannte Anweisungen bleiben wörtlich erhalten.

type RelaydHost struct {
	Addr  string `json:"addr"`
	Retry int    `json:"retry"`
}

type RelaydTable struct {
	ID      int          `json:"id"`
	Name    string       `json:"name"`
	Disable bool         `json:"disable"`
	Hosts   []RelaydHost `json:"hosts"`
	Raw     string       `json:"raw,omitempty"` // Tabelle mit Makros o. ä.: unverändert
}

type RelaydCheck struct {
	Kind string `json:"kind"` // "", icmp, tcp, tls, http, https
	Path string `json:"path"`
	Code int    `json:"code"`
}

type RelaydListen struct {
	Addr  string `json:"addr"`
	Proto string `json:"proto"` // "", tcp, udp (nur redirect)
	Port  string `json:"port"`  // Zahl oder min:max
	TLS   bool   `json:"tls"`   // nur relay
}

type RelaydService struct {
	ID       int            `json:"id"`
	Kind     string         `json:"kind"` // redirect, relay
	Name     string         `json:"name"`
	Disable  bool           `json:"disable"`
	Listen   []RelaydListen `json:"listen"`
	Protocol string         `json:"protocol"` // nur relay
	Table    string         `json:"table"`
	Port     int            `json:"port"`
	Mode     string         `json:"mode"`
	Check    RelaydCheck    `json:"check"`
	Timeout  int            `json:"sessionTimeout"`
	Sticky   bool           `json:"sticky"` // nur redirect
	Notes    []string       `json:"notes"`
	Raw      string         `json:"raw,omitempty"` // ganzer Block unverändert
	kept     []string
}

type RelaydProtocolNew struct {
	Name    string `json:"name"`
	Preset  string `json:"preset"`  // http, https, tcp
	Keypair string `json:"keypair"` // Name des Schlüsselpaars für https
}

type RelaydConf struct {
	Interval  int                 `json:"interval"`
	Timeout   int                 `json:"timeout"`
	Prefork   int                 `json:"prefork"`
	LogConn   bool                `json:"logConnection"`
	LogState  bool                `json:"logStateChanges"`
	Tables    []RelaydTable       `json:"tables"`
	Services  []RelaydService     `json:"services"`
	Protocols []RelaydProtocolNew `json:"newProtocols"`
}

type RelaydView struct {
	Conf      RelaydConf `json:"conf"`
	Protocols []string   `json:"protocols"` // vorhandene Protokollnamen
	Keypairs  []string   `json:"keypairs"`
	Addresses []string   `json:"addresses"`
	VMHosts   []VMHost   `json:"vmHosts"`
	Running   bool       `json:"running"`
	Enabled   bool       `json:"enabled"`
	Other     []string   `json:"other"` // übrige Anweisungen (Makros, include, …)
	PFAnchor  bool       `json:"pfAnchor"`
}

type VMHost struct {
	Addr string `json:"addr"`
	Name string `json:"name"`
}

var (
	reRelayName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,40}$`)
	reRelayPath = regexp.MustCompile(`^/[A-Za-z0-9._~/%?=&-]{0,200}$`)
	reRelayPort = regexp.MustCompile(`^[0-9]{1,5}(:[0-9]{1,5})?$`)
)

type relaydTop struct {
	kind    string // global, table, redirect, relay, protocol
	name    string
	lines   []string
	leading []string
}

func joinCont(lines []string) []string {
	var out []string
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		for strings.HasSuffix(strings.TrimRight(l, " \t"), "\\") && i+1 < len(lines) {
			l = strings.TrimSuffix(strings.TrimRight(l, " \t"), "\\") + " " + strings.TrimSpace(lines[i+1])
			i++
		}
		out = append(out, l)
	}
	return out
}

var reRelaydHead = regexp.MustCompile(`^\s*(?:(?:tcp|http|dns)\s+)?(table|redirect|relay|protocol)\s+("[^"]*"|<[^>]+>|\S+)`)

func parseRelaydTop(src string) []relaydTop {
	var out []relaydTop
	lines := joinCont(splitFileLines(src))
	var comments []string
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		t := strings.TrimSpace(l)
		switch {
		case t == "":
			if len(comments) > 0 {
				out = append(out, relaydTop{kind: "global", lines: comments})
				comments = nil
			}
			continue
		case strings.HasPrefix(t, "#"):
			comments = append(comments, l)
			continue
		}
		it := relaydTop{kind: "global", leading: comments, lines: []string{l}}
		comments = nil
		if m := reRelaydHead.FindStringSubmatch(l); m != nil {
			it.kind, it.name = m[1], strings.Trim(m[2], `"<>`)
		}
		if d := braceDelta(l); d > 0 {
			for i++; i < len(lines) && d > 0; i++ {
				it.lines = append(it.lines, lines[i])
				d += braceDelta(lines[i])
			}
			i--
		}
		out = append(out, it)
	}
	if len(comments) > 0 {
		out = append(out, relaydTop{kind: "global", lines: comments})
	}
	return out
}

func parseRelaydTable(it relaydTop, id int) RelaydTable {
	t := RelaydTable{ID: id, Name: it.name, Hosts: []RelaydHost{}}
	text := strings.Join(it.lines, "\n")
	if strings.ContainsAny(text, "$") || strings.Count(text, "{") != 1 {
		t.Raw = strings.Join(append(append([]string{}, it.leading...), it.lines...), "\n")
		return t
	}
	head := it.lines[0]
	t.Disable = strings.Contains(strings.SplitN(head, "{", 2)[0], " disable")
	body := text[strings.Index(text, "{")+1 : strings.LastIndex(text, "}")]
	body = strings.ReplaceAll(strings.ReplaceAll(body, ",", " "), "\n", " ")
	f := strings.Fields(body)
	for i := 0; i < len(f); i++ {
		switch f[i] {
		case "retry":
			if i+1 < len(f) && len(t.Hosts) > 0 {
				t.Hosts[len(t.Hosts)-1].Retry = atoiSafe(f[i+1])
				i++
			}
		case "ip", "parent", "priority":
			t.Raw = strings.Join(append(append([]string{}, it.leading...), it.lines...), "\n")
			return t
		default:
			t.Hosts = append(t.Hosts, RelaydHost{Addr: strings.Trim(f[i], `"`)})
		}
	}
	return t
}

// parseRelaydService liest redirect- und relay-Blöcke; Unbekanntes bleibt in kept.
func parseRelaydService(it relaydTop, id int) RelaydService {
	s := RelaydService{ID: id, Kind: it.kind, Name: it.name, Listen: []RelaydListen{}, Notes: []string{}}
	head := strings.Fields(strings.TrimSuffix(strings.TrimSpace(it.lines[0]), "{"))
	for _, x := range head {
		if x == "disable" {
			s.Disable = true
		}
	}
	whole := func() {
		s.Raw = strings.Join(append(append([]string{}, it.leading...), it.lines...), "\n")
		s.Notes = append(s.Notes, "Dieser Block enthält Anweisungen, die das Formular nicht kennt, und bleibt unverändert.")
	}
	if len(it.lines) < 2 || strings.Contains(it.lines[0], "}") {
		whole()
		return s
	}
	for _, l := range it.lines[1 : len(it.lines)-1] {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		f := strings.Fields(t)
		switch {
		case f[0] == "listen" && len(f) >= 5 && f[1] == "on":
			li := RelaydListen{Addr: f[2]}
			rest := f[3:]
			if rest[0] == "tcp" || rest[0] == "udp" {
				li.Proto, rest = rest[0], rest[1:]
			}
			if len(rest) >= 2 && rest[0] == "port" {
				li.Port = rest[1]
				rest = rest[2:]
				if len(rest) == 1 && rest[0] == "tls" {
					li.TLS, rest = true, nil
				}
				if len(rest) == 0 {
					s.Listen = append(s.Listen, li)
					continue
				}
			}
			whole()
			return s
		case f[0] == "protocol" && len(f) == 2:
			s.Protocol = strings.Trim(f[1], `"`)
		case f[0] == "session" && len(f) == 3 && f[1] == "timeout":
			s.Timeout = atoiSafe(f[2])
		case f[0] == "sticky-address" && len(f) == 1:
			s.Sticky = true
		case f[0] == "forward" && len(f) >= 3 && f[1] == "to" && strings.HasPrefix(f[2], "<") && s.Table == "":
			ok := true
			s.Table = strings.Trim(f[2], "<>")
			for i := 3; i < len(f) && ok; i++ {
				switch f[i] {
				case "port":
					if i+1 < len(f) {
						s.Port = atoiSafe(f[i+1])
						i++
					} else {
						ok = false
					}
				case "mode":
					if i+1 < len(f) {
						s.Mode = f[i+1]
						i++
					} else {
						ok = false
					}
				case "check":
					if i+1 >= len(f) {
						ok = false
						break
					}
					switch f[i+1] {
					case "icmp", "tcp", "tls":
						s.Check.Kind = f[i+1]
						i++
					case "http", "https":
						if i+4 < len(f)+0 && f[i+3] == "code" && i+4 < len(f) {
							s.Check = RelaydCheck{Kind: f[i+1], Path: strings.Trim(f[i+2], `"`), Code: atoiSafe(f[i+4])}
							i += 4
						} else {
							ok = false
						}
					default:
						ok = false
					}
				default:
					ok = false
				}
			}
			if !ok {
				s.Table, s.Port, s.Mode, s.Check = "", 0, "", RelaydCheck{}
				s.kept = append(s.kept, "\t"+t)
				s.Notes = append(s.Notes, t)
			}
		default:
			s.kept = append(s.kept, l)
			s.Notes = append(s.Notes, t)
		}
	}
	return s
}

func (h *Host) Relayd(ctx context.Context) (*RelaydView, error) {
	src, err := h.readFile(relaydPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	v := &RelaydView{Conf: RelaydConf{Tables: []RelaydTable{}, Services: []RelaydService{}, Protocols: []RelaydProtocolNew{}}, Protocols: []string{}, Keypairs: []string{}, Addresses: []string{}, VMHosts: []VMHost{}, Other: []string{}}
	for i, it := range parseRelaydTop(src) {
		switch it.kind {
		case "table":
			v.Conf.Tables = append(v.Conf.Tables, parseRelaydTable(it, i+1))
		case "redirect", "relay":
			v.Conf.Services = append(v.Conf.Services, parseRelaydService(it, i+1))
		case "protocol":
			v.Protocols = append(v.Protocols, it.name)
		default:
			for _, l := range it.lines {
				t := strings.TrimSpace(l)
				switch {
				case strings.HasPrefix(t, "interval "):
					v.Conf.Interval = atoiSafe(strings.TrimPrefix(t, "interval "))
				case strings.HasPrefix(t, "timeout "):
					v.Conf.Timeout = atoiSafe(strings.TrimPrefix(t, "timeout "))
				case strings.HasPrefix(t, "prefork "):
					v.Conf.Prefork = atoiSafe(strings.TrimPrefix(t, "prefork "))
				case t == "log connection":
					v.Conf.LogConn = true
				case t == "log state changes":
					v.Conf.LogState = true
				case t != "" && !strings.HasPrefix(t, "#"):
					v.Other = append(v.Other, t)
				}
			}
		}
	}
	v.Enabled, v.Running = h.rcActive(ctx, "relayd")
	v.Addresses = append([]string{}, h.localAddrs(ctx)...)
	if net, err := h.VNet(ctx); err == nil {
		for _, n := range net.NICs {
			if n.IP != "" {
				v.VMHosts = append(v.VMHosts, VMHost{Addr: n.IP, Name: n.VM})
			}
		}
	}
	// Schlüsselpaare: /etc/ssl/<name>.crt zusammen mit /etc/ssl/private/<name>.key
	if ents, err := os.ReadDir(h.P("/etc/ssl")); err == nil {
		for _, e := range ents {
			n := strings.TrimSuffix(e.Name(), ".crt")
			if !e.IsDir() && n != e.Name() {
				if _, err := os.Stat(h.P(filepath.Join("/etc/ssl/private", n+".key"))); err == nil {
					v.Keypairs = append(v.Keypairs, n)
				}
			}
		}
	}
	sort.Strings(v.Keypairs)
	pf, _ := h.readFile(pfConfPath)
	v.PFAnchor = strings.Contains(pf, `anchor "relayd/*"`)
	return v, nil
}

func (c RelaydCheck) text() string {
	switch c.Kind {
	case "icmp", "tcp", "tls":
		return " check " + c.Kind
	case "http", "https":
		return fmt.Sprintf(" check %s %q code %d", c.Kind, c.Path, c.Code)
	}
	return ""
}

func (s RelaydService) lines() []string {
	var out []string
	head := fmt.Sprintf("%s %q", s.Kind, s.Name)
	if s.Disable {
		head += " disable"
	}
	out = append(out, head+" {")
	for _, l := range s.Listen {
		t := "\tlisten on " + l.Addr
		if l.Proto != "" {
			t += " " + l.Proto
		}
		t += " port " + l.Port
		if l.TLS {
			t += " tls"
		}
		out = append(out, t)
	}
	if s.Kind == "relay" && s.Protocol != "" {
		out = append(out, fmt.Sprintf("\tprotocol %q", s.Protocol))
	}
	if s.Table != "" {
		t := "\tforward to <" + s.Table + ">"
		if s.Port > 0 {
			t += fmt.Sprintf(" port %d", s.Port)
		}
		if s.Mode != "" {
			t += " mode " + s.Mode
		}
		out = append(out, t+s.Check.text())
	}
	if s.Timeout > 0 {
		out = append(out, fmt.Sprintf("\tsession timeout %d", s.Timeout))
	}
	if s.Sticky && s.Kind == "redirect" {
		out = append(out, "\tsticky-address")
	}
	out = append(out, s.kept...)
	out = append(out, "}")
	return out
}

func (t RelaydTable) lines() []string {
	head := "table <" + t.Name + ">"
	if t.Disable {
		head += " disable"
	}
	var hs []string
	for _, h := range t.Hosts {
		x := h.Addr
		if h.Retry > 0 {
			x += fmt.Sprintf(" retry %d", h.Retry)
		}
		hs = append(hs, x)
	}
	return []string{head + " { " + strings.Join(hs, ", ") + " }"}
}

func protocolPreset(p RelaydProtocolNew) []string {
	switch p.Preset {
	case "http":
		return []string{fmt.Sprintf("http protocol %q {", p.Name), `	match header set "X-Forwarded-For" value "$REMOTE_ADDR"`, `	match header set "X-Forwarded-By" value "$SERVER_ADDR:$SERVER_PORT"`, "	pass", "}"}
	case "https":
		return []string{fmt.Sprintf("http protocol %q {", p.Name), `	match header set "X-Forwarded-For" value "$REMOTE_ADDR"`, `	match header set "X-Forwarded-Proto" value "https"`, "	pass", fmt.Sprintf("	tls { keypair %q }", p.Keypair), "}"}
	default:
		return []string{fmt.Sprintf("tcp protocol %q {", p.Name), "	tcp { nodelay }", "}"}
	}
}

func (h *Host) validateRelayd(c *RelaydConf, cur *RelaydView) error {
	if c.Interval < 0 || c.Interval > 3600 || c.Timeout < 0 || c.Timeout > 60000 || c.Prefork < 0 || c.Prefork > 32 {
		return invalid("interval (1–3600 s), timeout (1–60000 ms) oder prefork (1–32) außerhalb des Bereichs")
	}
	if c.Interval > 0 && c.Timeout > 0 && c.Timeout > c.Interval*1000 {
		return invalid("Der Timeout (%d ms) darf das Intervall (%d s) nicht überschreiten", c.Timeout, c.Interval)
	}
	if len(c.Tables) > 50 || len(c.Services) > 100 || len(c.Protocols) > 20 {
		return invalid("zu viele Einträge")
	}
	tables := map[string]bool{}
	for _, t := range c.Tables {
		if t.Raw != "" {
			tables[t.Name] = true
			continue
		}
		if !reRelayName.MatchString(t.Name) || tables[t.Name] {
			return invalid("Tabelle %q (Name ungültig oder doppelt)", t.Name)
		}
		tables[t.Name] = true
		if len(t.Hosts) == 0 || len(t.Hosts) > 100 {
			return invalid("Tabelle <%s>: ein bis 100 Hosts", t.Name)
		}
		for _, hh := range t.Hosts {
			if !validHostOrIP(hh.Addr) {
				return invalid("Tabelle <%s>: Host %q", t.Name, hh.Addr)
			}
			if hh.Retry < 0 || hh.Retry > 10 {
				return invalid("Tabelle <%s>: retry %d (0–10)", t.Name, hh.Retry)
			}
		}
	}
	protos := map[string]bool{}
	for _, p := range cur.Protocols {
		protos[p] = true
	}
	kp := map[string]bool{}
	for _, k := range cur.Keypairs {
		kp[k] = true
	}
	for _, p := range c.Protocols {
		if !reRelayName.MatchString(p.Name) || protos[p.Name] {
			return invalid("Protokoll %q (Name ungültig oder schon vorhanden)", p.Name)
		}
		protos[p.Name] = true
		switch p.Preset {
		case "http", "tcp":
		case "https":
			if !kp[p.Keypair] {
				return invalid("Protokoll %s: Es gibt kein Schlüsselpaar %q (/etc/ssl/<name>.crt und /etc/ssl/private/<name>.key)", p.Name, p.Keypair)
			}
		default:
			return invalid("Protokollvorlage %q", p.Preset)
		}
	}
	addrs := map[string]bool{"*": true}
	for _, a := range cur.Addresses {
		addrs[a] = true
	}
	for _, s := range cur.Conf.Services {
		for _, l := range s.Listen {
			addrs[l.Addr] = true
		}
	}
	seen := map[string]bool{}
	for _, s := range c.Services {
		if s.Raw != "" {
			seen[s.Kind+":"+s.Name] = true
			continue
		}
		if s.Kind != "redirect" && s.Kind != "relay" {
			return invalid("Dienstart %q", s.Kind)
		}
		if !reRelayName.MatchString(s.Name) || seen[s.Kind+":"+s.Name] {
			return invalid("%s %q (Name ungültig oder doppelt)", s.Kind, s.Name)
		}
		seen[s.Kind+":"+s.Name] = true
		if len(s.Listen) == 0 || len(s.Listen) > 10 {
			return invalid("%s: ein bis zehn Adressen zum Lauschen", s.Name)
		}
		for _, l := range s.Listen {
			if !addrs[l.Addr] && !validHostOrIP(l.Addr) {
				return invalid("%s: Adresse %q", s.Name, l.Addr)
			}
			if !addrs[l.Addr] {
				return invalid("%s: %q ist keine Adresse dieses Hosts", s.Name, l.Addr)
			}
			if !reRelayPort.MatchString(l.Port) {
				return invalid("%s: Port %q", s.Name, l.Port)
			}
			for _, p := range strings.Split(l.Port, ":") {
				if n := atoiSafe(p); n < 1 || n > 65535 {
					return invalid("%s: Port %s außerhalb 1–65535", s.Name, p)
				}
			}
			if l.Proto != "" && l.Proto != "tcp" && l.Proto != "udp" || (s.Kind == "relay" && l.Proto != "") || (s.Kind == "redirect" && l.TLS) {
				return invalid("%s: Protokoll oder TLS nicht passend zur Art %s", s.Name, s.Kind)
			}
			if s.Kind == "relay" && strings.Contains(l.Port, ":") {
				return invalid("%s: Ein Relay lauscht auf genau einem Port", s.Name)
			}
		}
		if s.Kind == "relay" {
			if s.Protocol != "" && !protos[s.Protocol] {
				return invalid("%s: Protokoll %q gibt es nicht", s.Name, s.Protocol)
			}
			if s.Sticky {
				return invalid("%s: sticky-address gibt es nur bei redirect", s.Name)
			}
		}
		if s.Table != "" {
			if !tables[s.Table] {
				return invalid("%s: Tabelle <%s> gibt es nicht", s.Name, s.Table)
			}
			if s.Port < 0 || s.Port > 65535 {
				return invalid("%s: Zielport %d", s.Name, s.Port)
			}
			modes := map[string][]string{"redirect": {"", "roundrobin", "random", "source-hash", "least-states"}, "relay": {"", "roundrobin", "random", "source-hash", "hash", "loadbalance"}}
			if !contains(modes[s.Kind], s.Mode) {
				return invalid("%s: Verteilverfahren %q gibt es bei %s nicht", s.Name, s.Mode, s.Kind)
			}
			switch s.Check.Kind {
			case "", "icmp", "tcp", "tls":
			case "http", "https":
				if !reRelayPath.MatchString(s.Check.Path) || s.Check.Code < 100 || s.Check.Code > 599 {
					return invalid("%s: Prüfung %s braucht einen Pfad (/health) und einen Statuscode 100–599", s.Name, s.Check.Kind)
				}
			default:
				return invalid("%s: Prüfart %q", s.Name, s.Check.Kind)
			}
		} else if s.Kind == "redirect" {
			return invalid("%s: Eine Weiterleitung braucht eine Zieltabelle", s.Name)
		}
		if s.Timeout < 0 || s.Timeout > 86400 {
			return invalid("%s: session timeout %d (0–86400)", s.Name, s.Timeout)
		}
	}
	return nil
}

// SaveRelayd schreibt relayd.conf neu: Formularteile aus der Eingabe, Rest wörtlich.
func (h *Host) SaveRelayd(ctx context.Context, c *RelaydConf, activate, preview bool) (*ConfResult, error) {
	cur, err := h.Relayd(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.validateRelayd(c, cur); err != nil {
		return nil, err
	}
	src, _ := h.readFile(relaydPath)
	top := parseRelaydTop(src)
	byID := map[int]relaydTop{}
	for i, it := range top {
		byID[i+1] = it
	}
	var out []string
	add := func(lines ...string) { out = append(out, lines...) }
	if c.Interval > 0 {
		add(fmt.Sprintf("interval %d", c.Interval))
	}
	if c.Timeout > 0 {
		add(fmt.Sprintf("timeout %d", c.Timeout))
	}
	if c.Prefork > 0 {
		add(fmt.Sprintf("prefork %d", c.Prefork))
	}
	if c.LogState {
		add("log state changes")
	}
	if c.LogConn {
		add("log connection")
	}
	for _, it := range top { // übrige globale Zeilen und freistehende Kommentare
		if it.kind != "global" {
			continue
		}
		for _, l := range append(append([]string{}, it.leading...), it.lines...) {
			t := strings.TrimSpace(l)
			switch {
			case strings.HasPrefix(t, "interval "), strings.HasPrefix(t, "timeout "), strings.HasPrefix(t, "prefork "), t == "log connection", t == "log state changes":
			default:
				add(l)
			}
		}
	}
	if len(out) > 0 {
		add("")
	}
	for _, t := range c.Tables {
		if t.Raw != "" {
			add(t.Raw, "")
			continue
		}
		add(t.lines()...)
		add("")
	}
	for _, it := range top { // vorhandene Protokolle unverändert
		if it.kind == "protocol" {
			add(it.leading...)
			add(it.lines...)
			add("")
		}
	}
	for _, p := range c.Protocols {
		add(protocolPreset(p)...)
		add("")
	}
	for i := range c.Services {
		s := &c.Services[i]
		if s.ID > 0 {
			o, ok := byID[s.ID]
			if !ok || (o.kind != "redirect" && o.kind != "relay") {
				return nil, invalid("Dienst %d gibt es nicht mehr – bitte neu laden", s.ID)
			}
			old := parseRelaydService(o, s.ID)
			if old.Raw != "" {
				add(old.Raw, "")
				continue
			}
			s.kept = old.kept
			add(o.leading...)
		}
		add(s.lines()...)
		add("")
	}
	content := strings.TrimRight(strings.Join(out, "\n"), "\n") + "\n"
	res := &ConfResult{Warnings: []string{}}
	hasRedirect, hasTLS := false, false
	for _, s := range c.Services {
		hasRedirect = hasRedirect || s.Kind == "redirect"
		for _, l := range s.Listen {
			hasTLS = hasTLS || l.TLS
		}
	}
	if hasRedirect && !cur.PFAnchor {
		res.Warnings = append(res.Warnings, `Weiterleitungen (redirect) brauchen in /etc/pf.conf die Zeile  anchor "relayd/*"  – ohne sie greifen sie nicht.`)
	}
	if hasTLS {
		res.Warnings = append(res.Warnings, "Relays mit TLS brauchen ein Schlüsselpaar: im Protokoll (tls { keypair … }) oder /etc/ssl/<Adresse>.crt samt Schlüssel.")
	}
	if len(c.Services) > 0 {
		pf, _ := h.readFile(pfConfPath)
		for _, s := range c.Services {
			for _, l := range s.Listen {
				if p := strings.SplitN(l.Port, ":", 2)[0]; !strings.Contains(pf, p) && !s.Disable {
					res.Warnings = append(res.Warnings, fmt.Sprintf("%s: Port %s kommt in /etc/pf.conf nicht vor – der Paketfilter blockiert ihn vermutlich.", s.Name, l.Port))
				}
			}
		}
	}
	if err := h.putConf(ctx, relaydPath, content, preview, res); err != nil {
		return nil, err
	}
	if preview {
		return res, nil
	}
	enabled, running := h.rcActive(ctx, "relayd")
	switch {
	case running:
		err = h.run(ctx, "rcctl", "reload", "relayd")
		res.Applied = "relayd neu geladen."
	case activate && !enabled:
		if err = h.run(ctx, "rcctl", "enable", "relayd"); err == nil {
			err = h.run(ctx, "rcctl", "start", "relayd")
		}
		res.Applied = "relayd aktiviert und gestartet."
	case activate:
		err = h.run(ctx, "rcctl", "start", "relayd")
		res.Applied = "relayd gestartet."
	default:
		res.Applied = "Gespeichert; relayd läuft nicht und wurde nicht gestartet."
	}
	if err != nil {
		return nil, fmt.Errorf("Datei gespeichert, aber relayd ließ sich nicht (neu) laden: %w", err)
	}
	return res, nil
}
