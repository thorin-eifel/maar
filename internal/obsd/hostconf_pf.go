package obsd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Host-Regeln für den Paketfilter als Formular. maar verwaltet einen Block in
// /etc/pf.conf (vor dem Anker „maar“ für die VM-Netze); alles andere bleibt.
// Neue Regeln gelten zunächst nur auf Probe: Ohne Bestätigung binnen 60 Sekunden
// stellt maar den alten Regelsatz wieder her – so sperrt man sich nicht aus.

const (
	pfRulesPath   = "/var/maar/pf-rules.json"
	pfPendingPath = "/var/maar/pf-pending.json"
	pfConfPath    = "/etc/pf.conf"
	pfBegin       = "# >>> maar: Host-Regeln (automatisch erzeugt, nicht von Hand ändern)"
	pfEnd         = "# <<< maar"
)

// pfConfirmWait ist die Frist zum Bestätigen neuer Regeln (in Tests verkürzt).
var pfConfirmWait = 60 * time.Second

type PFRule struct {
	Disabled bool   `json:"disabled"`
	Action   string `json:"action"` // pass, block
	Dir      string `json:"dir"`    // in, out, "" = beide
	Iface    string `json:"iface"`  // "" = alle
	Proto    string `json:"proto"`  // "", tcp, udp, icmp, icmp6
	From     string `json:"from"`   // "" = beliebig, self, Adresse/Netz, <tabelle>
	To       string `json:"to"`
	Ports    string `json:"ports"` // Zielports
	Quick    bool   `json:"quick"`
	Log      bool   `json:"log"`
	Descr    string `json:"description"`
}

type PFTable struct {
	Name  string   `json:"name"`
	Addrs []string `json:"addrs"`
}

type PFManaged struct {
	Tables []PFTable `json:"tables"`
	Rules  []PFRule  `json:"rules"`
}

type PFService struct {
	Name  string `json:"name"`
	Port  int    `json:"port"`
	Proto string `json:"proto"`
}

type PFGuard struct {
	Port  int    `json:"port"`
	Label string `json:"label"`
}

type PFPendingInfo struct {
	Deadline int64 `json:"deadline"` // Unix-Sekunden
	Seconds  int   `json:"seconds"`
}

type PFFormView struct {
	Conf       PFManaged      `json:"conf"`
	Interfaces []string       `json:"interfaces"`
	Tables     []string       `json:"tables"` // alle bekannten, ohne die eigenen
	Services   []PFService    `json:"services"`
	Existing   []string       `json:"existing"` // Regeln der pf.conf außerhalb des Blocks
	Pending    *PFPendingInfo `json:"pending,omitempty"`
	Enabled    bool           `json:"enabled"`
}

var pfServices = []PFService{{"ssh", 22, "tcp"}, {"smtp", 25, "tcp"}, {"domain", 53, "tcp/udp"}, {"bootps", 67, "udp"}, {"http", 80, "tcp"}, {"ntp", 123, "udp"},
	{"imap", 143, "tcp"}, {"snmp", 161, "udp"}, {"ldap", 389, "tcp"}, {"https", 443, "tcp"}, {"smb", 445, "tcp"}, {"submission", 587, "tcp"}, {"ldaps", 636, "tcp"},
	{"syslog", 514, "udp"}, {"imaps", 993, "tcp"}, {"openvpn", 1194, "udp"}, {"nfs", 2049, "tcp/udp"}, {"mysql", 3306, "tcp"}, {"rdp", 3389, "tcp"},
	{"postgresql", 5432, "tcp"}, {"wireguard", 51820, "udp"}}

var (
	rePFTableDef = regexp.MustCompile(`(?m)^\s*table\s+<([A-Za-z0-9_.-]+)>`)
	rePFPorts    = regexp.MustCompile(`^[0-9]{1,5}(:[0-9]{1,5})?(,[0-9]{1,5}(:[0-9]{1,5})?)*$`)
	rePFTable    = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,30}$`)
	rePFAnchor   = regexp.MustCompile(`^\s*(#\s*VM-Netze von maar|anchor\s+"maar")`)
)

func (h *Host) loadPFRules() PFManaged {
	c := PFManaged{Tables: []PFTable{}, Rules: []PFRule{}}
	if b, err := os.ReadFile(h.P(pfRulesPath)); err == nil {
		json.Unmarshal(b, &c)
	}
	if c.Tables == nil {
		c.Tables = []PFTable{}
	}
	if c.Rules == nil {
		c.Rules = []PFRule{}
	}
	return c
}

func (h *Host) writeAtomic(path string, data []byte, mode os.FileMode) error {
	p := h.P(path)
	os.MkdirAll(filepath.Dir(p), 0o700)
	if err := os.WriteFile(p+".tmp", data, mode); err != nil {
		return err
	}
	return os.Rename(p+".tmp", p)
}

// stripPFBlock liefert pf.conf ohne den verwalteten Block.
func stripPFBlock(src string) string {
	if a := strings.Index(src, pfBegin); a >= 0 {
		if b := strings.Index(src[a:], pfEnd); b >= 0 {
			return src[:a] + src[lineEnd(src, a+b):]
		}
	}
	return src
}

func (h *Host) PFForm(ctx context.Context) (*PFFormView, error) {
	src, err := h.readFile(pfConfPath)
	if err != nil {
		return nil, err
	}
	v := &PFFormView{Conf: h.loadPFRules(), Services: pfServices, Interfaces: []string{}, Tables: []string{}, Existing: []string{}}
	own := map[string]bool{}
	for _, t := range v.Conf.Tables {
		own[t.Name] = true
	}
	seen := map[string]bool{}
	for _, m := range rePFTableDef.FindAllStringSubmatch(stripPFBlock(src), -1) {
		seen[m[1]] = true
	}
	if st, err := h.PF(ctx); err == nil {
		v.Enabled = st.Enabled
		for _, t := range st.Tables {
			seen[strings.TrimSpace(t)] = true
		}
	}
	for t := range seen {
		if !own[t] && t != "" {
			v.Tables = append(v.Tables, t)
		}
	}
	sort.Strings(v.Tables)
	ifs := map[string]bool{"egress": true}
	if out, err := h.out(ctx, "ifconfig", "-A"); err == nil {
		for _, i := range ParseIfconfig(out) {
			if !strings.HasPrefix(i.Name, "pflog") {
				ifs[i.Name] = true
			}
			for _, g := range i.Groups {
				if g == "egress" || g == "vport" || g == "veb" || g == "tap" || g == "wg" {
					ifs[g] = g == "egress" || ifs[g]
				}
			}
		}
	}
	for i := range ifs {
		v.Interfaces = append(v.Interfaces, i)
	}
	sort.Strings(v.Interfaces)
	for _, l := range splitFileLines(stripPFBlock(src)) {
		t := strings.TrimSpace(l)
		if t != "" && !strings.HasPrefix(t, "#") && len(v.Existing) < 300 {
			v.Existing = append(v.Existing, t)
		}
	}
	h.pfMu.Lock()
	if p := h.pfPending; p != nil {
		v.Pending = &PFPendingInfo{Deadline: p.deadline.Unix(), Seconds: int(time.Until(p.deadline).Seconds())}
	}
	h.pfMu.Unlock()
	return v, nil
}

func pfAddr(s string, tables map[string]bool) (string, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "" || s == "any":
		return "", nil
	case s == "self":
		return "self", nil
	case strings.HasPrefix(s, "<") && strings.HasSuffix(s, ">"):
		n := s[1 : len(s)-1]
		if !tables[n] {
			return "", invalid("Tabelle <%s> gibt es nicht", n)
		}
		return s, nil
	}
	if _, err := netip.ParsePrefix(s); err == nil {
		return s, nil
	}
	if _, err := netip.ParseAddr(s); err == nil {
		return s, nil
	}
	return "", invalid("Adresse %q (IP, Netz wie 10.0.0.0/24, <Tabelle> oder self)", s)
}

func (c *PFManaged) validate(known map[string]bool, ifs map[string]bool) error {
	if len(c.Rules) > 200 || len(c.Tables) > 20 {
		return invalid("zu viele Regeln oder Tabellen")
	}
	tables := map[string]bool{}
	for k := range known {
		tables[k] = true
	}
	for i := range c.Tables {
		t := &c.Tables[i]
		if !rePFTable.MatchString(t.Name) {
			return invalid("Tabellenname %q (Buchstabe, dann Buchstaben, Ziffern, Unterstrich)", t.Name)
		}
		if known[t.Name] {
			return invalid("Die Tabelle <%s> gibt es schon in pf.conf", t.Name)
		}
		if tables[t.Name] {
			return invalid("Tabelle <%s> ist doppelt", t.Name)
		}
		tables[t.Name] = true
		if len(t.Addrs) > 500 {
			return invalid("Tabelle <%s> hat zu viele Adressen", t.Name)
		}
		for j, a := range t.Addrs {
			a = strings.TrimSpace(a)
			if _, e1 := netip.ParsePrefix(a); e1 != nil {
				if _, e2 := netip.ParseAddr(a); e2 != nil {
					return invalid("Adresse %q in <%s>", a, t.Name)
				}
			}
			t.Addrs[j] = a
		}
	}
	for i := range c.Rules {
		r := &c.Rules[i]
		n := i + 1
		if r.Action != "pass" && r.Action != "block" {
			return invalid("Regel %d: Aktion pass oder block", n)
		}
		if r.Dir != "" && r.Dir != "in" && r.Dir != "out" {
			return invalid("Regel %d: Richtung", n)
		}
		if r.Iface != "" && !ifs[r.Iface] {
			return invalid("Regel %d: Interface %q gibt es nicht", n, r.Iface)
		}
		switch r.Proto {
		case "", "tcp", "udp", "icmp", "icmp6":
		default:
			return invalid("Regel %d: Protokoll %q", n, r.Proto)
		}
		var err error
		if r.From, err = pfAddr(r.From, tables); err != nil {
			return fmt.Errorf("Regel %d: %w", n, err)
		}
		if r.To, err = pfAddr(r.To, tables); err != nil {
			return fmt.Errorf("Regel %d: %w", n, err)
		}
		r.Ports = strings.ReplaceAll(strings.TrimSpace(r.Ports), " ", "")
		if r.Ports != "" {
			if r.Proto != "tcp" && r.Proto != "udp" {
				return invalid("Regel %d: Ports gibt es nur bei TCP oder UDP", n)
			}
			if !rePFPorts.MatchString(r.Ports) {
				return invalid("Regel %d: Ports %q (z. B. 443, 8000:8080 oder 80,443)", n, r.Ports)
			}
			for _, p := range strings.Split(r.Ports, ",") {
				lo, hi, _ := strings.Cut(p, ":")
				a, b := atoiSafe(lo), atoiSafe(hi)
				if hi == "" {
					b = a
				}
				if a < 1 || b > 65535 || a > b {
					return invalid("Regel %d: Port %s außerhalb 1–65535", n, p)
				}
			}
		}
		if strings.ContainsAny(r.Descr, "\n\r") || len(r.Descr) > 80 {
			return invalid("Regel %d: Beschreibung (höchstens 80 Zeichen, einzeilig)", n)
		}
	}
	return nil
}

func pfSet(parts []string) string {
	if len(parts) == 1 {
		return parts[0]
	}
	return "{ " + strings.Join(parts, " ") + " }"
}

func (r PFRule) line() string {
	var b strings.Builder
	b.WriteString(r.Action)
	if r.Dir != "" {
		b.WriteString(" " + r.Dir)
	}
	if r.Log {
		b.WriteString(" log")
	}
	if r.Quick {
		b.WriteString(" quick")
	}
	if r.Iface != "" {
		b.WriteString(" on " + r.Iface)
	}
	if r.Proto != "" {
		b.WriteString(" proto " + r.Proto)
	}
	switch {
	case r.From == "" && r.To == "":
		b.WriteString(" all")
	default:
		if r.From != "" {
			b.WriteString(" from " + r.From)
		}
		if r.To != "" {
			b.WriteString(" to " + r.To)
		}
	}
	if r.Ports != "" {
		b.WriteString(" port " + pfSet(strings.Split(r.Ports, ",")))
	}
	return b.String()
}

func (c *PFManaged) render() string {
	var b strings.Builder
	b.WriteString(pfBegin + "\n")
	for _, t := range c.Tables {
		if len(t.Addrs) == 0 {
			fmt.Fprintf(&b, "table <%s> persist\n", t.Name)
		} else {
			fmt.Fprintf(&b, "table <%s> persist { %s }\n", t.Name, strings.Join(t.Addrs, ", "))
		}
	}
	for _, r := range c.Rules {
		if r.Descr != "" {
			b.WriteString("# " + r.Descr + "\n")
		}
		if r.Disabled {
			b.WriteString("# (aus) " + r.line() + "\n")
		} else {
			b.WriteString(r.line() + "\n")
		}
	}
	b.WriteString(pfEnd + "\n")
	return b.String()
}

// mergePF setzt den Block vor den Anker „maar“ (oder ans Ende) bzw. ersetzt ihn an Ort und Stelle.
func mergePF(src string, c *PFManaged) string {
	empty := len(c.Rules) == 0 && len(c.Tables) == 0
	if a := strings.Index(src, pfBegin); a >= 0 {
		if b := strings.Index(src[a:], pfEnd); b >= 0 {
			e := lineEnd(src, a+b)
			if empty {
				if strings.HasPrefix(src[e:], "\n") { // die beim Einfügen ergänzte Leerzeile
					e++
				}
				return src[:a] + src[e:]
			}
			return src[:a] + c.render() + src[e:]
		}
	}
	if empty {
		return src
	}
	lines := strings.Split(strings.TrimRight(src, "\n"), "\n")
	at := len(lines)
	for i, l := range lines {
		if rePFAnchor.MatchString(l) {
			at = i
			break
		}
	}
	block := strings.Split(strings.TrimRight(c.render(), "\n"), "\n")
	out := append(append(append([]string{}, lines[:at]...), block...), "")
	out = append(out, lines[at:]...)
	return strings.Join(out, "\n") + "\n"
}

type pfPendingState struct {
	old      string
	rules    PFManaged
	deadline time.Time
	timer    *time.Timer
}

// SavePF schreibt den Block, lädt den Regelsatz und startet die Bestätigungsfrist.
func (h *Host) SavePF(ctx context.Context, c *PFManaged, guard []PFGuard, preview bool) (*ConfResult, error) {
	cur, err := h.PFForm(ctx)
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, t := range cur.Tables {
		known[t] = true
	}
	ifs := map[string]bool{}
	for _, i := range cur.Interfaces {
		ifs[i] = true
	}
	if err := c.validate(known, ifs); err != nil {
		return nil, err
	}
	src, err := h.readFile(pfConfPath)
	if err != nil {
		return nil, err
	}
	content := mergePF(src, c)
	res := &ConfResult{Warnings: []string{}}
	for i, r := range c.Rules {
		if r.Disabled || r.Action != "block" || r.Dir == "out" {
			continue
		}
		if r.To != "" && r.To != "self" {
			continue
		}
		for _, g := range guard {
			if (r.Proto == "" || r.Proto == "tcp") && (r.Ports == "" || portsCover(r.Ports, g.Port)) {
				res.Warnings = append(res.Warnings, fmt.Sprintf("Regel %d kann den Zugriff auf %s (Port %d) sperren. Du hast nach dem Anwenden %d Sekunden Zeit zu bestätigen; sonst nimmt maar die Änderung zurück.", i+1, g.Label, g.Port, confirmSeconds()))
			}
		}
	}
	if !cur.Enabled {
		res.Warnings = append(res.Warnings, "Der Paketfilter ist ausgeschaltet – die Regeln werden zwar geladen, aber nicht angewendet.")
	}
	if preview {
		res.Content = content
		return res, nil
	}
	h.pfMu.Lock()
	defer h.pfMu.Unlock()
	if h.pfPending != nil {
		return nil, invalid("Eine Änderung wartet noch auf Bestätigung – bestätigen oder zurücknehmen")
	}
	if content == src {
		res.Output = "Keine Änderung."
		return res, nil
	}
	r, err := h.WriteConfig(ctx, pfConfPath, content, 0)
	if err != nil {
		return nil, err
	}
	res.Output = r.Check
	if out, err := h.R.Run(ctx, nil, "pfctl", "-f", pfConfPath); err != nil {
		h.writeAtomic(pfConfPath, []byte(src), 0o600)
		h.R.Run(ctx, nil, "pfctl", "-f", pfConfPath)
		return nil, fmt.Errorf("pfctl konnte den Regelsatz nicht laden (alter Stand wiederhergestellt): %s", strings.TrimSpace(string(out.Stderr)+string(out.Stdout)))
	}
	p := &pfPendingState{old: src, rules: *c, deadline: time.Now().Add(pfConfirmWait)}
	p.timer = time.AfterFunc(pfConfirmWait, func() { h.PFRevert(context.Background(), true) })
	h.pfPending = p
	b, _ := json.Marshal(map[string]any{"old": src, "deadline": p.deadline.Unix()})
	h.writeAtomic(pfPendingPath, b, 0o600)
	res.Pending = confirmSeconds()
	res.Applied = fmt.Sprintf("Regeln geladen. Bestätige innerhalb von %d Sekunden, sonst werden sie zurückgenommen.", res.Pending)
	return res, nil
}

func portsCover(spec string, port int) bool {
	for _, p := range strings.Split(spec, ",") {
		lo, hi, _ := strings.Cut(p, ":")
		a, b := atoiSafe(lo), atoiSafe(hi)
		if hi == "" {
			b = a
		}
		if port >= a && port <= b {
			return true
		}
	}
	return false
}

// PFConfirm macht die geladenen Regeln dauerhaft.
func (h *Host) PFConfirm(ctx context.Context) error {
	h.pfMu.Lock()
	defer h.pfMu.Unlock()
	p := h.pfPending
	if p == nil {
		return invalid("Es wartet keine Änderung auf Bestätigung (die Frist ist abgelaufen?)")
	}
	p.timer.Stop()
	b, _ := json.MarshalIndent(p.rules, "", "  ")
	if err := h.writeAtomic(pfRulesPath, b, 0o600); err != nil {
		return err
	}
	os.Remove(h.P(pfPendingPath))
	h.pfPending = nil
	return nil
}

// PFRevert stellt den Stand vor der Änderung wieder her. auto: durch Zeitablauf.
func (h *Host) PFRevert(ctx context.Context, auto bool) error {
	h.pfMu.Lock()
	defer h.pfMu.Unlock()
	p := h.pfPending
	if p == nil {
		return invalid("Es wartet keine Änderung auf Bestätigung")
	}
	p.timer.Stop()
	if err := h.writeAtomic(pfConfPath, []byte(p.old), 0o600); err != nil {
		return err
	}
	_, err := h.R.Run(ctx, nil, "pfctl", "-f", pfConfPath)
	os.Remove(h.P(pfPendingPath))
	h.pfPending = nil
	if auto {
		h.Emit("pf_reverted", "pf.conf", "Die Paketfilter-Änderung wurde nicht bestätigt und automatisch zurückgenommen.")
	}
	return err
}

// RecoverPF nimmt eine unbestätigte Änderung zurück, wenn maar währenddessen beendet wurde.
func (h *Host) RecoverPF(ctx context.Context) {
	b, err := os.ReadFile(h.P(pfPendingPath))
	if err != nil {
		return
	}
	var p struct {
		Old string `json:"old"`
	}
	if json.Unmarshal(b, &p) != nil || p.Old == "" {
		os.Remove(h.P(pfPendingPath))
		return
	}
	h.writeAtomic(pfConfPath, []byte(p.Old), 0o600)
	h.R.Run(ctx, nil, "pfctl", "-f", pfConfPath)
	os.Remove(h.P(pfPendingPath))
}

func confirmSeconds() int {
	if s := int(pfConfirmWait.Seconds() + 0.999); s > 0 {
		return s
	}
	return 1
}
