package obsd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Netzwerk-Interfaces (hostname.if(5)) als Formular. Änderungen gelten auf Probe: Ohne
// Bestätigung binnen 60 Sekunden stellt maar den alten Stand wieder her – so sperrt man
// sich nicht aus, wenn man das Interface ändert, über das man gerade verbunden ist.

const ifPendingPath = "/var/maar/if-pending.json"

var ifConfirmWait = 60 * time.Second

type IfAddr struct {
	Addr   string `json:"addr"`
	Prefix int    `json:"prefix"`
}

type IfConf struct {
	Name       string   `json:"name"`
	Kind       string   `json:"kind"`   // ethernet, vlan
	V4Mode     string   `json:"v4Mode"` // none, dhcp, static
	V4         []IfAddr `json:"v4"`     // erste = Hauptadresse, weitere = Aliase
	Gateway    string   `json:"gateway"`
	V6Mode     string   `json:"v6Mode"` // none, auto, static
	V6         []IfAddr `json:"v6"`
	Descr      string   `json:"description"`
	MTU        int      `json:"mtu"` // 0 = Standard
	Down       bool     `json:"down"`
	VlanParent string   `json:"vlanParent"`
	VlanID     int      `json:"vlanId"`
}

type IfPendingInfo struct {
	Name     string `json:"name"`
	Deadline int64  `json:"deadline"`
	Seconds  int    `json:"seconds"`
}

type IfView struct {
	Conf      IfConf         `json:"conf"`
	Exists    bool           `json:"exists"` // es gibt schon eine hostname-Datei
	Preserved []string       `json:"preserved"`
	Managed   string         `json:"managedBy,omitempty"` // „VM-Netze“, wenn maar das Interface verwaltet
	Egress    bool           `json:"egress"`
	Parents   []string       `json:"parents"`
	Current   []string       `json:"current"` // laufende Adressen laut ifconfig
	Link      string         `json:"link"`
	Pending   *IfPendingInfo `json:"pending,omitempty"`
	Editable  bool           `json:"editable"`
	Reason    string         `json:"reason,omitempty"`
}

var (
	reIfName   = regexp.MustCompile(`^[a-z]{1,12}[0-9]{1,4}$`)
	reIfDescr  = regexp.MustCompile(`^[^"\\\x00-\x1f]{0,60}$`)
	reIfHostIf = regexp.MustCompile(`^(dhcp|inet autoconf|inet6 autoconf)$`)
)

func maskToPrefix(m string) int {
	if strings.HasPrefix(m, "0x") {
		v, _ := strconv.ParseUint(m[2:], 16, 32)
		return popcount(uint32(v))
	}
	if a, err := netip.ParseAddr(m); err == nil && a.Is4() {
		b := a.As4()
		return popcount(uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3]))
	}
	return 24
}

func parseIfFile(name, src string) (IfConf, []string) {
	c := IfConf{Name: name, Kind: "ethernet", V4Mode: "none", V6Mode: "none", V4: []IfAddr{}, V6: []IfAddr{}}
	if strings.HasPrefix(name, "vlan") {
		c.Kind = "vlan"
	}
	var keep []string
	for _, l := range splitFileLines(src) {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "#") {
			if t != "" {
				keep = append(keep, l)
			}
			continue
		}
		f := strings.Fields(t)
		switch {
		case t == "dhcp" || t == "inet autoconf":
			c.V4Mode = "dhcp"
		case t == "inet6 autoconf":
			c.V6Mode = "auto"
		case f[0] == "inet" && len(f) >= 2 && f[1] != "alias" && f[1] != "autoconf":
			a := IfAddr{Addr: f[1], Prefix: 24}
			if i := strings.Index(f[1], "/"); i > 0 {
				a.Addr, a.Prefix = f[1][:i], atoiSafe(f[1][i+1:])
			} else if len(f) >= 3 {
				a.Prefix = maskToPrefix(f[2])
			}
			if len(f) > 3 && !(len(f) == 4 && f[3] == "NONE") && !strings.Contains(strings.Join(f[3:], " "), "broadcast") || len(f) > 5 {
				keep = append(keep, l)
				continue
			}
			c.V4Mode = "static"
			c.V4 = append(c.V4, a)
		case f[0] == "inet" && len(f) >= 3 && f[1] == "alias":
			a := IfAddr{Addr: f[2], Prefix: 32}
			if i := strings.Index(f[2], "/"); i > 0 {
				a.Addr, a.Prefix = f[2][:i], atoiSafe(f[2][i+1:])
			} else if len(f) >= 4 {
				a.Prefix = maskToPrefix(f[3])
			}
			c.V4 = append(c.V4, a)
			if c.V4Mode == "none" {
				c.V4Mode = "static"
			}
		case f[0] == "inet6" && len(f) >= 2 && f[1] != "autoconf" && f[1] != "alias" && !strings.HasPrefix(f[1], "-"):
			a := IfAddr{Addr: f[1], Prefix: 64}
			if len(f) >= 3 {
				a.Prefix = atoiSafe(f[2])
			}
			c.V6Mode = "static"
			c.V6 = append(c.V6, a)
		case f[0] == "description":
			c.Descr = strings.Trim(strings.TrimSpace(strings.TrimPrefix(t, "description")), `"`)
		case f[0] == "mtu" && len(f) == 2:
			c.MTU = atoiSafe(f[1])
		case t == "up":
			c.Down = false
		case t == "down":
			c.Down = true
		case f[0] == "parent" && len(f) >= 4 && f[2] == "vnetid":
			c.VlanParent, c.VlanID = f[1], atoiSafe(f[3])
		default:
			keep = append(keep, l)
		}
	}
	return c, keep
}

func (h *Host) ifManaged(name string) string {
	cfg, err := h.loadNetConfig()
	if err != nil {
		return ""
	}
	for _, p := range cfg.PortGroups {
		if p.Managed && (p.Bridge == name || p.Vport == name || p.vlanIf() == name) {
			return "VM-Netze (Portgruppe " + p.Name + ")"
		}
	}
	return ""
}

func (h *Host) Interface(ctx context.Context, name string) (*IfView, error) {
	if !reIfName.MatchString(name) {
		return nil, invalid("Interface %q", name)
	}
	net, err := h.Network(ctx)
	if err != nil {
		return nil, err
	}
	var cur *Iface
	for _, i := range net.Interfaces {
		if i.Name == name {
			cur = i
		}
	}
	v := &IfView{Preserved: []string{}, Parents: []string{}, Current: []string{}}
	src, err := h.readFile("/etc/hostname." + name)
	v.Exists = err == nil
	v.Conf, v.Preserved = parseIfFile(name, src)
	if v.Preserved == nil {
		v.Preserved = []string{}
	}
	v.Managed = h.ifManaged(name)
	for _, r := range net.Routes {
		if r.Dest == "default" && r.Family == "inet" && r.Iface == name {
			v.Egress = true
		}
	}
	if cur != nil {
		v.Link = cur.Status
		for _, a := range cur.Addrs {
			if !strings.HasPrefix(a.Addr, "fe80") {
				v.Current = append(v.Current, a.Addr)
			}
		}
		for _, g := range cur.Groups {
			if g == "egress" {
				v.Egress = true
			}
		}
	}
	for _, i := range net.Interfaces {
		if !isVirtual(i.Name) {
			v.Parents = append(v.Parents, i.Name)
		}
	}
	v.Editable = true
	switch {
	case v.Managed != "":
		v.Editable, v.Reason = false, "Dieses Interface erzeugt und verwaltet "+v.Managed+". Änderungen bitte dort vornehmen."
	case !isVirtual(name) || v.Conf.Kind == "vlan":
	default:
		v.Editable, v.Reason = false, "Für Interfaces der Art "+strings.TrimRight(name, "0123456789")+" gibt es kein Formular; sie bleiben im Text-Editor bearbeitbar."
	}
	if cur == nil && !v.Exists && v.Conf.Kind != "vlan" {
		v.Editable, v.Reason = false, "Das Interface "+name+" gibt es auf diesem Host nicht."
	}
	h.ifMu.Lock()
	if p := h.ifPending; p != nil {
		v.Pending = &IfPendingInfo{Name: p.name, Deadline: p.deadline.Unix(), Seconds: int(time.Until(p.deadline).Seconds())}
	}
	h.ifMu.Unlock()
	if gw, err := h.readFile("/etc/mygate"); err == nil && v.Egress {
		v.Conf.Gateway = strings.TrimSpace(strings.SplitN(gw, "\n", 2)[0])
	}
	sort.Strings(v.Parents)
	return v, nil
}

func ipFamily(a string, want6 bool) (netip.Addr, error) {
	ip, err := netip.ParseAddr(a)
	if err != nil || ip.Is6() != want6 {
		return ip, fmt.Errorf("ungültig")
	}
	return ip, nil
}

func (c *IfConf) validate(cur *IfView, others []string) error {
	if !reIfName.MatchString(c.Name) {
		return invalid("Interface %q", c.Name)
	}
	if !reIfDescr.MatchString(c.Descr) {
		return invalid("Beschreibung: höchstens 60 Zeichen, ohne Anführungszeichen")
	}
	if c.MTU != 0 && (c.MTU < 576 || c.MTU > 16000) {
		return invalid("MTU %d (576–16000, 0 = Standard)", c.MTU)
	}
	if c.V4Mode == "" {
		c.V4Mode = "none"
	}
	if c.V6Mode == "" {
		c.V6Mode = "none"
	}
	switch c.V4Mode {
	case "none", "dhcp":
		c.V4 = nil
	case "static":
		if len(c.V4) == 0 || len(c.V4) > 16 {
			return invalid("IPv4 statisch: ein bis 16 Adressen")
		}
	default:
		return invalid("IPv4-Modus %q", c.V4Mode)
	}
	switch c.V6Mode {
	case "none", "auto":
		c.V6 = nil
	case "static":
		if len(c.V6) == 0 || len(c.V6) > 16 {
			return invalid("IPv6 statisch: ein bis 16 Adressen")
		}
	default:
		return invalid("IPv6-Modus %q", c.V6Mode)
	}
	seen := map[string]bool{}
	for i, a := range c.V4 {
		ip, err := ipFamily(a.Addr, false)
		if err != nil || a.Prefix < 8 || a.Prefix > 32 {
			return invalid("IPv4-Adresse %q/%d", a.Addr, a.Prefix)
		}
		if ip.IsLoopback() || ip.IsMulticast() || ip.IsUnspecified() {
			return invalid("%s ist als Interface-Adresse nicht zulässig", a.Addr)
		}
		pf := netip.PrefixFrom(ip, a.Prefix)
		if a.Prefix < 31 && (ip == pf.Masked().Addr() || ip == broadcast(pf.Masked())) {
			return invalid("%s ist Netz- oder Broadcast-Adresse von %s", a.Addr, pf.Masked())
		}
		if seen[a.Addr] {
			return invalid("%s ist doppelt", a.Addr)
		}
		seen[a.Addr] = true
		_ = i
	}
	for _, a := range c.V6 {
		if _, err := ipFamily(a.Addr, true); err != nil || a.Prefix < 8 || a.Prefix > 128 {
			return invalid("IPv6-Adresse %q/%d", a.Addr, a.Prefix)
		}
		if seen[a.Addr] {
			return invalid("%s ist doppelt", a.Addr)
		}
		seen[a.Addr] = true
	}
	if c.Gateway != "" {
		if c.V4Mode != "static" {
			return invalid("Ein Standard-Gateway gibt es nur bei statischer IPv4-Adresse (bei DHCP kommt es vom Server)")
		}
		gw, err := ipFamily(c.Gateway, false)
		if err != nil {
			return invalid("Gateway %q", c.Gateway)
		}
		ok := false
		for _, a := range c.V4 {
			if ip, e := netip.ParseAddr(a.Addr); e == nil && netip.PrefixFrom(ip, a.Prefix).Masked().Contains(gw) && gw != ip {
				ok = true
			}
		}
		if !ok {
			return invalid("Das Gateway %s liegt in keinem der Netze dieses Interfaces", c.Gateway)
		}
	}
	// Überschneidung mit anderen Interfaces
	if c.V4Mode == "static" {
		for _, a := range c.V4 {
			ip, _ := netip.ParseAddr(a.Addr)
			pf := netip.PrefixFrom(ip, a.Prefix).Masked()
			for _, o := range others {
				if op, err := netip.ParsePrefix(o); err == nil && op.Masked().Overlaps(pf) && op.Addr() == ip {
					return invalid("%s ist schon auf einem anderen Interface vergeben", a.Addr)
				}
			}
		}
	}
	if c.Kind == "vlan" {
		if !strings.HasPrefix(c.Name, "vlan") {
			return invalid("VLAN-Interfaces heißen vlanN")
		}
		if c.VlanID < 1 || c.VlanID > 4094 {
			return invalid("VLAN-ID %d (1–4094)", c.VlanID)
		}
		if !contains(cur.Parents, c.VlanParent) {
			return invalid("Das Interface %q für das VLAN gibt es nicht oder ist nicht physisch", c.VlanParent)
		}
	} else if c.VlanParent != "" || c.VlanID != 0 {
		return invalid("VLAN-Angaben nur bei VLAN-Interfaces")
	}
	return nil
}

func (c *IfConf) render(preserved []string) string {
	var out []string
	if c.Kind == "vlan" {
		out = append(out, fmt.Sprintf("parent %s vnetid %d", c.VlanParent, c.VlanID))
	}
	switch c.V4Mode {
	case "dhcp":
		out = append(out, "dhcp")
	case "static":
		for i, a := range c.V4 {
			if i == 0 {
				out = append(out, fmt.Sprintf("inet %s/%d", a.Addr, a.Prefix))
			} else {
				out = append(out, fmt.Sprintf("inet alias %s/%d", a.Addr, a.Prefix))
			}
		}
	}
	switch c.V6Mode {
	case "auto":
		out = append(out, "inet6 autoconf")
	case "static":
		for i, a := range c.V6 {
			if i == 0 {
				out = append(out, fmt.Sprintf("inet6 %s %d", a.Addr, a.Prefix))
			} else {
				out = append(out, fmt.Sprintf("inet6 alias %s %d", a.Addr, a.Prefix))
			}
		}
	}
	if c.Descr != "" {
		out = append(out, fmt.Sprintf("description %q", c.Descr))
	}
	if c.MTU > 0 {
		out = append(out, fmt.Sprintf("mtu %d", c.MTU))
	}
	out = append(out, preserved...)
	if c.Down {
		out = append(out, "down")
	} else {
		out = append(out, "up")
	}
	return strings.Join(out, "\n") + "\n"
}

type ifPendingState struct {
	name       string
	oldFile    string
	hadFile    bool
	oldGate    string
	hadGate    bool
	deadline   time.Time
	timer      *time.Timer
	newGateSet bool
}

func (h *Host) otherPrefixes(ctx context.Context, name string) []string {
	var out []string
	if n, err := h.Network(ctx); err == nil {
		for _, i := range n.Interfaces {
			if i.Name == name {
				continue
			}
			for _, a := range i.Addrs {
				if a.Family == "inet" {
					out = append(out, a.Addr+"/32")
				}
			}
		}
	}
	return out
}

// SaveInterface schreibt hostname.<if> (und ggf. mygate), wendet es mit netstart an und startet die Bestätigungsfrist.
func (h *Host) SaveInterface(ctx context.Context, c *IfConf, preview bool) (*ConfResult, error) {
	cur, err := h.Interface(ctx, c.Name)
	if err != nil {
		return nil, err
	}
	if !cur.Editable && !(strings.HasPrefix(c.Name, "vlan") && cur.Managed == "") {
		return nil, invalid("%s", cur.Reason)
	}
	if c.Kind != "vlan" && strings.HasPrefix(c.Name, "vlan") {
		c.Kind = "vlan"
	}
	if err := c.validate(cur, h.otherPrefixes(ctx, c.Name)); err != nil {
		return nil, err
	}
	content := c.render(cur.Preserved)
	res := &ConfResult{Warnings: []string{}}
	if cur.Egress {
		res.Warnings = append(res.Warnings, fmt.Sprintf("%s ist die Uplink-Schnittstelle (Standardroute). Änderungen können die Verbindung zu dieser Oberfläche kappen – bestätige innerhalb von %d Sekunden, sonst nimmt maar sie zurück.", c.Name, ifSeconds()))
	}
	if c.Down {
		res.Warnings = append(res.Warnings, "Das Interface wird abgeschaltet (down).")
	}
	if cur.Egress && c.V4Mode == "static" && c.Gateway == "" {
		res.Warnings = append(res.Warnings, "Ohne Standard-Gateway erreicht der Host Netze außerhalb seines Subnetzes nicht.")
	}
	if preview {
		res.Content = content
		return res, nil
	}
	h.ifMu.Lock()
	defer h.ifMu.Unlock()
	if h.ifPending != nil {
		return nil, invalid("Eine Interface-Änderung wartet noch auf Bestätigung – bestätigen oder zurücknehmen")
	}
	oldFile, errF := h.readFile("/etc/hostname." + c.Name)
	oldGate, errG := h.readFile("/etc/mygate")
	if errF == nil && oldFile == content && (c.Gateway == "" || strings.TrimSpace(oldGate) == c.Gateway) {
		res.Output = "Keine Änderung."
		return res, nil
	}
	if _, err := h.WriteConfig(ctx, "/etc/hostname."+c.Name, content, 0); err != nil {
		return nil, err
	}
	p := &ifPendingState{name: c.Name, oldFile: oldFile, hadFile: errF == nil, oldGate: oldGate, hadGate: errG == nil, deadline: time.Now().Add(ifConfirmWait)}
	if c.Gateway != "" && strings.TrimSpace(oldGate) != c.Gateway {
		if _, err := h.WriteConfig(ctx, "/etc/mygate", c.Gateway+"\n", 0); err != nil {
			h.restoreIf(ctx, p)
			return nil, err
		}
		p.newGateSet = true
	}
	var out strings.Builder
	r, err := h.R.Run(ctx, nil, "sh", "/etc/netstart", c.Name)
	out.Write(r.Stdout)
	out.Write(r.Stderr)
	if err != nil {
		h.restoreIf(ctx, p)
		return nil, fmt.Errorf("netstart %s schlug fehl (alter Stand wiederhergestellt): %s", c.Name, strings.TrimSpace(out.String()))
	}
	if p.newGateSet {
		h.R.Run(ctx, nil, "route", "-qn", "flush", "-inet", "default")
		h.R.Run(ctx, nil, "route", "add", "default", c.Gateway)
	}
	p.timer = time.AfterFunc(ifConfirmWait, func() { h.IfRevert(context.Background(), true) })
	h.ifPending = p
	b, _ := json.Marshal(map[string]any{"name": p.name, "oldFile": p.oldFile, "hadFile": p.hadFile, "oldGate": p.oldGate, "hadGate": p.hadGate, "gateSet": p.newGateSet})
	h.writeAtomic(ifPendingPath, b, 0o600)
	res.Output = strings.TrimSpace(out.String())
	res.Pending = ifSeconds()
	res.Applied = fmt.Sprintf("%s ist neu konfiguriert. Bestätige innerhalb von %d Sekunden, sonst wird der alte Stand wiederhergestellt.", c.Name, res.Pending)
	return res, nil
}

func ifSeconds() int {
	if s := int(ifConfirmWait.Seconds() + 0.999); s > 0 {
		return s
	}
	return 1
}

func (h *Host) restoreIf(ctx context.Context, p *ifPendingState) {
	path := h.P("/etc/hostname." + p.name)
	if p.hadFile {
		h.writeAtomic("/etc/hostname."+p.name, []byte(p.oldFile), 0o640)
	} else {
		os.Remove(path)
		if strings.HasPrefix(p.name, "vlan") {
			h.R.Run(ctx, nil, "ifconfig", p.name, "destroy")
		}
	}
	if p.newGateSet {
		if p.hadGate {
			h.writeAtomic("/etc/mygate", []byte(p.oldGate), 0o644)
		} else {
			os.Remove(h.P("/etc/mygate"))
		}
	}
	if p.hadFile {
		h.R.Run(ctx, nil, "sh", "/etc/netstart", p.name)
	}
	if p.newGateSet && p.hadGate {
		h.R.Run(ctx, nil, "route", "-qn", "flush", "-inet", "default")
		h.R.Run(ctx, nil, "route", "add", "default", strings.TrimSpace(p.oldGate))
	}
}

func (h *Host) IfConfirm(ctx context.Context) error {
	h.ifMu.Lock()
	defer h.ifMu.Unlock()
	if h.ifPending == nil {
		return invalid("Es wartet keine Interface-Änderung auf Bestätigung (die Frist ist abgelaufen?)")
	}
	h.ifPending.timer.Stop()
	os.Remove(h.P(ifPendingPath))
	h.ifPending = nil
	return nil
}

func (h *Host) IfRevert(ctx context.Context, auto bool) error {
	h.ifMu.Lock()
	defer h.ifMu.Unlock()
	p := h.ifPending
	if p == nil {
		return invalid("Es wartet keine Interface-Änderung auf Bestätigung")
	}
	p.timer.Stop()
	h.restoreIf(ctx, p)
	os.Remove(h.P(ifPendingPath))
	h.ifPending = nil
	if auto {
		h.Emit("if_reverted", p.name, "Die Änderung an "+p.name+" wurde nicht bestätigt und automatisch zurückgenommen.")
	}
	return nil
}

// RecoverIf nimmt eine unbestätigte Interface-Änderung zurück, wenn maar währenddessen beendet wurde.
func (h *Host) RecoverIf(ctx context.Context) {
	b, err := os.ReadFile(h.P(ifPendingPath))
	if err != nil {
		return
	}
	var p struct {
		Name    string `json:"name"`
		OldFile string `json:"oldFile"`
		HadFile bool   `json:"hadFile"`
		OldGate string `json:"oldGate"`
		HadGate bool   `json:"hadGate"`
		GateSet bool   `json:"gateSet"`
	}
	if json.Unmarshal(b, &p) != nil || !reIfName.MatchString(p.Name) {
		os.Remove(h.P(ifPendingPath))
		return
	}
	h.restoreIf(ctx, &ifPendingState{name: p.Name, oldFile: p.OldFile, hadFile: p.HadFile, oldGate: p.OldGate, hadGate: p.HadGate, newGateSet: p.GateSet})
	os.Remove(h.P(ifPendingPath))
}
