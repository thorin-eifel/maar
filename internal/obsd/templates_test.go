package obsd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func runTask(t *testing.T, task *Task, err error) string {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := task.Run(context.Background(), &out); err != nil {
		t.Fatalf("%s: %v\n%s", task.Title, err, out.String())
	}
	return out.String()
}

func TestTemplatesAndLinkedClones(t *testing.T) {
	h := newDemoHost(t)
	ctx := context.Background()
	list, err := h.Templates()
	if err != nil || len(list) != 2 {
		t.Fatalf("Templates: %v %d", err, len(list))
	}
	var base *Template
	for _, x := range list {
		if x.Name == "openbsd80-base" {
			base = x
		}
	}
	if base == nil || len(base.Clones) != 1 || base.Clones[0] != "web01" {
		t.Fatalf("web01 sollte als Klon von openbsd80-base erkannt werden: %+v", base)
	}
	if err := h.DeleteTemplate("openbsd80-base"); !errors.Is(err, ErrInvalid) {
		t.Errorf("Template mit Klonen darf nicht gelöscht werden: %v", err)
	}

	// Verknüpfter Klon mit Gastanpassung.
	task, err := h.Deploy(ctx, "template", "openbsd80-base", &DeploySpec{
		Name: "app01", Memory: "2G", CPUs: 2, Linked: true, Switches: []string{"intern"}, Enabled: true, StartNow: true,
		Guest: &GuestConfig{Hostname: "app01.ctw.lan", Network: "static", Address: "10.0.0.31/24", Gateway: "10.0.0.1", DNS: []string{"10.0.0.1"}, User: "ops", Password: "geheim123", SSHKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMq thorin@mac"},
	})
	out := runTask(t, task, err)
	if !strings.Contains(out, "Fertig") {
		t.Errorf("Ausgabe: %s", out)
	}
	conf, _ := h.loadVMConf()
	v := conf.VM("app01")
	if v == nil || v.CPUs != 2 || v.CDROM != "/var/vmd/seed/app01-cidata.iso" || v.Disks[0] != "/var/vmd/app01.qcow2" {
		t.Fatalf("app01 in vm.conf: %+v", v)
	}
	if b := qcow2Backing(h.P("/var/vmd/app01.qcow2")); b != base.Disk {
		t.Errorf("Overlay zeigt auf %q", b)
	}
	if list, _ := h.Templates(); len(list[1].Clones)+len(list[0].Clones) != 2 {
		t.Errorf("Klon-Zählung nach Bereitstellung falsch")
	}

	// Klonen einer laufenden VM wird abgelehnt, einer gestoppten als Vollkopie erlaubt.
	if _, err := h.Deploy(ctx, "vm", "app01", &DeploySpec{Name: "app02", Memory: "1G", Linked: true}); !errors.Is(err, ErrInvalid) {
		t.Errorf("laufende VM sollte nicht klonbar sein: %v", err)
	}
	task, err = h.Deploy(ctx, "vm", "build", &DeploySpec{Name: "build2", Memory: "1G", Linked: true})
	runTask(t, task, err)
	if b := qcow2Backing(h.P("/var/vmd/build2.qcow2")); b != "" {
		t.Errorf("VM-Klon muss eine Vollkopie sein, hat Basis %q", b)
	}

	// Löschen der VM entfernt auch die Seed-ISO.
	h.R.Run(ctx, nil, "vmctl", "stop", "app01")
	if err := h.DeleteVM(ctx, "app01", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(h.P("/var/vmd/seed/app01-cidata.iso")); !os.IsNotExist(err) {
		t.Error("Seed-ISO wurde nicht entfernt")
	}
}

func TestTemplateFromVMAndImport(t *testing.T) {
	h := newDemoHost(t)
	ctx := context.Background()
	if _, err := h.TemplateFromVM(ctx, "web01", &TemplateSpec{Name: "x"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("laufende VM: %v", err)
	}
	task, err := h.TemplateFromVM(ctx, "build", &TemplateSpec{Name: "build-gold", OS: "openbsd", Description: "Buildhost"})
	runTask(t, task, err)
	st, err := os.Stat(h.P("/var/vmd/templates/build-gold.qcow2"))
	if err != nil || st.Mode().Perm() != 0o444 {
		t.Errorf("Template-Image fehlt oder ist nicht schreibgeschützt: %v %v", err, st)
	}
	if _, err := h.TemplateFromVM(ctx, "build", &TemplateSpec{Name: "build-gold"}); !errors.Is(err, ErrInvalid) {
		t.Error("doppelter Template-Name wurde akzeptiert")
	}
	for _, bad := range []*TemplateSpec{
		{Name: "a", URL: "file:///etc/master.passwd"},
		{Name: "a", Path: "/etc/master.passwd"},
		{Name: "a"},
		{Name: "a/b", URL: "https://x.example/a.qcow2"},
		{Name: "a", OS: "windows", URL: "https://x.example/a.qcow2"},
	} {
		if _, err := h.ImportTemplate(ctx, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: erwartet ErrInvalid, bekam %v", bad, err)
		}
	}
	task, err = h.ImportTemplate(ctx, &TemplateSpec{Name: "alpine", OS: "linux", URL: "https://dl-cdn.alpinelinux.org/alpine/v3.22/releases/cloud/nocloud_alpine.qcow2"})
	runTask(t, task, err)
	if tp, err := h.template("alpine"); err != nil || tp.OS != "linux" || tp.Memory != "1G" {
		t.Errorf("Import: %+v %v", tp, err)
	}
}

func TestSeedContents(t *testing.T) {
	h := newDemoHost(t)
	ctx := context.Background()
	linux, err := h.buildSeed(ctx, "web", &GuestConfig{OS: "linux", Hostname: "web.ctw.lan", Network: "static", Address: "10.0.0.5/24", Gateway: "10.0.0.1", DNS: []string{"9.9.9.9"}, User: "ops", Password: `pa"ss: word`, SSHKey: "ssh-ed25519 AAAAC3Nz key"})
	if err != nil {
		t.Fatal(err)
	}
	ud := linux.files["user-data"]
	if !strings.HasPrefix(ud, "#cloud-config\n") || !strings.Contains(ud, `"pa\"ss: word"`) || !strings.Contains(ud, `- "ssh-ed25519 AAAAC3Nz key"`) {
		t.Errorf("user-data:\n%s", ud)
	}
	if nc := linux.files["network-config"]; !strings.Contains(nc, `addresses: ["10.0.0.5/24"]`) || !strings.Contains(nc, `via: "10.0.0.1"`) {
		t.Errorf("network-config:\n%s", nc)
	}
	if _, ok := linux.files["maar.cfg"]; ok {
		t.Error("Linux-Seed braucht kein maar.cfg")
	}
	obsd, err := h.buildSeed(ctx, "fw", &GuestConfig{OS: "openbsd", Hostname: "fw", Password: "geheim123"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := obsd.files["maar.cfg"]
	if !strings.Contains(cfg, "pwhash $2b$") || strings.Contains(strings.Join(mapValues(obsd.files), ""), "geheim123") {
		t.Errorf("OpenBSD-Seed darf nur den Hash enthalten:\n%s", cfg)
	}
	if _, ok := obsd.files["user-data"]; ok {
		t.Error("OpenBSD-Seed sollte kein cloud-init user-data enthalten")
	}
	for _, bad := range []*GuestConfig{
		{OS: "linux", Hostname: "-x"},
		{OS: "linux", Hostname: "a b"},
		{OS: "linux", Hostname: "ok", Network: "static", Address: "10.0.0.5"},
		{OS: "linux", Hostname: "ok", SSHKey: "ssh-ed25519 AAA\nevil"},
		{OS: "linux", Hostname: "ok", User: "Root!", Password: "geheim123"},
		{OS: "linux", Hostname: "ok", DNS: []string{"1.1.1.1; rm"}},
		{OS: "linux", Hostname: "ok", User: "ops"},
	} {
		if _, err := h.buildSeed(ctx, "v", bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: erwartet ErrInvalid, bekam %v", bad, err)
		}
	}
}

func mapValues(m map[string]string) []string {
	var v []string
	for _, x := range m {
		v = append(v, x)
	}
	return v
}
