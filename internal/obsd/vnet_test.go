package obsd

import (
	"context"
	"strings"
	"testing"
)

func TestSetInterfaces(t *testing.T) {
	src := `vm "a" {
	memory 1G
	interface { switch "x" }
	local interface
	interface tap7 {
		switch "y"
		lladdr fe:e1:ba:00:00:01
	}
	owner thorin
}
vm "b" { memory 1G interface { switch "x" } }
`
	f := ParseVMConf(src)
	got := f.VM("a").SetInterfaces([]VMIface{{Switch: "dmz", LLAddr: "fe:e1:ba:01:02:03"}})
	want := "vm \"a\" {\n\tmemory 1G\n\tlocal interface\n\towner thorin\n\tinterface { switch \"dmz\" lladdr fe:e1:ba:01:02:03 }\n}\n"
	if got != want {
		t.Errorf("Block a:\n%s\nerwartet:\n%s", got, want)
	}
	p := ParseVMConf(got).VM("a")
	if !p.Local || len(p.Interfaces) != 2 || p.Interfaces[1].Switch != "dmz" || p.Interfaces[1].LLAddr != "fe:e1:ba:01:02:03" {
		t.Errorf("neu geparst: %+v", p.Interfaces)
	}
	one := ParseVMConf(f.VM("b").SetInterfaces([]VMIface{{Switch: "z"}})).VM("b")
	if one == nil || len(one.Interfaces) != 1 || one.Interfaces[0].Switch != "z" || one.Memory != "1G" {
		t.Errorf("einzeiliger Block: %+v", one)
	}
}

func TestVNetDemoGenerated(t *testing.T) {
	h := newDemoHost(t)
	ctx := context.Background()
	o, err := h.VNet(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(o.PortGroups) != 1 || !o.PortGroups[0].Present || o.PortGroups[0].Subnet != "10.0.0.0/24" || !o.Status["pfAnchor"] || !o.Status["unboundInclude"] {
		t.Fatalf("Übersicht: %+v %v", o.PortGroups, o.Status)
	}
	if len(o.Warnings) != 0 {
		t.Errorf("Warnungen: %v", o.Warnings)
	}
	dhcp, _ := h.readFile(dhcpdPath)
	ub, _ := h.readFile(unboundPath)
	pf, _ := h.readFile(pfAnchorPath)
	for _, c := range []struct{ file, text string }{
		{dhcp, "hardware ethernet fe:e1:ba:d0:12:01;\n\t\tfixed-address 10.0.0.11;"},
		{dhcp, "range 10.0.0.100 10.0.0.199;"},
		{ub, `local-data: "web01.vm.ctw.lan. 300 IN A 10.0.0.11"`},
		{ub, "access-control: 10.0.0.0/24 allow"},
		{pf, "pass quick inet proto tcp from any to 10.0.0.11 port { 80 443 }"},
		{pf, "pass quick inet proto tcp from 10.0.0.11 to 10.0.0.12 port 5432"},
		{pf, "block log quick inet from 10.0.0.12 to any"},
		{pf, "pass on intern_vm"},
	} {
		if !strings.Contains(c.file, c.text) {
			t.Errorf("fehlt: %s", c.text)
		}
	}
	if strings.Contains(ub, "interface: 127.0.0.1") {
		t.Error("unbound.conf hat eigene interface-Zeilen – keine zusätzlichen erwartet")
	}
}

func TestPortGroupLifecycle(t *testing.T) {
	h := newDemoHost(t)
	ctx := context.Background()
	rt := func(task *Task, err error) { t.Helper(); runTask(t, task, err) }
	for _, bad := range []PortGroup{
		{Name: "x", Gateway: "10.0.0.129/25"}, // überschneidet intern
		{Name: "x", Uplink: "em0"},            // hat Adressen, ungetaggt
		{Name: "x", VLAN: 20},                 // VLAN ohne Uplink
		{Name: "x", DHCP: true},               // DHCP ohne Gateway
		{Name: "x", Gateway: "10.9.0.1/24", DHCP: true, RangeFrom: "10.9.0.1", RangeTo: "10.9.0.50"}, // Gateway im Bereich
		{Name: "intern"},
		{Name: "x", Descr: `a"b`},
	} {
		if _, err := h.SavePortGroup(ctx, "", &bad); err == nil {
			t.Errorf("%+v akzeptiert", bad)
		}
	}
	rt(h.SavePortGroup(ctx, "", &PortGroup{Name: "dmz", Descr: "Öffentlich", Uplink: "em1", VLAN: 20,
		Gateway: "10.20.0.1/24", NAT: true, DHCP: true, RangeFrom: "10.20.0.200", RangeTo: "10.20.0.250", DNS: true, Domain: "dmz.ctw.lan", Filter: true}))
	if _, err := h.SavePortGroup(ctx, "", &PortGroup{Name: "dmz2", Uplink: "em1", VLAN: 20}); err == nil {
		t.Error("doppeltes VLAN akzeptiert")
	}
	veb, _ := h.readFile("/etc/hostname.veb1")
	vlan, _ := h.readFile("/etc/hostname.vlan20")
	vport, _ := h.readFile("/etc/hostname.vport1")
	if !strings.Contains(veb, "add vport1\nadd vlan20\nlink1\nup") || !strings.Contains(vlan, "parent em1 vnetid 20") || !strings.HasPrefix(vport, "inet 10.20.0.1 255.255.255.0") {
		t.Fatalf("hostname-Dateien:\n%s\n%s\n%s", veb, vlan, vport)
	}
	o, _ := h.VNet(ctx)
	var dmz *PortGroupInfo
	for i := range o.PortGroups {
		if o.PortGroups[i].Name == "dmz" {
			dmz = &o.PortGroups[i]
		}
	}
	if dmz == nil || !dmz.Present || strings.Join(dmz.Members, " ") != "vport1 vlan20" {
		t.Fatalf("dmz: %+v", dmz)
	}
	if fl := h.outLenient(ctx, "rcctl", "get", "dhcpd", "flags"); !strings.Contains(fl, "vport0 vport1") {
		t.Errorf("dhcpd_flags: %q", fl)
	}

	// VM build: lokales Interface bleibt, neuer Anschluss an dmz mit automatischer Adresse.
	if _, err := h.SetVMNICs(ctx, "build", []NICSpec{{Switch: "dmz", Addr: "10.20.0.210"}}); err == nil {
		t.Error("Adresse im dynamischen Bereich akzeptiert")
	}
	if _, err := h.SetVMNICs(ctx, "build", []NICSpec{{Switch: "dmz", Addr: "10.0.0.5"}}); err == nil {
		t.Error("Adresse aus fremdem Netz akzeptiert")
	}
	if _, err := h.SetVMNICs(ctx, "build", []NICSpec{{Switch: "intern", Addr: "10.0.0.11"}}); err == nil {
		t.Error("doppelte Adresse akzeptiert")
	}
	if _, err := h.SetVMNICs(ctx, "build", []NICSpec{{Switch: "intern", Addr: "auto", Hostname: "web01"}}); err == nil {
		t.Error("doppelter DNS-Name akzeptiert")
	}
	rt(h.SetVMNICs(ctx, "build", []NICSpec{{Switch: "dmz", Addr: "auto"}}))
	conf, _ := h.loadVMConf()
	b := conf.VM("build")
	if !b.Local || len(b.Interfaces) != 2 || b.Interfaces[1].Switch != "dmz" || !reMAC.MatchString(b.Interfaces[1].LLAddr) {
		t.Fatalf("vm.conf build: %+v", b.Interfaces)
	}
	dhcp, _ := h.readFile(dhcpdPath)
	if !strings.Contains(dhcp, "fixed-address 10.20.0.10;") || !strings.Contains(dhcp, "hardware ethernet "+b.Interfaces[1].LLAddr) {
		t.Errorf("dhcpd.conf ohne build:\n%s", dhcp)
	}

	// Firewall
	if _, err := h.SetVMFirewall(ctx, "build", VMPolicy{In: "deny", Out: "allow"}, []*FWRule{{Dir: "in", Action: "pass", Proto: "tcp", Port: "99999"}}); err == nil {
		t.Error("Port 99999 akzeptiert")
	}
	if _, err := h.SetVMFirewall(ctx, "build", VMPolicy{In: "deny", Out: "allow"}, []*FWRule{{Dir: "in", Action: "pass", Proto: "tcp", Peer: "any; pass all"}}); err == nil {
		t.Error("Gegenstelle mit Einschleusung akzeptiert")
	}
	if _, err := h.SetVMFirewall(ctx, "build", VMPolicy{In: "deny", Out: "allow"}, []*FWRule{{Dir: "in", Action: "pass", Proto: "tcp", Peer: "<lan>", Port: "8000:8080", Descr: "Dev"}}); err != nil {
		t.Fatal(err)
	}
	pf, _ := h.readFile(pfAnchorPath)
	if !strings.Contains(pf, `pass quick inet proto tcp from <lan> to 10.20.0.10 port 8000:8080 label "Dev"`) || !strings.Contains(pf, "block log quick inet from any to 10.20.0.10") {
		t.Errorf("pf-Anker:\n%s", pf)
	}

	// Löschen erst, wenn keine VM mehr dranhängt.
	if _, err := h.DeletePortGroup(ctx, "dmz"); err == nil {
		t.Error("benutzte Portgruppe gelöscht")
	}
	rt(h.SetVMNICs(ctx, "build", nil))
	rt(h.DeletePortGroup(ctx, "dmz"))
	if h.exists("/etc/hostname.veb1") || h.exists("/etc/hostname.vlan20") {
		t.Error("hostname-Dateien nicht entfernt")
	}
	conf, _ = h.loadVMConf()
	if conf.Switch("dmz") != nil {
		t.Error("Switch noch in vm.conf")
	}
	if pf, _ := h.readFile(pfAnchorPath); strings.Contains(pf, "10.20.0") {
		t.Errorf("Regeln für entfernte Adressen:\n%s", pf)
	}
	if fl := h.outLenient(ctx, "rcctl", "get", "dhcpd", "flags"); strings.Contains(fl, "vport1") {
		t.Errorf("dhcpd_flags nach Löschen: %q", fl)
	}
}

func TestAdoptSwitch(t *testing.T) {
	h := newDemoHost(t)
	pg := h.adoptSwitch(&SwitchConf{Name: "intern", Interface: "veb0"})
	if pg.Vport != "vport0" || pg.Gateway != "10.0.0.1/24" || !pg.Filter || pg.Uplink != "" || pg.Descr != "VM-Switch intern" {
		t.Errorf("übernommen: %+v", pg)
	}
}
