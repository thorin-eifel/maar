package obsd

import (
	"context"
	"strings"
	"testing"
)

func TestParseDmesg(t *testing.T) {
	d := parseDmesg(demoDmesg)
	if len(d.hbas) != 2 || d.hbas[0].Model != "QLogic ISP2532" || d.wwpn["qla0"] != "21000024ff4c6a52" {
		t.Errorf("HBAs: %+v %v", d.hbas, d.wwpn)
	}
	cases := map[string]struct {
		transport, wwn string
		size           int64
	}{
		"sd0": {"local", "", 0},
		"sd2": {"iscsi", "naa.60014050f3a1c25e8f0b4d4a9e1c7b02", 1048576000 * 512},
		"sd4": {"fc", "naa.600a098038314c4c6b4a5a4d6e575a31", 4294967296 * 512},
	}
	for n, want := range cases {
		dk := d.disks[n]
		if dk == nil || transportOf(d.buses[dk.Bus]) != want.transport || dk.WWN != want.wwn || (want.size > 0 && dk.Size != want.size) {
			t.Errorf("%s: %+v", n, dk)
		}
	}
	if len(d.nvme) != 1 || !strings.Contains(d.nvme[0], "Samsung") {
		t.Errorf("nvme: %v", d.nvme)
	}
}

func TestISCSIValidateAndConf(t *testing.T) {
	good := &ISCSITarget{Name: "san1", Addr: "10.1.2.3", Port: 3261, IQN: "iqn.2003-01.org.example:vol1", Initiator: "iqn.2026-10.lan.ctw:nordlicht", Enabled: true}
	if err := good.validate(); err != nil {
		t.Fatal(err)
	}
	want := "target san1 {\n\tinitiatorname \"iqn.2026-10.lan.ctw:nordlicht\"\n\ttargetaddr 10.1.2.3 port 3261\n\ttargetname \"iqn.2003-01.org.example:vol1\"\n}\n"
	if good.block() != want {
		t.Errorf("Block:\n%s", good.block())
	}
	off := &ISCSITarget{Name: "x", Addr: "san.ctw.lan", Discovery: true, IQN: "ignoriert"}
	if err := off.validate(); err != nil || off.IQN != "" || !strings.Contains(off.block(), "disabled") || !strings.Contains(off.block(), "discovery") {
		t.Errorf("Discovery: %v %s", err, off.block())
	}
	for _, bad := range []ISCSITarget{
		{Name: "a b", Addr: "10.0.0.1", IQN: "iqn.2003-01.org.x:y"},
		{Name: "a.b", Addr: "10.0.0.1", IQN: "iqn.2003-01.org.x:y"},
		{Name: "x", Addr: "10.0.0.1; rm -rf", IQN: "iqn.2003-01.org.x:y"},
		{Name: "x", Addr: "10.0.0.1", IQN: "kein-iqn"},
		{Name: "x", Addr: "10.0.0.1", IQN: "iqn.2003-01.org.x:y\"\n}"},
		{Name: "x", Addr: "10.0.0.1", IQN: "iqn.2003-01.org.x:y", Port: 70000},
		{Name: "x", Addr: "10.0.0.1", IQN: "iqn.2003-01.org.x:y", Initiator: "nope"},
	} {
		if bad.validate() == nil {
			t.Errorf("akzeptiert: %+v", bad)
		}
	}
	// Von Hand eingetragener Text außerhalb des Bereichs bleibt erhalten.
	cur := "target hand {\n\ttargetaddr 1.1.1.1\n}\n"
	out := iscsiConf(cur, []*ISCSITarget{good})
	if !strings.HasPrefix(out, cur) || strings.Count(out, iscsiBegin) != 1 || !strings.Contains(out, "target san1") {
		t.Errorf("Merge:\n%s", out)
	}
	if again := iscsiConf(out, nil); strings.Contains(again, "san1") || !strings.HasPrefix(again, cur) {
		t.Errorf("Entfernen:\n%s", again)
	}
}

func TestSANOverviewAndDatastoreLifecycle(t *testing.T) {
	h := newDemoHost(t)
	ctx := context.Background()
	o, err := h.SAN(ctx)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]SANDisk{}
	for _, d := range o.Disks {
		by[d.Name] = d
	}
	if len(o.Disks) != 6 || len(o.HBAs) != 2 || len(o.HBAs[0].Disks) != 1 {
		t.Fatalf("Übersicht: %d Disks, %+v", len(o.Disks), o.HBAs)
	}
	if by["sd0"].CanInit || by["sd0"].Transport != "local" {
		t.Errorf("lokale Disk initialisierbar: %+v", by["sd0"])
	}
	if d := by["sd2"]; d.CanInit || d.Datastore != "vm-pool" || len(d.Mounts) != 1 {
		t.Errorf("sd2: %+v", d)
	}
	if !by["sd3"].CanInit || by["sd3"].Labeled {
		t.Errorf("sd3: %+v", by["sd3"])
	}
	if len(o.Targets) != 1 || o.Targets[0].State != "angemeldet" || !o.ISCSI.Running {
		t.Errorf("Ziele: %+v %+v", o.Targets, o.ISCSI)
	}
	if o.NVMe.Supported || !strings.Contains(o.NVMe.Note, "NVMe-over-Fabrics") || len(o.NVMe.Local) != 1 {
		t.Errorf("NVMe: %+v", o.NVMe)
	}
	if len(o.Datastores) != 1 || !o.Datastores[0].Mounted || !o.Datastores[0].Present {
		t.Errorf("Datastores: %+v", o.Datastores)
	}

	// Schutz: lokale, eingehängte und unbekannte Disks werden nicht angefasst.
	for _, disk := range []string{"sd0", "sd1", "sd2", "sd9", "../sd3", "sd3;x"} {
		if _, err := h.InitDatastore(ctx, disk, "neu"); err == nil {
			t.Errorf("Initialisierung von %q erlaubt", disk)
		}
	}
	for _, name := range []string{"", "Gross", "iso", "templates", "vm-pool", "a/b", "-x"} {
		if _, err := h.InitDatastore(ctx, "sd3", name); err == nil {
			t.Errorf("Datastore-Name %q erlaubt", name)
		}
	}

	// Anlegen, in der VM-Übersicht und für Disks nutzbar, aushängen, entfernen.
	task, err := h.InitDatastore(ctx, "sd3", "scratch")
	runTask(t, task, err)
	o, _ = h.SAN(ctx)
	var ds *DatastoreInfo
	for i := range o.Datastores {
		if o.Datastores[i].Name == "scratch" {
			ds = &o.Datastores[i]
		}
	}
	if ds == nil || !ds.Mounted || ds.Mount != "/var/vmd/scratch" || len(ds.DUID) != 16 {
		t.Fatalf("scratch: %+v", ds)
	}
	fs, _ := h.readFile("/etc/fstab")
	if !strings.Contains(fs, ds.DUID+".a /var/vmd/scratch ffs rw,noauto") || !strings.Contains(fs, "# maar datastore scratch") {
		t.Errorf("fstab:\n%s", fs)
	}
	if err := h.CreateDisk(ctx, "test", "1G", "qcow2", "", "scratch"); err != nil {
		t.Fatal(err)
	}
	vms, _ := h.VMs(ctx)
	found := false
	for _, i := range vms.Images {
		found = found || i.Path == "/var/vmd/scratch/test.qcow2"
	}
	if !found || len(vms.Datastores) != 2 {
		t.Errorf("Images/Datastores: %v %v", vms.Images, vms.Datastores)
	}
	if err := h.CreateDisk(ctx, "x", "1G", "qcow2", "", "gibtsnicht"); err == nil {
		t.Error("unbekannter Datastore akzeptiert")
	}
	if err := h.UnmountDatastore(ctx, "scratch"); err != nil {
		t.Fatal(err)
	}
	if err := h.CreateDisk(ctx, "y", "1G", "qcow2", "", "scratch"); err == nil {
		t.Error("Disk auf ausgehängtem Datastore angelegt")
	}
	if err := h.MountDatastore(ctx, "scratch"); err != nil {
		t.Fatal(err)
	}
	if err := h.MountDatastore(ctx, "scratch"); err == nil {
		t.Error("doppeltes Einhängen")
	}
	if err := h.RemoveDatastore(ctx, "scratch"); err != nil {
		t.Fatal(err)
	}
	fs, _ = h.readFile("/etc/fstab")
	if strings.Contains(fs, "scratch") {
		t.Errorf("fstab nach Entfernen:\n%s", fs)
	}
	o, _ = h.SAN(ctx)
	if len(o.Datastores) != 1 {
		t.Errorf("Datastores nach Entfernen: %+v", o.Datastores)
	}
}

func TestISCSITargetLifecycle(t *testing.T) {
	h := newDemoHost(t)
	ctx := context.Background()
	tg := &ISCSITarget{Name: "san2", Addr: "10.0.0.51", IQN: "iqn.2003-01.org.example:vol2", Enabled: true}
	rt := func(task *Task, err error) { t.Helper(); runTask(t, task, err) }
	rt(h.SaveISCSITarget(ctx, "", tg))
	if _, err := h.SaveISCSITarget(ctx, "", tg); err == nil {
		t.Error("doppeltes Ziel akzeptiert")
	}
	conf, _ := h.readFile("/etc/iscsi.conf")
	if !strings.Contains(conf, "target san2") || !strings.Contains(conf, "target nas-vol1") {
		t.Errorf("iscsi.conf:\n%s", conf)
	}
	tg2 := *tg
	tg2.Enabled = false
	rt(h.SaveISCSITarget(ctx, "san2", &tg2))
	if conf, _ = h.readFile("/etc/iscsi.conf"); !strings.Contains(conf, "disabled") {
		t.Errorf("deaktiviert fehlt:\n%s", conf)
	}
	renamed := tg2
	renamed.Name = "anders"
	if _, err := h.SaveISCSITarget(ctx, "san2", &renamed); err == nil {
		t.Error("Umbenennen akzeptiert")
	}
	// Solange ein iSCSI-Datastore eingehängt ist, darf kein Ziel gelöscht werden.
	if _, err := h.DeleteISCSITarget(ctx, "san2"); err == nil {
		t.Error("Löschen trotz eingehängtem iSCSI-Datastore")
	}
	if err := h.UnmountDatastore(ctx, "vm-pool"); err != nil {
		t.Fatal(err)
	}
	rt(h.DeleteISCSITarget(ctx, "san2"))
	if conf, _ = h.readFile("/etc/iscsi.conf"); strings.Contains(conf, "san2") || !strings.Contains(conf, "nas-vol1") {
		t.Errorf("iscsi.conf nach Löschen:\n%s", conf)
	}
}
