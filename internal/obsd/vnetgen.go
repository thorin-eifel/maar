package obsd

import (
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
)

const genHeader = "# Automatisch erzeugt von maar (VM-Netze) – Änderungen hier werden überschrieben.\n"

// ---- Erzeugte Dateien ----

func (p *PortGroup) descr(suffix string) string {
	d := "maar: " + p.Name
	if p.Descr != "" {
		d += " – " + p.Descr
	}
	return fmt.Sprintf("description %s\n", quote(d+suffix))
}

// hostnameFiles liefert Inhalt je hostname.if-Datei dieser Portgruppe.
func (p *PortGroup) hostnameFiles() map[string]string {
	f := map[string]string{}
	var veb strings.Builder
	veb.WriteString(p.descr(""))
	if p.Vport != "" {
		veb.WriteString("add " + p.Vport + "\n")
		netw, gw, _ := p.net()
		f["/etc/hostname."+p.Vport] = fmt.Sprintf("inet %s %s\n%sup\n", gw, mask4(netw.Bits()), p.descr(" (Gateway)"))
	}
	if m := p.member(); m != "" {
		veb.WriteString("add " + m + "\n")
	}
	if vi := p.vlanIf(); vi != "" {
		f["/etc/hostname."+vi] = fmt.Sprintf("parent %s vnetid %d\n%sup\n", p.Uplink, p.VLAN, p.descr(fmt.Sprintf(" (VLAN %d)", p.VLAN)))
	}
	if p.Filter {
		veb.WriteString("link1\n")
	}
	veb.WriteString("up\n")
	f["/etc/hostname."+p.Bridge] = veb.String()
	return f
}

func (p *PortGroup) switchBlock() string {
	var b strings.Builder
	fmt.Fprintf(&b, "switch %s {\n\tinterface %s\n\tgroup %s\n", quote(p.Name), p.Bridge, quote(vmGroup(p.Name)))
	if p.Locked {
		b.WriteString("\tlocked lladdr\n")
	}
	b.WriteString("}\n")
	return b.String()
}

func (st *netState) sortedGroups() []*PortGroup {
	g := append([]*PortGroup(nil), st.cfg.PortGroups...)
	sort.Slice(g, func(i, j int) bool { return g[i].Name < g[j].Name })
	return g
}

const (
	dhcpBegin = "# >>> maar: VM-Netze (automatisch erzeugt, nicht von Hand ändern)"
	dhcpEnd   = "# <<< maar"
)

func (st *netState) dhcpdBlock() string {
	var b strings.Builder
	nics := st.nics()
	for _, pg := range st.sortedGroups() {
		netw, gw, ok := pg.net()
		if !pg.DHCP || !ok {
			continue
		}
		fmt.Fprintf(&b, "\n# Portgruppe %s (%s)\nsubnet %s netmask %s {\n\toption routers %s;\n", pg.Name, pg.Bridge, netw.Addr(), mask4(netw.Bits()), gw)
		if pg.DNS {
			fmt.Fprintf(&b, "\toption domain-name-servers %s;\n", gw)
		}
		if pg.Domain != "" {
			fmt.Fprintf(&b, "\toption domain-name %s;\n", quote(pg.Domain))
		}
		if pg.RangeFrom != "" {
			fmt.Fprintf(&b, "\trange %s %s;\n", pg.RangeFrom, pg.RangeTo)
		}
		for _, n := range nics {
			if n.Switch == pg.Name && n.IP != "" {
				fmt.Fprintf(&b, "\thost %s {\n\t\thardware ethernet %s;\n\t\tfixed-address %s;\n\t\toption host-name %s;\n\t}\n",
					quote(fmt.Sprintf("%s-%d", hostLabel(n.VM), n.Index)), n.LLAddr, n.IP, quote(n.Hostname))
			}
		}
		b.WriteString("}\n")
	}
	return b.String()
}

// mergeManaged ersetzt den maar-Bereich in src (oder hängt ihn an).
func mergeManaged(src, block string) string {
	region := dhcpBegin + "\n" + strings.TrimLeft(block, "\n") + dhcpEnd + "\n"
	if i := strings.Index(src, dhcpBegin); i >= 0 {
		if j := strings.Index(src[i:], dhcpEnd); j >= 0 {
			end := lineEnd(src, i+j)
			return src[:i] + region + src[end:]
		}
	}
	if strings.TrimSpace(block) == "" {
		return src
	}
	if src != "" && !strings.HasSuffix(src, "\n") {
		src += "\n"
	}
	if src != "" {
		src += "\n"
	}
	return src + region
}

var reSubnetDecl = regexp.MustCompile(`(?m)^\s*subnet\s+([0-9.]+)\s+netmask\s+([0-9.]+)`)

// foreignSubnets meldet Subnetze, die außerhalb des maar-Bereichs schon deklariert sind.
func foreignSubnets(src string) map[string]bool {
	if i := strings.Index(src, dhcpBegin); i >= 0 {
		if j := strings.Index(src[i:], dhcpEnd); j >= 0 {
			src = src[:i] + src[lineEnd(src, i+j):]
		}
	}
	m := map[string]bool{}
	for _, s := range reSubnetDecl.FindAllStringSubmatch(src, -1) {
		m[s[1]] = true
	}
	return m
}

func (st *netState) unboundConf(mainSrc string) string {
	var b strings.Builder
	b.WriteString(genHeader + "server:\n")
	explicit := regexp.MustCompile(`(?m)^\s*interface:\s*(\S+)`).FindAllStringSubmatch(mainSrc, -1)
	have := map[string]bool{}
	for _, m := range explicit {
		have[m[1]] = true
	}
	nics := st.nics()
	var dns []*PortGroup
	for _, pg := range st.sortedGroups() {
		if _, _, ok := pg.net(); ok && pg.DNS {
			dns = append(dns, pg)
		}
	}
	if len(dns) > 0 && len(explicit) == 0 {
		// Ohne eigene interface-Zeilen lauscht unbound nur lokal; das muss so bleiben.
		b.WriteString("\tinterface: 127.0.0.1\n\tinterface: ::1\n")
	}
	for _, pg := range dns {
		netw, gw, _ := pg.net()
		fmt.Fprintf(&b, "\n\t# Portgruppe %s\n", pg.Name)
		if !have[gw.String()] {
			fmt.Fprintf(&b, "\tinterface: %s\n", gw)
		}
		fmt.Fprintf(&b, "\taccess-control: %s allow\n", netw)
		if pg.Domain == "" {
			continue
		}
		fmt.Fprintf(&b, "\tlocal-zone: \"%s.\" static\n", pg.Domain)
		for _, n := range nics {
			if n.Switch == pg.Name && n.IP != "" {
				fmt.Fprintf(&b, "\tlocal-data: \"%s. 300 IN A %s\"\n\tlocal-data-ptr: \"%s 300 %s.\"\n", n.FQDN, n.IP, n.IP, n.FQDN)
			}
		}
	}
	return b.String()
}

func pfList(items []string) string {
	if len(items) == 1 {
		return items[0]
	}
	return "{ " + strings.Join(items, " ") + " }"
}

func pfPort(p string) string { return pfList(strings.Split(p, ",")) }

// pfAnchorConf erzeugt den Regelsatz für den Anker "maar". Regeln je VM
// sind quick und ohne Richtung: Sie greifen auf jedem Interface, über das
// der Verkehr läuft (vport, Uplink und – mit link1 – die VM-Ports).
func (st *netState) pfAnchorConf() (string, []string) {
	var b strings.Builder
	var warn []string
	b.WriteString(genHeader + "# Geladen über den Anker \"maar\" in /etc/pf.conf.\n")
	for _, pg := range st.sortedGroups() {
		netw, gw, ok := pg.net()
		if !ok && !pg.Filter {
			continue
		}
		fmt.Fprintf(&b, "\n# Portgruppe %s (%s)\n", pg.Name, pg.Bridge)
		if ok {
			if pg.NAT {
				fmt.Fprintf(&b, "match out on egress inet from %s to any nat-to (egress)\n", netw)
			}
			fmt.Fprintf(&b, "pass in on %s inet from %s\n", pg.Vport, netw)
			if pg.DHCP {
				fmt.Fprintf(&b, "pass in quick on %s inet proto udp from port bootpc to port bootps\n", pg.Vport)
				fmt.Fprintf(&b, "pass out quick on %s inet proto udp from %s port bootps to port bootpc\n", pg.Vport, gw)
			}
			if pg.DNS {
				fmt.Fprintf(&b, "pass in quick on %s inet proto { tcp udp } from %s to %s port domain\n", pg.Vport, netw, gw)
			}
		}
		if pg.Filter {
			// pf läuft mit link1 auch auf den Switch-Ports: Grundsätzlich durchlassen,
			// die quick-Regeln je VM entscheiden vorher.
			fmt.Fprintf(&b, "pass on %s\n", vmGroup(pg.Name))
			if m := pg.member(); m != "" {
				fmt.Fprintf(&b, "pass on %s\n", m)
			}
		}
	}
	vms := make([]string, 0, len(st.conf.VMs))
	for _, v := range st.conf.VMs {
		vms = append(vms, v.Name)
	}
	sort.Strings(vms)
	for _, vm := range vms {
		rules, pol := st.cfg.Rules[vm], st.cfg.policy(vm)
		if len(rules) == 0 && pol.In == "allow" && pol.Out == "allow" {
			continue
		}
		ips := st.vmIPs(vm)
		if len(ips) == 0 {
			warn = append(warn, fmt.Sprintf("VM %s hat Firewall-Regeln, aber keine feste Adresse – die Regeln sind nicht aktiv.", vm))
			continue
		}
		self := pfList(ips)
		fmt.Fprintf(&b, "\n# VM %s – %s\n", vm, strings.Join(ips, ", "))
		for _, r := range rules {
			peer := r.Peer
			if v := st.conf.VM(peer); v != nil {
				p := st.vmIPs(peer)
				if len(p) == 0 {
					warn = append(warn, fmt.Sprintf("VM %s: Gegenstelle %s hat keine feste Adresse – Regel ausgelassen.", vm, peer))
					continue
				}
				peer = pfList(p)
			}
			var l strings.Builder
			l.WriteString(r.Action)
			if r.Log {
				l.WriteString(" log")
			}
			l.WriteString(" quick inet")
			if r.Proto != "any" {
				l.WriteString(" proto " + r.Proto)
			}
			from, to := peer, self
			if r.Dir == "out" {
				from, to = self, peer
			}
			fmt.Fprintf(&l, " from %s to %s", from, to)
			if r.Port != "" {
				l.WriteString(" port " + pfPort(r.Port))
			}
			if r.Descr != "" {
				fmt.Fprintf(&l, " label %s", quote(r.Descr))
			}
			b.WriteString(l.String() + "\n")
		}
		if pol.In == "deny" {
			fmt.Fprintf(&b, "block log quick inet from any to %s\n", self)
		}
		if pol.Out == "deny" {
			fmt.Fprintf(&b, "block log quick inet from %s to any\n", self)
		}
	}
	return b.String(), warn
}

var reAnchorRef = regexp.MustCompile(`(?m)^\s*anchor\s+"?maar"?`)

// ---- Anwenden ----

// putFile schreibt eine Datei über WriteConfig (Prüfung, Sicherung), nur wenn sie sich ändert.
func (h *Host) putFile(ctx context.Context, w io.Writer, p, content string) (bool, error) {
	if cur, err := h.readFile(p); err == nil && cur == content || err != nil && content == "" {
		return false, nil
	}
	step(w, "%s schreiben", p)
	_, err := h.WriteConfig(ctx, p, content, 0)
	return err == nil, err
}

func (h *Host) dropFile(w io.Writer, p string) {
	if _, err := os.Stat(h.P(p)); err != nil {
		return
	}
	step(w, "%s entfernen (Sicherung bleibt erhalten)", p)
	h.backup(p)
	os.Remove(h.P(p))
}

func (h *Host) cmd(ctx context.Context, w io.Writer, name string, args ...string) error {
	fmt.Fprintf(w, "$ %s %s\n", name, strings.Join(args, " "))
	r, err := h.R.Run(ctx, nil, name, args...)
	if out := strings.TrimSpace(string(r.Stdout) + string(r.Stderr)); out != "" {
		fmt.Fprintln(w, out)
	}
	return err
}

// syncServices bringt dhcpd, unbound und den pf-Anker auf den Stand der Konfiguration.
func (h *Host) syncServices(ctx context.Context, w io.Writer, st *netState) error {
	// DHCP
	cur, _ := h.readFile(dhcpdPath)
	foreign := foreignSubnets(cur)
	var vports []string
	for _, pg := range st.sortedGroups() {
		if netw, _, ok := pg.net(); ok && pg.DHCP {
			if foreign[netw.Addr().String()] {
				return fmt.Errorf("%s deklariert das Subnetz %s schon außerhalb des maar-Bereichs – bitte dort entfernen", dhcpdPath, netw)
			}
			vports = append(vports, pg.Vport)
		}
	}
	changed, err := h.putFile(ctx, w, dhcpdPath, mergeManaged(cur, st.dhcpdBlock()))
	if err != nil {
		return err
	}
	flags := h.dhcpdFlags(ctx, st, vports)
	if flags != nil {
		step(w, "dhcpd auf %s", strings.Join(flags, " "))
		if err := h.cmd(ctx, w, "rcctl", append([]string{"set", "dhcpd", "flags"}, flags...)...); err != nil {
			return err
		}
		changed = true
	}
	if changed && len(vports) > 0 {
		if err := h.cmd(ctx, w, "rcctl", "restart", "dhcpd"); err != nil {
			return err
		}
	}

	// DNS
	mainSrc, mainErr := h.readFile(unboundMain)
	need := false
	for _, pg := range st.cfg.PortGroups {
		need = need || pg.DNS
	}
	if need || h.exists(unboundPath) {
		changed, err := h.putFile(ctx, w, unboundPath, st.unboundConf(mainSrc))
		if err != nil {
			return err
		}
		if mainErr == nil && !strings.Contains(mainSrc, unboundPath) {
			step(w, "unbound.conf bindet %s ein", unboundPath)
			if _, err := h.WriteConfig(ctx, unboundMain, strings.TrimRight(mainSrc, "\n")+"\n\n# VM-Netze von maar\ninclude: \""+unboundPath+"\"\n", 0); err != nil {
				return err
			}
			changed = true
		} else if mainErr != nil && need {
			fmt.Fprintf(w, "Hinweis: %s fehlt – DNS-Einträge sind erzeugt, aber unbound ist nicht eingerichtet.\n", unboundMain)
		}
		if changed && mainErr == nil {
			if err := h.cmd(ctx, w, "rcctl", "restart", "unbound"); err != nil {
				return err
			}
		}
	}
	return h.syncPF(ctx, w, st)
}

func (h *Host) exists(p string) bool {
	_, err := os.Stat(h.P(p))
	return err == nil
}

// dhcpdFlags berechnet neue dhcpd_flags oder nil, wenn sie schon stimmen.
// Interfaces, die maar nicht verwaltet, bleiben unangetastet.
func (h *Host) dhcpdFlags(ctx context.Context, st *netState, want []string) []string {
	out := h.outLenient(ctx, "rcctl", "get", "dhcpd", "flags")
	cur := strings.Fields(strings.TrimSpace(out))
	if len(cur) == 1 && cur[0] == "NO" {
		cur = nil
	}
	managed := map[string]bool{}
	for _, g := range st.gone {
		managed[g] = true
	}
	for _, pg := range st.cfg.PortGroups {
		if pg.Managed && pg.Vport != "" {
			managed[pg.Vport] = true
		}
	}
	var next []string
	seen := map[string]bool{}
	for _, f := range cur {
		if !managed[f] || contains(want, f) {
			next = append(next, f)
			seen[f] = true
		}
	}
	for _, f := range want {
		if !seen[f] {
			next = append(next, f)
		}
	}
	if strings.Join(next, " ") == strings.Join(cur, " ") || len(next) == 0 {
		return nil
	}
	return next
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func (h *Host) syncPF(ctx context.Context, w io.Writer, st *netState) error {
	conf, warn := st.pfAnchorConf()
	for _, m := range warn {
		fmt.Fprintln(w, "Hinweis: "+m)
	}
	changed, err := h.putFile(ctx, w, pfAnchorPath, conf)
	if err != nil {
		return err
	}
	pfc, err := h.readFile("/etc/pf.conf")
	if err != nil {
		return err
	}
	if !reAnchorRef.MatchString(pfc) {
		step(w, "pf.conf um den Anker \"maar\" ergänzen")
		pfc = strings.TrimRight(pfc, "\n") + "\n\n# VM-Netze von maar: NAT, Portgruppen und Regeln je VM\nanchor \"" + pfAnchorName + "\"\nload anchor \"" + pfAnchorName + "\" from \"" + pfAnchorPath + "\"\n"
		if _, err := h.WriteConfig(ctx, "/etc/pf.conf", pfc, 0); err != nil {
			return err
		}
		return h.cmd(ctx, w, "pfctl", "-f", "/etc/pf.conf")
	}
	if changed {
		return h.cmd(ctx, w, "pfctl", "-a", pfAnchorName, "-f", pfAnchorPath)
	}
	return nil
}

// NetPreview zeigt, was maar aus der aktuellen Konfiguration erzeugen würde.
func (h *Host) NetPreview(ctx context.Context) (map[string]any, error) {
	h.netMu.Lock()
	defer h.netMu.Unlock()
	st, err := h.loadNetState(ctx)
	if err != nil {
		return nil, err
	}
	mainSrc, _ := h.readFile(unboundMain)
	pf, warn := st.pfAnchorConf()
	files := map[string]string{}
	for _, pg := range st.cfg.PortGroups {
		for p, c := range pg.hostnameFiles() {
			files[p] = c
		}
		files["vm.conf: switch "+pg.Name] = pg.switchBlock()
	}
	return map[string]any{
		"dhcpd":    dhcpBegin + "\n" + strings.TrimLeft(st.dhcpdBlock(), "\n") + dhcpEnd + "\n",
		"unbound":  st.unboundConf(mainSrc),
		"pf":       pf,
		"files":    files,
		"warnings": append([]string{}, warn...),
	}, nil
}

// ---- Aufgaben ----

// SavePortGroup legt eine Portgruppe an (name leer) oder ändert sie.
func (h *Host) SavePortGroup(ctx context.Context, name string, in *PortGroup) (*Task, error) {
	check := func(st *netState) (*PortGroup, *PortGroup, error) {
		pg := *in
		var old *PortGroup
		if name != "" {
			if old = st.cfg.group(name); old == nil {
				return nil, nil, invalid("Portgruppe %q gibt es nicht", name)
			}
		}
		return &pg, old, h.validatePortGroup(st, &pg, old)
	}
	h.netMu.Lock()
	st, err := h.loadNetState(ctx)
	if err == nil {
		_, _, err = check(st)
	}
	h.netMu.Unlock()
	if err != nil {
		return nil, err
	}
	title := "Portgruppe " + in.Name + " anlegen"
	if name != "" {
		title = "Portgruppe " + name + " ändern"
	}
	return &Task{Title: title, Command: "maar vnet", Run: func(ctx context.Context, w io.Writer) error {
		h.netMu.Lock()
		defer h.netMu.Unlock()
		st, err := h.loadNetState(ctx)
		if err != nil {
			return err
		}
		pg, old, err := check(st)
		if err != nil {
			return err
		}
		return h.applyPortGroup(ctx, w, st, pg, old)
	}}, nil
}

func (h *Host) applyPortGroup(ctx context.Context, w io.Writer, st *netState, pg, old *PortGroup) error {
	files := pg.hostnameFiles()
	var up []string
	// Ein ungenutzter Uplink muss selbst „up“ sein, sonst fließt nichts.
	if pg.Uplink != "" && !h.exists("/etc/hostname."+pg.Uplink) {
		files["/etc/hostname."+pg.Uplink] = fmt.Sprintf("description %s\nup\n", quote("maar: Uplink"))
		up = append(up, pg.Uplink)
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if _, err := h.putFile(ctx, w, p, files[p]); err != nil {
			return err
		}
	}
	// Aufräumen, was die alte Fassung brauchte und die neue nicht mehr.
	if old != nil {
		if m := old.member(); m != "" && m != pg.member() {
			h.cmd(ctx, w, "ifconfig", old.Bridge, "del", m)
		}
		if vi := old.vlanIf(); vi != "" && vi != pg.vlanIf() {
			h.cmd(ctx, w, "ifconfig", vi, "destroy")
			h.dropFile(w, "/etc/hostname."+vi)
		}
		if old.Vport != "" && pg.Vport == "" {
			st.gone = append(st.gone, old.Vport)
			h.cmd(ctx, w, "ifconfig", old.Bridge, "del", old.Vport)
			h.cmd(ctx, w, "ifconfig", old.Vport, "destroy")
			h.dropFile(w, "/etc/hostname."+old.Vport)
		} else if _, ogw, ok := old.net(); ok && old.Gateway != pg.Gateway {
			h.cmd(ctx, w, "ifconfig", old.Vport, "inet", ogw.String(), "delete")
		}
		if old.Filter && !pg.Filter {
			h.cmd(ctx, w, "ifconfig", old.Bridge, "-link1")
		}
	}
	// Reihenfolge: Mitglieder vor dem Switch, der sie mit "add" aufnimmt.
	order := up
	if vi := pg.vlanIf(); vi != "" {
		order = append(order, vi)
	}
	if pg.Vport != "" {
		order = append(order, pg.Vport)
	}
	order = append(order, pg.Bridge)
	step(w, "Interfaces anwenden")
	if err := h.cmd(ctx, w, "sh", append([]string{"/etc/netstart"}, order...)...); err != nil {
		return err
	}
	step(w, "vm.conf: Switch %q", pg.Name)
	if err := h.setSwitchBlock(ctx, pg.Name, pg.switchBlock()); err != nil {
		return err
	}
	pg.Managed = true
	if old != nil {
		*old = *pg
	} else {
		st.cfg.PortGroups = append(st.cfg.PortGroups, pg)
	}
	if err := h.saveNetConfig(st.cfg); err != nil {
		return err
	}
	if err := h.syncServices(ctx, w, st); err != nil {
		return err
	}
	step(w, "Portgruppe %s ist bereit", pg.Name)
	return nil
}

func (h *Host) setSwitchBlock(ctx context.Context, name, block string) error {
	h.confMu.Lock()
	defer h.confMu.Unlock()
	conf, err := h.loadVMConf()
	if err != nil {
		return err
	}
	if s := conf.Switch(name); s != nil && s.Raw == block {
		return nil
	}
	if _, err := h.WriteConfig(ctx, vmConfPath, conf.SetSwitch(name, block), 0); err != nil {
		return err
	}
	return h.run(ctx, "vmctl", "reload")
}

func (h *Host) DeletePortGroup(ctx context.Context, name string) (*Task, error) {
	check := func(st *netState) (*PortGroup, error) {
		pg := st.cfg.group(name)
		if pg == nil {
			return nil, invalid("Portgruppe %q gibt es nicht", name)
		}
		var users []string
		for _, n := range st.nics() {
			if n.Switch == name && !contains(users, n.VM) {
				users = append(users, n.VM)
			}
		}
		if len(users) > 0 {
			return nil, invalid("Portgruppe %q wird noch von %s benutzt – Anschlüsse zuerst umhängen", name, strings.Join(users, ", "))
		}
		return pg, nil
	}
	h.netMu.Lock()
	st, err := h.loadNetState(ctx)
	if err == nil {
		_, err = check(st)
	}
	h.netMu.Unlock()
	if err != nil {
		return nil, err
	}
	return &Task{Title: "Portgruppe " + name + " löschen", Command: "maar vnet", Run: func(ctx context.Context, w io.Writer) error {
		h.netMu.Lock()
		defer h.netMu.Unlock()
		st, err := h.loadNetState(ctx)
		if err != nil {
			return err
		}
		pg, err := check(st)
		if err != nil {
			return err
		}
		step(w, "vm.conf: Switch %q entfernen", name)
		if err := h.setSwitchBlock(ctx, name, ""); err != nil {
			return err
		}
		h.cmd(ctx, w, "ifconfig", pg.Bridge, "destroy")
		h.dropFile(w, "/etc/hostname."+pg.Bridge)
		if pg.Vport != "" {
			st.gone = append(st.gone, pg.Vport)
			h.cmd(ctx, w, "ifconfig", pg.Vport, "destroy")
			h.dropFile(w, "/etc/hostname."+pg.Vport)
		}
		if vi := pg.vlanIf(); vi != "" {
			h.cmd(ctx, w, "ifconfig", vi, "destroy")
			h.dropFile(w, "/etc/hostname."+vi)
		}
		var keep []*PortGroup
		for _, p := range st.cfg.PortGroups {
			if p.Name != name {
				keep = append(keep, p)
			}
		}
		st.cfg.PortGroups = keep
		if err := h.saveNetConfig(st.cfg); err != nil {
			return err
		}
		return h.syncServices(ctx, w, st)
	}}, nil
}

// SetVMNICs ersetzt die Switch-Anschlüsse einer VM samt festen Adressen.
func (h *Host) SetVMNICs(ctx context.Context, vm string, specs []NICSpec) (*Task, error) {
	h.netMu.Lock()
	st, err := h.loadNetState(ctx)
	if err == nil {
		_, _, err = st.planNICs(vm, specs)
	}
	h.netMu.Unlock()
	if err != nil {
		return nil, err
	}
	return &Task{Title: "Netzwerk von " + vm, Command: "maar vnet", Run: func(ctx context.Context, w io.Writer) error {
		h.netMu.Lock()
		defer h.netMu.Unlock()
		st, err := h.loadNetState(ctx)
		if err != nil {
			return err
		}
		ifs, leases, err := st.planNICs(vm, specs)
		if err != nil {
			return err
		}
		for i, f := range ifs {
			ip := "dynamisch"
			for _, l := range leases {
				if l.LLAddr == f.LLAddr {
					ip = l.IP + " (" + l.Hostname + ")"
				}
			}
			fmt.Fprintf(w, "Anschluss %d: %s, %s, %s\n", i, f.Switch, f.LLAddr, ip)
		}
		step(w, "vm.conf: Anschlüsse von %s", vm)
		v := st.conf.VM(vm)
		if block := v.SetInterfaces(ifs); block != v.Raw {
			if err := h.UpdateVMBlock(ctx, vm, block); err != nil {
				return err
			}
		}
		var keep []*Lease
		for _, l := range st.cfg.Leases {
			if l.VM != vm {
				keep = append(keep, l)
			}
		}
		st.cfg.Leases = append(keep, leases...)
		if err := h.saveNetConfig(st.cfg); err != nil {
			return err
		}
		if st.conf, err = h.loadVMConf(); err != nil {
			return err
		}
		if err := h.syncServices(ctx, w, st); err != nil {
			return err
		}
		fmt.Fprintln(w, "Geänderte Anschlüsse gelten ab dem nächsten Start der VM.")
		return nil
	}}, nil
}

// SetVMFirewall speichert Richtlinie und Regeln einer VM und lädt den pf-Anker neu.
func (h *Host) SetVMFirewall(ctx context.Context, vm string, pol VMPolicy, rules []*FWRule) (string, error) {
	h.netMu.Lock()
	defer h.netMu.Unlock()
	st, err := h.loadNetState(ctx)
	if err != nil {
		return "", err
	}
	if err := st.validateRules(vm, &pol, rules); err != nil {
		return "", err
	}
	if len(rules) == 0 {
		delete(st.cfg.Rules, vm)
	} else {
		st.cfg.Rules[vm] = rules
	}
	if pol.In == "allow" && pol.Out == "allow" {
		delete(st.cfg.Policies, vm)
	} else {
		st.cfg.Policies[vm] = &pol
	}
	if err := h.saveNetConfig(st.cfg); err != nil {
		return "", err
	}
	var out strings.Builder
	err = h.syncPF(ctx, &out, st)
	return strings.TrimSpace(out.String()), err
}

// SyncNetwork erzeugt alle Dienst-Konfigurationen neu.
func (h *Host) SyncNetwork() *Task {
	return &Task{Title: "VM-Netze abgleichen", Command: "maar vnet sync", Run: func(ctx context.Context, w io.Writer) error {
		h.netMu.Lock()
		defer h.netMu.Unlock()
		st, err := h.loadNetState(ctx)
		if err != nil {
			return err
		}
		if err := h.syncServices(ctx, w, st); err != nil {
			return err
		}
		step(w, "alles auf dem Stand der Konfiguration")
		return nil
	}}
}

// ForgetVMNet entfernt Adressen und Regeln einer gelöschten VM.
func (h *Host) ForgetVMNet(ctx context.Context, vm string) {
	h.netMu.Lock()
	defer h.netMu.Unlock()
	st, err := h.loadNetState(ctx)
	if err != nil {
		return
	}
	var keep []*Lease
	for _, l := range st.cfg.Leases {
		if l.VM != vm {
			keep = append(keep, l)
		}
	}
	if len(keep) == len(st.cfg.Leases) && st.cfg.Rules[vm] == nil && st.cfg.Policies[vm] == nil {
		return
	}
	st.cfg.Leases = keep
	delete(st.cfg.Rules, vm)
	delete(st.cfg.Policies, vm)
	if h.saveNetConfig(st.cfg) == nil {
		h.syncServices(ctx, io.Discard, st)
	}
}
