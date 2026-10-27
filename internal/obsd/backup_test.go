package obsd

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestScheduleNextRun(t *testing.T) {
	loc := time.Local
	base := time.Date(2026, 10, 27, 10, 30, 0, 0, loc) // Dienstag
	cases := []struct {
		s    Schedule
		want time.Time
	}{
		{Schedule{Kind: "daily", Time: "02:15"}, time.Date(2026, 10, 28, 2, 15, 0, 0, loc)},
		{Schedule{Kind: "daily", Time: "23:00"}, time.Date(2026, 10, 27, 23, 0, 0, 0, loc)},
		{Schedule{Kind: "hourly", Time: "00:45"}, time.Date(2026, 10, 27, 10, 45, 0, 0, loc)},
		{Schedule{Kind: "hourly", Time: "00:15"}, time.Date(2026, 10, 27, 11, 15, 0, 0, loc)},
		{Schedule{Kind: "weekly", Time: "03:00", Weekday: 0}, time.Date(2026, 11, 1, 3, 0, 0, 0, loc)},
		{Schedule{Kind: "weekly", Time: "11:00", Weekday: 2}, time.Date(2026, 10, 27, 11, 0, 0, 0, loc)},
		{Schedule{Kind: "manual"}, time.Time{}},
	}
	for _, c := range cases {
		if got := c.s.NextRun(base); !got.Equal(c.want) {
			t.Errorf("%+v: %v, erwartet %v", c.s, got, c.want)
		}
	}
}

func TestBackupTargetValidation(t *testing.T) {
	h := newDemoHost(t)
	for _, bad := range []*BackupTarget{
		{Name: "x", Type: "local", Path: "/etc"},
		{Name: "x", Type: "local", Path: "/var/vmd/backup"},
		{Name: "x", Type: "local", Path: "relativ"},
		{Name: "x", Type: "local", Path: "/a/../etc"},
		{Name: "x", Type: "nfs", Path: "nas:/x;reboot"},
		{Name: "x", Type: "sftp", Host: "h", User: "u", Path: "../x"},
		{Name: "x", Type: "sftp", Host: "-oProxyCommand=x", User: "u", Path: "x"},
		{Name: "x", Type: "sftp", Host: "h", User: "u x", Path: "x"},
		{Name: "", Type: "local", Path: "/backup"},
		{Name: "x", Type: "s3", Path: "/backup"},
	} {
		if _, err := h.SaveTarget(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: erwartet ErrInvalid, bekam %v", bad, err)
		}
	}
	if err := h.DeleteTarget("nas"); !errors.Is(err, ErrInvalid) {
		t.Errorf("Ziel mit Job darf nicht gelöscht werden: %v", err)
	}
}

func TestBackupSnapshotModeAndRestore(t *testing.T) {
	h := newDemoHost(t)
	ctx := context.Background()
	usb := "usb"

	// web01 läuft: Snapshot-Modus friert ein, startet sofort wieder und sichert aus dem Snapshot.
	task, err := h.BackupTask(ctx, &BackupRequest{VMs: []string{"web01"}, Target: usb, Mode: "snapshot", Keep: 2}, "")
	out := runTask(t, task, err)
	if !strings.Contains(out, "läuft wieder") || h.vmState(ctx, "web01") != "running" {
		t.Fatalf("Snapshot-Modus:\n%s", out)
	}
	tree, _ := h.Snapshots(ctx, "web01")
	if len(tree.Snapshots) != 1 || tree.Snapshots[0].Name != backupSnapName {
		t.Fatalf("erwartet genau einen Backup-Snapshot, gefunden %d", len(tree.Snapshots))
	}
	// Zweiter und dritter Lauf: alter Backup-Snapshot wird zusammengeführt, Kette wächst nicht.
	for i := 0; i < 2; i++ {
		time.Sleep(1100 * time.Millisecond) // eindeutige Sicherungs-ID (Sekunden)
		task, err = h.BackupTask(ctx, &BackupRequest{VMs: []string{"web01"}, Target: usb, Mode: "snapshot", Keep: 2}, "")
		runTask(t, task, err)
	}
	tree, _ = h.Snapshots(ctx, "web01")
	if len(tree.Snapshots) != 1 {
		t.Errorf("Backup-Snapshots häufen sich: %d", len(tree.Snapshots))
	}
	list, _ := h.ListBackups(ctx, usb)
	if len(list) != 2 {
		t.Fatalf("Aufbewahrung 2, gefunden %d", len(list))
	}

	// Wiederherstellen als neue VM.
	task, err = h.RestoreTask(ctx, usb, &RestoreRequest{VM: "web01", Backup: list[0].ID, Mode: "new", Name: "web01-restore"})
	runTask(t, task, err)
	conf, _ := h.loadVMConf()
	v := conf.VM("web01-restore")
	if v == nil || v.Enabled || v.Disks[0] != "/var/vmd/web01-restore.qcow2" || v.Memory != "1G" || len(v.Interfaces) != 1 || v.Interfaces[0].Switch != "intern" {
		t.Fatalf("wiederhergestellte VM: %+v", v)
	}
	if b := qcow2Backing(h.P(v.Disks[0])); b != "" {
		t.Errorf("Wiederherstellung sollte eigenständig sein, Basis %q", b)
	}

	// Überschreiben verlangt eine gestoppte VM und sichert vorher den Zustand als Snapshot.
	if _, err := h.RestoreTask(ctx, usb, &RestoreRequest{VM: "web01", Backup: list[0].ID, Mode: "overwrite"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("Überschreiben einer laufenden VM: %v", err)
	}
	h.R.Run(ctx, nil, "vmctl", "stop", "web01")
	task, err = h.RestoreTask(ctx, usb, &RestoreRequest{VM: "web01", Backup: list[1].ID, Mode: "overwrite"})
	runTask(t, task, err)
	tree, _ = h.Snapshots(ctx, "web01")
	found := false
	for _, s := range tree.Snapshots {
		found = found || strings.HasPrefix(s.Name, "vor Wiederherstellung")
	}
	if !found {
		t.Error("kein Sicherheits-Snapshot vor dem Überschreiben")
	}

	// Pfadangriffe auf Sicherungs-IDs.
	if _, err := h.RestoreTask(ctx, usb, &RestoreRequest{VM: "web01", Backup: "../../etc", Mode: "new", Name: "x"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("ungültige ID: %v", err)
	}
}

func TestBackupNFSAndSFTP(t *testing.T) {
	h := newDemoHost(t)
	ctx := context.Background()
	// Vorhandene Demo-Sicherungen auf dem NAS.
	list, err := h.ListBackups(ctx, "nas")
	if err != nil || len(list) == 0 {
		t.Fatalf("NAS: %v %d", err, len(list))
	}
	sftp, err := h.SaveTarget(&BackupTarget{Name: "Offsite", Type: "sftp", Host: "backup.example.net", User: "obsd", Path: "/srv/vm"})
	if err != nil || sftp.ID != "offsite" || sftp.Port != 22 {
		t.Fatalf("SFTP-Ziel: %+v %v", sftp, err)
	}
	if msg, err := h.TestTarget(ctx, "offsite"); err != nil || !strings.Contains(msg, "beschreibbar") {
		t.Fatalf("Test: %q %v", msg, err)
	}
	// db01 offline sichern (VM wird gestoppt und wieder gestartet).
	task, err := h.BackupTask(ctx, &BackupRequest{VMs: []string{"db01"}, Target: "offsite", Mode: "offline", Keep: 3}, "")
	out := runTask(t, task, err)
	if !strings.Contains(out, "Fahre db01 herunter") || h.vmState(ctx, "db01") != "running" {
		t.Errorf("Offline-Modus:\n%s", out)
	}
	list, _ = h.ListBackups(ctx, "offsite")
	if len(list) != 1 || list[0].VM != "db01" || len(list[0].Disks) != 1 {
		t.Fatalf("SFTP-Liste: %+v", list)
	}
	task, err = h.RestoreTask(ctx, "offsite", &RestoreRequest{VM: "db01", Backup: list[0].ID, Mode: "new", Name: "db01-copy", StartNow: true, NoNetwork: true})
	runTask(t, task, err)
	if conf, _ := h.loadVMConf(); len(conf.VM("db01-copy").Interfaces) != 0 {
		t.Error("Kopie ohne Netz hat dennoch Interfaces")
	}
	if h.vmState(ctx, "db01-copy") != "running" {
		t.Error("wiederhergestellte VM läuft nicht")
	}
	if err := h.DeleteBackup(ctx, "offsite", "db01", list[0].ID); err != nil {
		t.Fatal(err)
	}
	if list, _ = h.ListBackups(ctx, "offsite"); len(list) != 0 {
		t.Errorf("nach Löschen noch %d Sicherungen", len(list))
	}
}

func TestBackupJobs(t *testing.T) {
	h := newDemoHost(t)
	j, err := h.SaveJob(&BackupJob{Name: "Wöchentlich offsite", Target: "usb", Mode: "pause", Keep: 4, Enabled: true, Schedule: Schedule{Kind: "weekly", Time: "03:30", Weekday: 0}})
	if err != nil || j.ID != "w-chentlich-offsite" {
		t.Fatalf("%+v %v", j, err)
	}
	for _, bad := range []*BackupJob{
		{Name: "x", Target: "fehlt", Mode: "pause", Keep: 1, Schedule: Schedule{Kind: "manual"}},
		{Name: "x", Target: "usb", Mode: "live", Keep: 1, Schedule: Schedule{Kind: "manual"}},
		{Name: "x", Target: "usb", Mode: "pause", Keep: 0, Schedule: Schedule{Kind: "manual"}},
		{Name: "x", Target: "usb", Mode: "pause", Keep: 1, Schedule: Schedule{Kind: "daily", Time: "25:00"}},
		{Name: "x", Target: "usb", Mode: "pause", Keep: 1, VMs: []string{"a;b"}, Schedule: Schedule{Kind: "manual"}},
	} {
		if _, err := h.SaveJob(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	c, _ := h.BackupConfig()
	if len(c.Jobs) != 2 || c.LastRun[j.ID] == 0 {
		t.Errorf("Jobs: %d, LastRun gesetzt: %v", len(c.Jobs), c.LastRun[j.ID] != 0)
	}
}
