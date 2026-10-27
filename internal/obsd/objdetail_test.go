package obsd

import (
	"context"
	"testing"
)

func TestDatastoreAndPortGroupDetail(t *testing.T) {
	h := newDemoHost(t)
	ctx := context.Background()

	// Eine VM legt ihre Disk auf den Datastore.
	if err := h.CreateDisk(ctx, "db02", "2G", "qcow2", "", "vm-pool"); err != nil {
		t.Fatal(err)
	}
	if err := h.CreateVM(ctx, &VMSpec{Name: "db02", Memory: "1G", CPUs: 1, DiskPath: "/var/vmd/vm-pool/db02.qcow2", Switches: []string{"intern"}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	d, err := h.DatastoreDetail(ctx, "vm-pool", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Mounted || d.Disk == nil || d.Disk.Name != "sd2" || d.Disk.Transport != "iscsi" || len(d.Targets) != 1 {
		t.Errorf("Datastore: %+v disk=%+v", d.DatastoreInfo, d.Disk)
	}
	if len(d.Users) != 1 || d.Users[0].VM != "db02" || len(d.Users[0].Disks) != 1 || d.Users[0].State != "stopped" {
		t.Errorf("Verwendet von: %+v", d.Users)
	}
	var img *VMImage
	for i := range d.Images {
		if d.Images[i].Name == "db02.qcow2" {
			img = &d.Images[i]
		}
	}
	if img == nil || len(img.UsedBy) != 1 || img.UsedBy[0] != "db02" {
		t.Errorf("Images: %+v", d.Images)
	}
	// Eingegrenzt: fremde VMs werden nicht verraten.
	s, _ := h.DatastoreDetail(ctx, "vm-pool", []string{"web01"})
	if len(s.Users) != 0 || s.Hidden != 1 || len(s.Images[0].UsedBy) != 1 || s.Images[0].UsedBy[0] != "(andere VM)" {
		t.Errorf("Eingrenzung: users=%v hidden=%d images=%+v", s.Users, s.Hidden, s.Images)
	}
	if _, err := h.DatastoreDetail(ctx, "gibtsnicht", nil); err == nil {
		t.Error("unbekannter Datastore")
	}

	p, err := h.PortGroupDetail(ctx, "intern", nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]PortGroupUse{}
	for _, u := range p.Uses {
		names[u.VM] = u
	}
	if len(p.Uses) != 3 || names["web01"].IP != "10.0.0.11" || names["web01"].State != "running" || names["web01"].In != "deny" || names["web01"].Rules != 2 || names["db02"].IP != "" {
		t.Errorf("Verwendet von: %+v", p.Uses)
	}
	if p.Reserved != 2 || p.Files["/etc/hostname.veb0"] == "" || p.Switch == "" || len(p.Members) == 0 {
		t.Errorf("Portgruppe: reserved=%d files=%v members=%v", p.Reserved, p.Files, p.Members)
	}
	sp, _ := h.PortGroupDetail(ctx, "intern", []string{"web01"})
	if len(sp.Uses) != 1 || sp.Hidden != 2 || len(sp.VMs) != 1 || sp.VMs[0] != "web01" {
		t.Errorf("Eingrenzung: %+v hidden=%d vms=%v", sp.Uses, sp.Hidden, sp.VMs)
	}
	if _, err := h.PortGroupDetail(ctx, "x", nil); err == nil {
		t.Error("unbekannte Portgruppe")
	}
}
