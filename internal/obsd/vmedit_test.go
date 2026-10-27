package obsd

import (
	"context"
	"strings"
	"testing"
)

func TestApplyEditKeepsEverythingElse(t *testing.T) {
	src := `vm "a" {
	memory 1G
	cpus 2
	enable
	boot "/bsd.rd"
	disk "/var/vmd/a.qcow2"
	disk "/var/vmd/b.raw" format raw
	cdrom "/var/vmd/iso/x.iso"
	boot device cdrom
	interfaces 2
	interface { switch "intern" lladdr fe:e1:ba:00:00:01 }
	interface tap5 {
		switch "dmz"
	}
	local interface
	allow instance { memory 2G }
	owner thorin:staff
}
`
	v := ParseVMConf(src).VM("a")
	e := v.Edit()
	if e.Memory != "1G" || e.CPUs != 2 || len(e.Disks) != 2 || e.BootDevice != "cdrom" || e.Owner != "thorin:staff" || !e.Local {
		t.Fatalf("Edit: %+v", e)
	}
	// Unverändert zurückschreiben: gleiche Bedeutung, alles Fremde erhalten.
	same := v.ApplyEdit(e, map[string]string{"/var/vmd/a.qcow2": "qcow2", "/var/vmd/b.raw": "raw"})
	p := ParseVMConf(same).VM("a")
	if p == nil || p.Edit().Memory != e.Memory || len(p.Interfaces) != len(v.Interfaces) || !p.Local {
		t.Fatalf("Rundlauf:\n%s", same)
	}
	for _, keep := range []string{`boot "/bsd.rd"`, "interfaces 2", `lladdr fe:e1:ba:00:00:01`, "interface tap5", "allow instance { memory 2G }", `disk "/var/vmd/a.qcow2" format qcow2`} {
		if !strings.Contains(same, keep) {
			t.Errorf("%q fehlt:\n%s", keep, same)
		}
	}
	// Änderungen: Speicher, CPUs, SEV, eine Disk weniger, lokales Interface aus, Boot von Disk.
	e2 := e
	e2.Memory, e2.CPUs, e2.SEV, e2.Disks, e2.Local, e2.BootDevice, e2.CDROM, e2.Enabled, e2.Owner = "4G", 8, "seves", []string{"/var/vmd/a.qcow2"}, false, "disk", "", false, ""
	out := v.ApplyEdit(e2, map[string]string{"/var/vmd/a.qcow2": "qcow2"})
	q := ParseVMConf(out).VM("a")
	if q == nil {
		t.Fatalf("nicht parsbar:\n%s", out)
	}
	got := q.Edit()
	if got.Memory != "4G" || got.CPUs != 8 || got.SEV != "seves" || len(got.Disks) != 1 || got.Local || got.BootDevice != "disk" || got.CDROM != "" || got.Enabled || got.Owner != "" {
		t.Errorf("Ergebnis: %+v\n%s", got, out)
	}
	for _, gone := range []string{"b.raw", "x.iso", "local interface", "owner", "memory 1G", "cpus 2", "device cdrom"} {
		if strings.Contains(out, gone) {
			t.Errorf("%q noch vorhanden:\n%s", gone, out)
		}
	}
	for _, keep := range []string{`boot "/bsd.rd"`, "interfaces 2", "interface tap5", "allow instance"} {
		if !strings.Contains(out, keep) {
			t.Errorf("%q verloren:\n%s", keep, out)
		}
	}
	// Einzeilige Blöcke.
	one := ParseVMConf(`vm "b" { memory 1G interface { switch "x" } }` + "\n").VM("b")
	o := one.ApplyEdit(VMEdit{Memory: "2G", CPUs: 1, Enabled: true, Disks: []string{}}, nil)
	if p := ParseVMConf(o).VM("b"); p == nil || p.Memory != "2G" || len(p.Interfaces) != 1 {
		t.Errorf("einzeilig:\n%s", o)
	}
}

func TestVMOptionsAndSettings(t *testing.T) {
	h := newDemoHost(t)
	ctx := context.Background()
	o, err := h.VMOptions(ctx, "db01")
	if err != nil {
		t.Fatal(err)
	}
	if o.Current.Memory != "2G" || o.Current.CPUs != 4 || !o.Running || o.HostCPUs != 8 || !o.SEV || len(o.Users) == 0 || len(o.Boot) != 4 {
		t.Fatalf("Optionen: %+v", o)
	}
	for _, i := range o.Images {
		if i.Name == "web01.qcow2" || i.Name == "build.qcow2" {
			t.Errorf("fremde Disk als Auswahl: %s", i.Name)
		}
	}
	if _, err := h.VMOptions(ctx, "gibtsnicht"); err == nil {
		t.Error("unbekannte VM")
	}
	if n, err := h.VMOptions(ctx, ""); err != nil || n.Current.Memory != "1G" {
		t.Errorf("neue VM: %v %+v", err, n)
	}

	e := o.Current
	e.Memory, e.CPUs = "4G", 6
	if err := h.UpdateVMSettings(ctx, "db01", &e); err != nil {
		t.Fatal(err)
	}
	conf, _ := h.loadVMConf()
	v := conf.VM("db01")
	if v.Memory != "4G" || v.CPUs != 6 || len(v.Interfaces) != 1 || v.Interfaces[0].LLAddr != "fe:e1:ba:d0:12:02" || !strings.Contains(v.Raw, `format qcow2`) {
		t.Errorf("db01 nach Speichern:\n%s", v.Raw)
	}
	// Ablehnungen: nichts wird geschrieben.
	before, _ := h.readFile("/etc/vm.conf")
	bad := []VMEdit{
		{Memory: "4G; rm", CPUs: 1, Enabled: true},
		{Memory: "4T", CPUs: 1, Enabled: true},
		{Memory: "512G", CPUs: 1, Enabled: true},
		{Memory: "1G", CPUs: 65, Enabled: true},
		{Memory: "1G", CPUs: 1, SEV: "evil", Enabled: true},
		{Memory: "1G", CPUs: 1, Enabled: true, Disks: []string{"/var/vmd/web01.qcow2"}},      // gehört web01
		{Memory: "1G", CPUs: 1, Enabled: true, Disks: []string{"/etc/passwd"}},               // außerhalb
		{Memory: "1G", CPUs: 1, Enabled: true, Disks: []string{"/var/vmd/gibtsnicht.qcow2"}}, // fehlt
		{Memory: "1G", CPUs: 1, Enabled: true, Disks: []string{"/var/vmd/db01.qcow2", "/var/vmd/db01.qcow2"}},
		{Memory: "1G", CPUs: 1, Enabled: true, BootDevice: "cdrom"}, // kein Medium
		{Memory: "1G", CPUs: 1, Enabled: true, BootDevice: "usb"},
		{Memory: "1G", CPUs: 1, Enabled: true, Owner: "gibtsnicht"},
		{Memory: "1G", CPUs: 1, Enabled: true, Owner: "thorin:gibtsnicht"},
		{Memory: "1G", CPUs: 1, Enabled: true, Owner: "a b"},
		{Memory: "1G", CPUs: 1, Enabled: true, CDROM: "/var/vmd/iso/gibtsnicht.iso"},
	}
	for i, b := range bad {
		if err := h.UpdateVMSettings(ctx, "db01", &b); err == nil {
			t.Errorf("Fall %d akzeptiert: %+v", i, b)
		}
	}
	if after, _ := h.readFile("/etc/vm.conf"); after != before {
		t.Error("vm.conf wurde trotz abgelehnter Eingaben verändert")
	}
	// Besitzer und Boot von der ISO der VM build.
	b, _ := h.VMOptions(ctx, "build")
	be := b.Current
	be.Owner, be.Memory = "thorin", "8G"
	if err := h.UpdateVMSettings(ctx, "build", &be); err != nil {
		t.Fatal(err)
	}
	conf, _ = h.loadVMConf()
	if v := conf.VM("build"); v.Owner != "thorin" || v.Memory != "8G" || !v.Local || v.CDROM == "" {
		t.Errorf("build:\n%s", v.Raw)
	}
}
