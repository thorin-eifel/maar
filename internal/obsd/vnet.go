package obsd

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// VM-Netze: Portgruppen (veb-Switch mit optionalem Uplink oder VLAN und
// vport-Gateway), feste Adressen der VM-Anschlüsse, daraus abgeleitete
// DHCP- und DNS-Einträge sowie pf-Regeln je VM. Gespeichert wird nur die
// Absicht in /var/maar/network.json; hostname.if-Dateien, der
// Switch-Block in vm.conf, dhcpd.conf, unbound und der pf-Anker werden
// daraus erzeugt.

const (
	netConfPath  = "/var/maar/network.json"
	pfAnchorPath = "/etc/pf.maar.conf"
	unboundPath  = "/var/unbound/etc/maar.conf"
	unboundMain  = "/var/unbound/etc/unbound.conf"
	dhcpdPath    = "/etc/dhcpd.conf"
	pfAnchorName = "maar"
)

type PortGroup struct {
	Name      string `json:"name"` // Switch-Name in vm.conf
	Descr     string `json:"description"`
	Bridge    string `json:"bridge"`  // vebN
	Uplink    string `json:"uplink"`  // physisches Interface, leer = nur intern
	VLAN      int    `json:"vlan"`    // 0 = ungetaggt
	Gateway   string `json:"gateway"` // Adresse des vport mit Präfix, leer = reines L2
	Vport     string `json:"vport"`
	NAT       bool   `json:"nat"`
	DHCP      bool   `json:"dhcp"`
	RangeFrom string `json:"rangeFrom"`
	RangeTo   string `json:"rangeTo"`
	DNS       bool   `json:"dns"`
	Domain    string `json:"domain"`
	Filter    bool   `json:"filter"` // link1: pf auch zwischen VMs am Switch
	Locked    bool   `json:"locked"` // locked lladdr: keine gefälschten Absender-MACs
	Managed   bool   `json:"managed"`
}

// Lease ist eine feste Adresse für einen VM-Anschluss, erkannt an der MAC.
type Lease struct {
	VM       string `json:"vm"`
	LLAddr   string `json:"lladdr"`
	IP       string `json:"ip"`
	Hostname string `json:"hostname"`
}

type FWRule struct {
	Dir    string `json:"dir"`    // in = zur VM, out = von der VM
	Action string `json:"action"` // pass, block
	Proto  string `json:"proto"`  // any, tcp, udp, icmp
	Peer   string `json:"peer"`   // any, Adresse/Netz, <tabelle> oder VM-Name
	Port   string `json:"port"`   // leer, 443, 8000:8080, 80,443
	Log    bool   `json:"log"`
	Descr  string `json:"description"`
}

type VMPolicy struct {
	In  string `json:"in"`  // allow, deny
	Out string `json:"out"` // allow, deny
}

type NetConfig struct {
	PortGroups []*PortGroup         `json:"portGroups"`
	Leases     []*Lease             `json:"leases"`
	Rules      map[string][]*FWRule `json:"rules"`
	Policies   map[string]*VMPolicy `json:"policies"`
}

func (h *Host) loadNetConfig() (*NetConfig, error) {
	c := &NetConfig{PortGroups: []*PortGroup{}, Leases: []*Lease{}, Rules: map[string][]*FWRule{}, Policies: map[string]*VMPolicy{}}
	b, err := os.ReadFile(h.P(netConfPath))
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("network.json: %w", err)
	}
	if c.Rules == nil {
		c.Rules = map[string][]*FWRule{}
	}
	if c.Policies == nil {
		c.Policies = map[string]*VMPolicy{}
	}
	return c, nil
}

func (h *Host) saveNetConfig(c *NetConfig) error {
	b, _ := json.MarshalIndent(c, "", "  ")
	p := h.P(netConfPath)
	os.MkdirAll(filepath.Dir(p), 0o700)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func (c *NetConfig) group(name string) *PortGroup {
	for _, p := range c.PortGroups {
		if p.Name == name {
			return p
		}
	}
	return nil
}

func (c *NetConfig) lease(vm, lladdr string) *Lease {
	for _, l := range c.Leases {
		if l.VM == vm && l.LLAddr == lladdr {
			return l
		}
	}
	return nil
}

func (c *NetConfig) policy(vm string) VMPolicy {
	if p := c.Policies[vm]; p != nil {
		return *p
	}
	return VMPolicy{In: "allow", Out: "allow"}
}

// netState ist eine Momentaufnahme aus Konfiguration, vm.conf und ifconfig.
type netState struct {
	cfg    *NetConfig
	conf   *VMConfFile
	ifaces []*Iface
	gone   []string // in diesem Durchlauf entfernte vports (aus dhcpd_flags streichen)
}

func (h *Host) loadNetState(ctx context.Context) (*netState, error) {
	cfg, err := h.loadNetConfig()
	if err != nil {
		return nil, err
	}
	conf, err := h.loadVMConf()
	if err != nil {
		return nil, err
	}
	out, err := h.out(ctx, "ifconfig", "-A")
	if err != nil {
		return nil, err
	}
	st := &netState{cfg: cfg, conf: conf, ifaces: ParseIfconfig(out)}
	// Switches aus vm.conf, die maar noch nicht kennt, werden aus den
	// vorhandenen hostname.if-Dateien rekonstruiert.
	for _, sw := range conf.Switches {
		if cfg.group(sw.Name) == nil {
			cfg.PortGroups = append(cfg.PortGroups, h.adoptSwitch(sw))
		}
	}
	return st, nil
}

func (st *netState) iface(name string) *Iface {
	for _, i := range st.ifaces {
		if i.Name == name {
			return i
		}
	}
	return nil
}

var (
	reHnAdd   = regexp.MustCompile(`^add\s+([a-z]+[0-9]+)`)
	reHnInet  = regexp.MustCompile(`^inet\s+([0-9.]+)(?:/([0-9]+)|\s+([0-9.]+|0x[0-9a-f]+))?`)
	reHnVLAN  = regexp.MustCompile(`(?:parent\s+([a-z]+[0-9]+))?.*vnetid\s+([0-9]+)(?:.*parent\s+([a-z]+[0-9]+))?`)
	reHnDescr = regexp.MustCompile(`^description\s+"?([^"]*)"?`)
)

func (h *Host) adoptSwitch(sw *SwitchConf) *PortGroup {
	pg := &PortGroup{Name: sw.Name, Bridge: sw.Interface, Locked: sw.Locked}
	src, _ := h.readFile("/etc/hostname." + sw.Interface)
	for _, l := range lines(src) {
		l = strings.TrimSpace(l)
		if m := reHnDescr.FindStringSubmatch(l); m != nil {
			pg.Descr = strings.TrimPrefix(m[1], "maar: ")
		}
		if strings.Contains(" "+l+" ", " link1 ") {
			pg.Filter = true
		}
		m := reHnAdd.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		switch {
		case strings.HasPrefix(m[1], "vport"):
			pg.Vport = m[1]
			v, _ := h.readFile("/etc/hostname." + m[1])
			for _, vl := range lines(v) {
				if im := reHnInet.FindStringSubmatch(strings.TrimSpace(vl)); im != nil {
					pg.Gateway = inetToCIDR(im[1], im[2], im[3])
					break
				}
			}
		case strings.HasPrefix(m[1], "vlan"):
			v, _ := h.readFile("/etc/hostname." + m[1])
			if vm := reHnVLAN.FindStringSubmatch(strings.Join(lines(v), " ")); vm != nil {
				pg.VLAN, _ = strconv.Atoi(vm[2])
				pg.Uplink = vm[1] + vm[3]
			}
		case !strings.HasPrefix(m[1], "tap"):
			pg.Uplink = m[1]
		}
	}
	return pg
}

func inetToCIDR(addr, bits, mask string) string {
	a, err := netip.ParseAddr(addr)
	if err != nil {
		return ""
	}
	n := 24
	switch {
	case bits != "":
		n, _ = strconv.Atoi(bits)
	case strings.HasPrefix(mask, "0x"):
		v, _ := strconv.ParseUint(mask[2:], 16, 32)
		n = popcount(uint32(v))
	case mask != "":
		if m, err := netip.ParseAddr(mask); err == nil && m.Is4() {
			b := m.As4()
			n = popcount(uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3]))
		}
	}
	return netip.PrefixFrom(a, n).String()
}

func popcount(v uint32) int {
	n := 0
	for ; v != 0; v &= v - 1 {
		n++
	}
	return n
}

// ---- Adressrechnung ----

func (p *PortGroup) net() (prefix netip.Prefix, gw netip.Addr, ok bool) {
	if p.Gateway == "" {
		return
	}
	pf, err := netip.ParsePrefix(p.Gateway)
	if err != nil {
		return
	}
	return pf.Masked(), pf.Addr(), true
}

func mask4(bits int) string {
	m := ^uint32(0) << (32 - bits)
	return fmt.Sprintf("%d.%d.%d.%d", m>>24, m>>16&255, m>>8&255, m&255)
}

func broadcast(p netip.Prefix) netip.Addr {
	b := p.Addr().As4()
	v := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	v |= ^(^uint32(0) << (32 - p.Bits()))
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}

func (p *PortGroup) inRange(a netip.Addr) bool {
	from, err1 := netip.ParseAddr(p.RangeFrom)
	to, err2 := netip.ParseAddr(p.RangeTo)
	return p.DHCP && err1 == nil && err2 == nil && a.Compare(from) >= 0 && a.Compare(to) <= 0
}

func (p *PortGroup) vlanIf() string {
	if p.VLAN > 0 {
		return "vlan" + strconv.Itoa(p.VLAN)
	}
	return ""
}

// member ist das Interface, über das der Switch nach außen geht.
func (p *PortGroup) member() string {
	if p.VLAN > 0 {
		return p.vlanIf()
	}
	return p.Uplink
}

// vmGroup ist die Interface-Gruppe aller VM-Ports dieses Switches (für pf).
// Gruppennamen dürfen höchstens 15 Zeichen lang sein und nicht auf eine Ziffer enden.
func vmGroup(sw string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(sw) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		}
		if b.Len() == 12 {
			break
		}
	}
	return b.String() + "_vm"
}

// ---- Prüfung ----

var (
	reDomain = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)
	reHost   = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	reMAC    = regexp.MustCompile(`^[0-9a-f]{2}(:[0-9a-f]{2}){5}$`)
	reDescr  = regexp.MustCompile(`^[^"\\\x00-\x1f]{0,60}$`)
	rePort   = regexp.MustCompile(`^[0-9]{1,5}(:[0-9]{1,5})?(,[0-9]{1,5}(:[0-9]{1,5})?)*$`)
)

// Diese Interface-Arten kommen als Uplink nicht in Frage.
var virtualIf = []string{"lo", "enc", "pflog", "pfsync", "veb", "vport", "tap", "vlan", "svlan", "bridge", "vether", "wg", "gif", "gre", "tun", "carp", "mpe", "mpw", "egre", "nvgre", "vxlan", "etherip", "pair", "tpmr"}

func isVirtual(name string) bool {
	base := strings.TrimRight(name, "0123456789")
	for _, v := range virtualIf {
		if base == v {
			return true
		}
	}
	return false
}

func hasRoutableAddr(i *Iface) bool {
	for _, a := range i.Addrs {
		if a.Family == "inet" || !strings.HasPrefix(a.Addr, "fe80") {
			return true
		}
	}
	return false
}

// freeIf sucht den ersten freien Namen prefixN.
func (h *Host) freeIf(st *netState, prefix string, extra ...string) string {
	taken := map[string]bool{}
	for _, i := range st.ifaces {
		taken[i.Name] = true
	}
	for _, p := range st.cfg.PortGroups {
		taken[p.Bridge], taken[p.Vport] = true, true
	}
	for _, e := range extra {
		taken[e] = true
	}
	for n := 0; ; n++ {
		name := prefix + strconv.Itoa(n)
		if _, err := os.Stat(h.P("/etc/hostname." + name)); !taken[name] && os.IsNotExist(err) {
			return name
		}
	}
}

func (h *Host) validatePortGroup(st *netState, pg, old *PortGroup) error {
	if old == nil {
		if err := checkName("Portgruppe", pg.Name, reName); err != nil {
			return err
		}
		if st.cfg.group(pg.Name) != nil || st.conf.Switch(pg.Name) != nil {
			return invalid("Portgruppe %q existiert bereits", pg.Name)
		}
		pg.Bridge = h.freeIf(st, "veb")
	} else {
		pg.Name, pg.Bridge = old.Name, old.Bridge
	}
	if !reDescr.MatchString(pg.Descr) {
		return invalid("Beschreibung: höchstens 60 Zeichen, keine Anführungszeichen")
	}
	others := []*PortGroup{}
	for _, p := range st.cfg.PortGroups {
		if p.Name != pg.Name {
			others = append(others, p)
		}
	}
	// Uplink und VLAN
	if pg.VLAN < 0 || pg.VLAN > 4094 {
		return invalid("VLAN-ID %d (erlaubt: 1–4094, 0 = ohne)", pg.VLAN)
	}
	if pg.Uplink == "" && pg.VLAN > 0 {
		return invalid("ein VLAN braucht einen Uplink")
	}
	if pg.Uplink != "" {
		if err := checkName("Uplink", pg.Uplink, reIface); err != nil {
			return err
		}
		up := st.iface(pg.Uplink)
		if up == nil {
			return invalid("Uplink %s gibt es auf diesem Host nicht", pg.Uplink)
		}
		if isVirtual(pg.Uplink) {
			return invalid("%s ist kein physisches Interface", pg.Uplink)
		}
		for _, p := range others {
			if p.Uplink == pg.Uplink && (p.VLAN == 0 || pg.VLAN == 0) {
				return invalid("%s ist schon Uplink der Portgruppe %q – ungetaggt kann ein Interface nur einer Portgruppe gehören", pg.Uplink, p.Name)
			}
			if pg.VLAN > 0 && p.VLAN == pg.VLAN {
				return invalid("VLAN %d gehört schon zur Portgruppe %q", pg.VLAN, p.Name)
			}
		}
		if pg.VLAN == 0 && hasRoutableAddr(up) {
			return invalid("%s hat eigene IP-Adressen – als ungetaggter Switch-Port würde der Host darüber nicht mehr erreichbar sein. Nimm ein VLAN oder ein freies Interface", pg.Uplink)
		}
		if vi := pg.vlanIf(); vi != "" && st.iface(vi) != nil && (old == nil || old.vlanIf() != vi) {
			return invalid("%s existiert bereits außerhalb von maar", vi)
		}
	}
	// Gateway und Adressbereich
	if pg.Gateway == "" {
		if pg.DHCP || pg.DNS || pg.NAT {
			return invalid("DHCP, DNS und NAT brauchen eine Gateway-Adresse")
		}
		pg.Vport = ""
	} else {
		pf, err := netip.ParsePrefix(pg.Gateway)
		if err != nil || !pf.Addr().Is4() || pf.Bits() < 8 || pf.Bits() > 30 {
			return invalid("Gateway %q: IPv4-Adresse mit Präfix, z. B. 10.20.0.1/24", pg.Gateway)
		}
		pg.Gateway = pf.String()
		netw, gw, _ := pg.net()
		if gw == netw.Addr() || gw == broadcast(netw) {
			return invalid("%s ist Netz- oder Broadcast-Adresse", gw)
		}
		for _, p := range others {
			if on, _, ok := p.net(); ok && on.Overlaps(netw) {
				return invalid("%s überschneidet sich mit Portgruppe %q (%s)", netw, p.Name, on)
			}
		}
		for _, i := range st.ifaces {
			if old != nil && i.Name == old.Vport {
				continue
			}
			for _, a := range i.Addrs {
				if ip, err := netip.ParseAddr(a.Addr); err == nil && ip.Is4() && netw.Contains(ip) && !ip.IsLoopback() {
					return invalid("%s liegt im Netz von %s (%s)", netw, i.Name, ip)
				}
			}
		}
		if old != nil && old.Vport != "" {
			pg.Vport = old.Vport
		} else {
			pg.Vport = h.freeIf(st, "vport")
		}
	}
	if pg.DHCP && (pg.RangeFrom != "" || pg.RangeTo != "") {
		netw, gw, _ := pg.net()
		from, err1 := netip.ParseAddr(pg.RangeFrom)
		to, err2 := netip.ParseAddr(pg.RangeTo)
		if err1 != nil || err2 != nil || !netw.Contains(from) || !netw.Contains(to) || from.Compare(to) > 0 {
			return invalid("DHCP-Bereich muss im Netz %s liegen (von ≤ bis)", netw)
		}
		if pg.inRange(gw) || from == netw.Addr() || to == broadcast(netw) {
			return invalid("der DHCP-Bereich darf Gateway, Netz- und Broadcast-Adresse nicht enthalten")
		}
	}
	if pg.DNS || pg.Domain != "" {
		pg.Domain = strings.TrimSuffix(strings.ToLower(pg.Domain), ".")
		if !reDomain.MatchString(pg.Domain) || len(pg.Domain) > 200 {
			return invalid("Domain %q", pg.Domain)
		}
	}
	// Feste Adressen müssen ins (neue) Netz passen.
	for _, n := range st.nics() {
		if n.Switch != pg.Name || n.IP == "" {
			continue
		}
		ip, _ := netip.ParseAddr(n.IP)
		netw, gw, ok := pg.net()
		if !ok || !netw.Contains(ip) || ip == gw || pg.inRange(ip) {
			return invalid("die feste Adresse %s von VM %q passt nicht zum neuen Netz – zuerst die Adresse der VM ändern", n.IP, n.VM)
		}
	}
	return nil
}

// ---- VM-Anschlüsse ----

type VMNIC struct {
	VM       string `json:"vm"`
	Index    int    `json:"index"`
	Switch   string `json:"switch"`
	LLAddr   string `json:"lladdr"`
	IP       string `json:"ip,omitempty"`
	Hostname string `json:"hostname,omitempty"`
	FQDN     string `json:"fqdn,omitempty"`
}

// nics liefert alle Switch-Anschlüsse aus vm.conf mit ihren festen Adressen.
func (st *netState) nics() []VMNIC {
	var list []VMNIC
	for _, v := range st.conf.VMs {
		for i, ifc := range v.Interfaces {
			if ifc.Switch == "" {
				continue
			}
			n := VMNIC{VM: v.Name, Index: i, Switch: ifc.Switch, LLAddr: strings.ToLower(ifc.LLAddr)}
			if l := st.cfg.lease(v.Name, n.LLAddr); l != nil && n.LLAddr != "" {
				n.IP, n.Hostname = l.IP, l.Hostname
				if pg := st.cfg.group(n.Switch); pg != nil && pg.Domain != "" {
					n.FQDN = l.Hostname + "." + pg.Domain
				}
			}
			list = append(list, n)
		}
	}
	return list
}

// vmIPs liefert die festen Adressen einer VM.
func (st *netState) vmIPs(vm string) []string {
	var ips []string
	for _, n := range st.nics() {
		if n.VM == vm && n.IP != "" {
			ips = append(ips, n.IP)
		}
	}
	return ips
}

func hostLabel(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func randomMAC(taken map[string]bool) string {
	for {
		var b [3]byte
		rand.Read(b[:])
		// fe:e1:ba ist das Präfix, das auch vmd für zufällige Adressen nutzt (lokal verwaltet).
		m := fmt.Sprintf("fe:e1:ba:%02x:%02x:%02x", b[0], b[1], b[2])
		if !taken[m] {
			return m
		}
	}
}

// allocIP vergibt die erste freie Adresse ab .10 außerhalb des DHCP-Bereichs.
func (pg *PortGroup) allocIP(used map[netip.Addr]bool) (netip.Addr, error) {
	netw, gw, ok := pg.net()
	if !ok {
		return netip.Addr{}, invalid("Portgruppe %q hat kein Gateway – feste Adressen sind nicht möglich", pg.Name)
	}
	bc := broadcast(netw)
	a := netw.Addr()
	for i := 0; i < 10 && a.Next().Compare(bc) < 0; i++ {
		a = a.Next()
	}
	for start := a; ; {
		if a != gw && !used[a] && !pg.inRange(a) {
			return a, nil
		}
		if a = a.Next(); a.Compare(bc) >= 0 {
			a = netw.Addr().Next()
		}
		if a == start {
			return netip.Addr{}, invalid("im Netz %s ist keine Adresse mehr frei", netw)
		}
	}
}

// NICSpec ist ein Anschluss aus dem Dialog „Netzwerk der VM“.
type NICSpec struct {
	Switch   string `json:"switch"`
	LLAddr   string `json:"lladdr"`
	Addr     string `json:"addr"` // auto, dynamic oder eine IPv4-Adresse
	Hostname string `json:"hostname"`
}

// planNICs prüft die Anschlüsse einer VM und berechnet MACs und Adressen.
func (st *netState) planNICs(vm string, specs []NICSpec) ([]VMIface, []*Lease, error) {
	v := st.conf.VM(vm)
	if v == nil {
		return nil, nil, invalid("VM %q ist nicht in vm.conf definiert", vm)
	}
	if len(specs) > 8 {
		return nil, nil, invalid("höchstens 8 Anschlüsse")
	}
	macs := map[string]bool{}
	used := map[netip.Addr]bool{}
	names := map[string]string{} // fqdn → vm
	for _, n := range st.nics() {
		if n.VM == vm {
			continue
		}
		macs[n.LLAddr] = true
		if ip, err := netip.ParseAddr(n.IP); err == nil {
			used[ip] = true
		}
		if n.FQDN != "" {
			names[n.FQDN] = n.VM
		}
	}
	var ifs []VMIface
	var leases []*Lease
	for i, s := range specs {
		pg := st.cfg.group(s.Switch)
		if pg == nil || st.conf.Switch(s.Switch) == nil {
			return nil, nil, invalid("Portgruppe %q gibt es nicht", s.Switch)
		}
		mac := strings.ToLower(strings.TrimSpace(s.LLAddr))
		if mac == "" {
			mac = randomMAC(macs)
		}
		if !reMAC.MatchString(mac) || macs[mac] {
			return nil, nil, invalid("MAC-Adresse %q ist ungültig oder schon vergeben", mac)
		}
		macs[mac] = true
		ifs = append(ifs, VMIface{Switch: s.Switch, LLAddr: mac})
		if s.Addr == "dynamic" || s.Addr == "" {
			continue
		}
		var ip netip.Addr
		if s.Addr == "auto" {
			a, err := pg.allocIP(used)
			if err != nil {
				return nil, nil, err
			}
			ip = a
		} else {
			a, err := netip.ParseAddr(s.Addr)
			netw, gw, ok := pg.net()
			if err != nil || !ok || !netw.Contains(a) || a == gw || a == netw.Addr() || a == broadcast(netw) {
				return nil, nil, invalid("Adresse %q passt nicht ins Netz der Portgruppe %q", s.Addr, pg.Name)
			}
			if pg.inRange(a) {
				return nil, nil, invalid("%s liegt im dynamischen DHCP-Bereich – feste Adressen bitte außerhalb wählen", a)
			}
			if used[a] {
				return nil, nil, invalid("%s ist schon vergeben", a)
			}
			ip = a
		}
		used[ip] = true
		hn := strings.ToLower(strings.TrimSpace(s.Hostname))
		if hn == "" {
			hn = hostLabel(vm)
			if i > 0 {
				hn += "-" + strconv.Itoa(i)
			}
		}
		if !reHost.MatchString(hn) {
			return nil, nil, invalid("Hostname %q (Buchstaben, Ziffern, Bindestrich)", hn)
		}
		if pg.Domain != "" {
			if other, ok := names[hn+"."+pg.Domain]; ok {
				return nil, nil, invalid("%s.%s gehört schon zu VM %q", hn, pg.Domain, other)
			}
			names[hn+"."+pg.Domain] = vm
		}
		leases = append(leases, &Lease{VM: vm, LLAddr: mac, IP: ip.String(), Hostname: hn})
	}
	return ifs, leases, nil
}

// ---- Firewall je VM ----

func (st *netState) validateRules(vm string, pol *VMPolicy, rules []*FWRule) error {
	if st.conf.VM(vm) == nil {
		return invalid("VM %q ist nicht in vm.conf definiert", vm)
	}
	if pol.In != "allow" && pol.In != "deny" || pol.Out != "allow" && pol.Out != "deny" {
		return invalid("Richtlinie: allow oder deny")
	}
	if len(rules) > 100 {
		return invalid("höchstens 100 Regeln je VM")
	}
	for i, r := range rules {
		n := i + 1
		if r.Dir != "in" && r.Dir != "out" {
			return invalid("Regel %d: Richtung in oder out", n)
		}
		if r.Action != "pass" && r.Action != "block" {
			return invalid("Regel %d: Aktion pass oder block", n)
		}
		switch r.Proto {
		case "any", "icmp":
			r.Port = ""
		case "tcp", "udp":
		default:
			return invalid("Regel %d: Protokoll %q", n, r.Proto)
		}
		r.Port = strings.ReplaceAll(strings.TrimSpace(r.Port), " ", "")
		if r.Port != "" && !rePort.MatchString(r.Port) {
			return invalid("Regel %d: Port %q (z. B. 443, 8000:8080 oder 80,443)", n, r.Port)
		}
		for _, p := range strings.FieldsFunc(r.Port, func(c rune) bool { return c == ',' || c == ':' }) {
			if v, _ := strconv.Atoi(p); v < 1 || v > 65535 {
				return invalid("Regel %d: Port %s außerhalb 1–65535", n, p)
			}
		}
		r.Peer = strings.TrimSpace(r.Peer)
		switch {
		case r.Peer == "" || r.Peer == "any":
			r.Peer = "any"
		case strings.HasPrefix(r.Peer, "<") && strings.HasSuffix(r.Peer, ">") && reTable.MatchString(r.Peer[1:len(r.Peer)-1]):
		case reAddr.MatchString(r.Peer):
			if _, err := netip.ParsePrefix(r.Peer); err != nil {
				if _, err := netip.ParseAddr(r.Peer); err != nil {
					return invalid("Regel %d: Adresse %q", n, r.Peer)
				}
			}
		case st.conf.VM(r.Peer) != nil:
		default:
			return invalid("Regel %d: Gegenstelle %q – any, Adresse/Netz, <tabelle> oder VM-Name", n, r.Peer)
		}
		if !reDescr.MatchString(r.Descr) {
			return invalid("Regel %d: Beschreibung höchstens 60 Zeichen, keine Anführungszeichen", n)
		}
	}
	return nil
}

// ---- Übersicht für die Oberfläche ----

type PortGroupInfo struct {
	*PortGroup
	Subnet  string   `json:"subnet,omitempty"`
	Present bool     `json:"present"` // Switch-Interface existiert
	Members []string `json:"members"`
	VMs     []string `json:"vms"`
	Group   string   `json:"group"`
	Free    int      `json:"free"` // freie feste Adressen
}

type UplinkInfo struct {
	Name   string `json:"name"`
	Descr  string `json:"description,omitempty"`
	Status string `json:"status,omitempty"`
	Addr   bool   `json:"hasAddress"`
}

type VNetOverview struct {
	PortGroups []PortGroupInfo      `json:"portGroups"`
	NICs       []VMNIC              `json:"nics"`
	VMs        []string             `json:"vms"`
	Rules      map[string][]*FWRule `json:"rules"`
	Policies   map[string]VMPolicy  `json:"policies"`
	Uplinks    []UplinkInfo         `json:"uplinks"`
	Status     map[string]bool      `json:"status"`
	Warnings   []string             `json:"warnings"`
}

func (h *Host) VNet(ctx context.Context) (*VNetOverview, error) {
	h.netMu.Lock()
	defer h.netMu.Unlock()
	st, err := h.loadNetState(ctx)
	if err != nil {
		return nil, err
	}
	o := &VNetOverview{NICs: st.nics(), Rules: st.cfg.Rules, Policies: map[string]VMPolicy{}, Status: map[string]bool{}, Warnings: []string{}}
	if o.NICs == nil {
		o.NICs = []VMNIC{}
	}
	for _, v := range st.conf.VMs {
		o.VMs = append(o.VMs, v.Name)
		o.Policies[v.Name] = st.cfg.policy(v.Name)
	}
	for _, pg := range st.cfg.PortGroups {
		info := PortGroupInfo{PortGroup: pg, Members: []string{}, VMs: []string{}, Group: vmGroup(pg.Name)}
		if i := st.iface(pg.Bridge); i != nil {
			info.Present, info.Members = true, i.Members
		}
		if netw, _, ok := pg.net(); ok {
			info.Subnet = netw.String()
			used := map[netip.Addr]bool{}
			for _, n := range o.NICs {
				if ip, err := netip.ParseAddr(n.IP); err == nil {
					used[ip] = true
				}
			}
			info.Free = countFree(pg, used)
		}
		seen := map[string]bool{}
		for _, n := range o.NICs {
			if n.Switch == pg.Name && !seen[n.VM] {
				seen[n.VM] = true
				info.VMs = append(info.VMs, n.VM)
			}
		}
		o.PortGroups = append(o.PortGroups, info)
	}
	if o.PortGroups == nil {
		o.PortGroups = []PortGroupInfo{}
	}
	for _, i := range st.ifaces {
		if !isVirtual(i.Name) {
			o.Uplinks = append(o.Uplinks, UplinkInfo{Name: i.Name, Descr: i.Descr, Status: i.Status, Addr: hasRoutableAddr(i)})
		}
	}
	pfc, _ := h.readFile("/etc/pf.conf")
	o.Status["pfAnchor"] = reAnchorRef.MatchString(pfc)
	ub, _ := h.readFile(unboundMain)
	o.Status["unboundInclude"] = strings.Contains(ub, unboundPath)
	_, warn := st.pfAnchorConf()
	o.Warnings = append(o.Warnings, warn...)
	for _, n := range o.NICs {
		if n.LLAddr == "" {
			o.Warnings = append(o.Warnings, fmt.Sprintf("VM %s, Anschluss %d: ohne feste MAC-Adresse vergibt vmd bei jedem Start eine neue – DHCP-Reservierung und Regeln greifen erst nach „Netzwerk der VM“ → Speichern.", n.VM, n.Index))
		}
	}
	sort.Strings(o.Warnings)
	return o, nil
}

func countFree(pg *PortGroup, used map[netip.Addr]bool) int {
	netw, gw, _ := pg.net()
	if netw.Bits() < 20 {
		return -1 // großes Netz: nicht zählen
	}
	n := 0
	bc := broadcast(netw)
	for a := netw.Addr().Next(); a.Compare(bc) < 0; a = a.Next() {
		if a != gw && !used[a] && !pg.inRange(a) {
			n++
		}
	}
	return n
}
