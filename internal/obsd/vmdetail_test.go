package obsd

import (
	"context"
	"strings"
	"testing"
)

func TestVMDetail(t *testing.T) {
	h := newDemoHost(t)
	ctx := context.Background()
	d, err := h.VMDetail(ctx, "web01")
	if err != nil {
		t.Fatal(err)
	}
	if d.VM.State != "running" || d.VM.Conf == nil || len(d.Disks) != 1 {
		t.Fatalf("web01: %+v", d.VM)
	}
	disk := d.Disks[0]
	if disk.Missing || disk.Format != "qcow2" || disk.Virtual < disk.Size || d.Template != "openbsd80-base" || len(disk.Backing) == 0 {
		t.Errorf("Disk: %+v (Template %q)", disk, d.Template)
	}
	if len(d.NICs) != 1 || d.NICs[0].IP != "10.0.0.11" || len(d.Groups) != 1 || d.Groups[0].Name != "intern" {
		t.Errorf("Netz: %+v %+v", d.NICs, d.Groups)
	}
	if d.Policy.In != "deny" || len(d.Rules) != 2 {
		t.Errorf("Firewall: %+v %d", d.Policy, len(d.Rules))
	}
	if len(d.Backups) != 1 || d.Backups[0].Name == "" {
		t.Errorf("Backup-Jobs: %+v", d.Backups)
	}
	if _, err := h.VMDetail(ctx, "gibtsnicht"); err == nil {
		t.Error("unbekannte VM")
	}
	if _, err := h.VMDetail(ctx, "../x"); err == nil {
		t.Error("ungültiger Name")
	}
	tpl, ds, pg := h.InventoryNames(ctx)
	if len(tpl) != 2 || len(ds) != 1 || ds[0] != "vm-pool" || len(pg) != 1 || strings.Join(pg, "") != "intern" {
		t.Errorf("Inventar: %v %v %v", tpl, ds, pg)
	}
	// Eine VM auf einem Datastore wird dort erkannt.
	di := h.diskInfo("/var/vmd/vm-pool/.keep")
	if di.Store != "vm-pool" {
		t.Errorf("Datastore nicht erkannt: %+v", di)
	}
}
