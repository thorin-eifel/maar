package obsd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleVMConf = `# Kommentar mit vm "falle" {
switch "uplink" {
	interface veb0
}

vm "web" {
	memory 1G
	cpus 8
	seves
	enable
	disk "/var/vmd/web.qcow2" format qcow2
	interface { switch "uplink" ; lladdr fe:e1:ba:d0:00:01 }
}

vm "install" {
	memory 512M
	disable
	cdrom "/var/vmd/iso/install80.iso"
	boot device cdrom
	local interface
	owner thorin:staff
}
`

func TestParseVMConf(t *testing.T) {
	f := ParseVMConf(sampleVMConf)
	if len(f.VMs) != 2 || len(f.Switches) != 1 {
		t.Fatalf("VMs=%d Switches=%d", len(f.VMs), len(f.Switches))
	}
	web := f.VM("web")
	if web.Memory != "1G" || !web.Enabled || len(web.Disks) != 1 || web.Interfaces[0].Switch != "uplink" || web.Interfaces[0].LLAddr != "fe:e1:ba:d0:00:01" {
		t.Errorf("web falsch geparst: %+v", web)
	}
	if web.CPUs != 8 || web.SEV != "seves" || web.Disks[0] != "/var/vmd/web.qcow2" {
		t.Errorf("OpenBSD-8.0-Felder falsch: cpus=%d sev=%q disks=%v", web.CPUs, web.SEV, web.Disks)
	}
	inst := f.VM("install")
	if inst.Enabled || inst.CPUs != 1 || inst.Boot != "device cdrom" || !inst.Local || inst.Owner != "thorin:staff" || inst.CDROM == "" {
		t.Errorf("install falsch geparst: %+v", inst)
	}
	if f.Switches[0].Interface != "veb0" {
		t.Errorf("switch: %+v", f.Switches[0])
	}
}

func TestReplaceAndRemoveVM(t *testing.T) {
	f := ParseVMConf(sampleVMConf)
	out, err := f.ReplaceVM("web", "vm \"web\" {\n\tmemory 2G\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	g := ParseVMConf(out)
	if g.VM("web").Memory != "2G" || g.VM("install") == nil || len(g.Switches) != 1 {
		t.Errorf("Ersetzen beschädigt die Datei:\n%s", out)
	}
	out, _ = f.ReplaceVM("install", "")
	g = ParseVMConf(out)
	if g.VM("install") != nil || g.VM("web") == nil || strings.Contains(out, "owner thorin") {
		t.Errorf("Entfernen fehlerhaft:\n%s", out)
	}
}

func TestVMSpecBlockRoundTrip(t *testing.T) {
	s := &VMSpec{Name: "neu", Memory: "2G", CPUs: 4, SEV: "sev", DiskPath: "/var/vmd/neu.qcow2", CDROM: "/var/vmd/iso/x.iso", BootCD: true, Switches: []string{"uplink"}, Enabled: true}
	f := ParseVMConf(s.Block())
	v := f.VM("neu")
	if v == nil || v.Memory != "2G" || v.CPUs != 4 || v.SEV != "sev" || v.Boot != "device cdrom" || v.Interfaces[0].Switch != "uplink" || v.Disks[0] != s.DiskPath {
		t.Fatalf("Rundreise fehlgeschlagen: %+v\n%s", v, s.Block())
	}
}

func TestParseIfconfig(t *testing.T) {
	ifs := ParseIfconfig(demoIfconfig)
	byName := map[string]*Iface{}
	for _, i := range ifs {
		byName[i.Name] = i
	}
	em0 := byName["em0"]
	if em0 == nil || !em0.Up || em0.Descr != "Uplink" || em0.Groups[0] != "egress" || em0.Addrs[0].Netmask != "255.255.255.0" {
		t.Fatalf("em0: %+v", em0)
	}
	if em1 := byName["em1"]; em1.Up || em1.Status != "no carrier" {
		t.Errorf("em1: %+v", em1)
	}
	if veb := byName["veb0"]; len(veb.Members) != 3 || veb.Members[0] != "vport0" {
		t.Errorf("veb0 members: %v", veb.Members)
	}
	if enc := byName["enc0"]; enc == nil || enc.Up {
		t.Errorf("enc0: %+v", enc)
	}
}

func TestHexMaskAndSize(t *testing.T) {
	if m := hexMask("0xffff0000"); m != "255.255.0.0" {
		t.Errorf("hexMask: %s", m)
	}
	for in, want := range map[string]int64{"412M": 412 << 20, "1.5G": 3 << 29, "2047K": 2047 << 10, "0K": 0, "-": 0} {
		if got := parseSize(in); got != want {
			t.Errorf("parseSize(%q) = %d, erwartet %d", in, got, want)
		}
	}
}

func newDemoHost(t *testing.T) *Host {
	t.Helper()
	d, root, err := NewDemo()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	return NewHost(d, root, true)
}

func TestOverviewAndVMs(t *testing.T) {
	h := newDemoHost(t)
	ctx := context.Background()
	o, err := h.Overview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if o.Hostname != "nordlicht.ctw.lan" || o.NCPU != 8 || o.MemTotal == 0 || o.Uptime <= 0 || len(o.Load) != 3 {
		t.Errorf("Overview: %+v", o)
	}
	ov, err := h.VMs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ov.VMs) != 3 || !ov.VMDRun {
		t.Fatalf("VMs: %d, vmd=%v", len(ov.VMs), ov.VMDRun)
	}
	tty, err := h.VMTTY(ctx, "web01")
	if err != nil || tty != "/dev/ttyp0" {
		t.Errorf("VMTTY = %q, %v", tty, err)
	}
	if _, err := h.VMTTY(ctx, "build"); !errors.Is(err, ErrInvalid) {
		t.Errorf("gestoppte VM sollte kein TTY liefern: %v", err)
	}
}

func TestCreateVMValidation(t *testing.T) {
	h := newDemoHost(t)
	ctx := context.Background()
	bad := []*VMSpec{
		{Name: "../etc", Memory: "1G"},
		{Name: "ok", Memory: "1G; rm"},
		{Name: "ok", Memory: "1G", CDROM: "/etc/master.passwd"},
		{Name: "ok", Memory: "1G", DiskPath: "/var/vmd/../../etc/x"},
		{Name: "web01", Memory: "1G"},
		{Name: "ok", Memory: "1G", CPUs: 65},
		{Name: "ok", Memory: "1G", SEV: "tdx"},
		{Name: "ok", Memory: "1G", Owner: "thorin:staff:x"},
		{Name: "ok", Memory: "1G", Owner: "root;id"},
	}
	for _, s := range bad {
		if err := h.CreateVM(ctx, s); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: erwartet ErrInvalid, bekam %v", s, err)
		}
	}
	if err := h.CreateVM(ctx, &VMSpec{Name: "neu", Memory: "1G", CPUs: 16, Owner: ":staff", DiskSize: "10G", Switches: []string{"intern"}, Enabled: true, StartNow: true}); err != nil {
		t.Fatal(err)
	}
	conf, _ := h.loadVMConf()
	if v := conf.VM("neu"); v == nil || v.Disks[0] != "/var/vmd/neu.qcow2" || v.CPUs != 16 || v.Owner != ":staff" {
		t.Errorf("neue VM fehlt in vm.conf: %+v", v)
	}
	if _, err := os.Stat(h.P("/var/vmd/neu.qcow2")); err != nil {
		t.Errorf("Disk wurde nicht angelegt: %v", err)
	}
	ov, _ := h.VMs(ctx)
	for _, v := range ov.VMs {
		if v.Name == "neu" && (v.State != "running" || v.VCPUs != 16) {
			t.Errorf("gestartete VM: state=%s vcpus=%d", v.State, v.VCPUs)
		}
	}
}

func TestWriteConfigCheckBackupConflict(t *testing.T) {
	h := newDemoHost(t)
	ctx := context.Background()
	cf, err := h.ReadConfig("/etc/pf.conf")
	if err != nil {
		t.Fatal(err)
	}
	// Syntaxfehler: Datei bleibt unverändert.
	_, err = h.WriteConfig(ctx, "/etc/pf.conf", cf.Content+"SYNTAXFEHLER\n", cf.MTime)
	var ce *CheckError
	if !errors.As(err, &ce) || !strings.Contains(ce.Output, "/etc/pf.conf") {
		t.Fatalf("erwartet CheckError mit Originalpfad, bekam %v", err)
	}
	if now, _ := h.ReadConfig("/etc/pf.conf"); now.Content != cf.Content {
		t.Fatal("Datei wurde trotz Fehler verändert")
	}
	// Gültig: Sicherung entsteht.
	res, err := h.WriteConfig(ctx, "/etc/pf.conf", cf.Content+"# neu\n", cf.MTime)
	if err != nil || res.Backup == "" {
		t.Fatalf("speichern: %v %+v", err, res)
	}
	if b, _ := h.Backups("/etc/pf.conf"); len(b) != 1 {
		t.Errorf("erwartet 1 Sicherung, gefunden %d", len(b))
	}
	// Veraltete mtime: Konflikt.
	if _, err := h.WriteConfig(ctx, "/etc/pf.conf", "x\n", cf.MTime); !errors.Is(err, ErrConflict) {
		t.Errorf("erwartet ErrConflict, bekam %v", err)
	}
	// Nicht freigegebene Pfade.
	for _, p := range []string{"/etc/master.passwd", "/etc/hostname.em0/../../master.passwd", "/etc/pf.conf.bak"} {
		if _, err := h.WriteConfig(ctx, p, "x", 0); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: erwartet ErrInvalid, bekam %v", p, err)
		}
	}
}

func TestLogPathAndTail(t *testing.T) {
	h := newDemoHost(t)
	for _, bad := range []string{"../etc/master.passwd", "/var/log/messages", "httpd/../../../etc/passwd", "a/b/c", "pflog"} {
		if _, err := h.LogPath(bad); err == nil {
			t.Errorf("%q sollte abgelehnt werden", bad)
		}
	}
	p, err := h.LogPath("messages")
	if err != nil {
		t.Fatal(err)
	}
	l, size, err := TailFile(p, 10)
	if err != nil || len(l) != 10 || size == 0 {
		t.Errorf("TailFile: %d Zeilen, size=%d, err=%v", len(l), size, err)
	}
	empty := filepath.Join(t.TempDir(), "leer")
	os.WriteFile(empty, nil, 0o644)
	if l, _, _ := TailFile(empty, 10); len(l) != 0 {
		t.Errorf("leere Datei: %v", l)
	}
}

func TestServicesAndUsers(t *testing.T) {
	h := newDemoHost(t)
	ctx := context.Background()
	svc, err := h.Services(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range svc {
		if s.Name == "httpd" {
			found = s.Enabled && s.Running
		}
		if s.Name == "pf" && !s.Special {
			t.Error("pf sollte als Spezialvariable gelten")
		}
	}
	if !found {
		t.Error("httpd sollte aktiviert und gestartet sein")
	}
	if _, err := h.ServiceAction(ctx, "httpd; reboot", "start"); !errors.Is(err, ErrInvalid) {
		t.Errorf("Dienstname nicht validiert: %v", err)
	}
	if !h.InGroup("thorin", "wheel") || h.InGroup("mara", "wheel") {
		t.Error("InGroup falsch")
	}
	if err := h.CreateUser(ctx, &UserSpec{Name: "neu", Shell: "/bin/bash -c id"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("Shell nicht validiert: %v", err)
	}
}
