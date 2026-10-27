package obsd

import (
	"context"
	"errors"
	"os"
	"testing"
)

func TestSnapshotTree(t *testing.T) {
	h := newDemoHost(t)
	ctx := context.Background()
	disk := "/var/vmd/web01.qcow2"
	tmpl := "/var/vmd/templates/openbsd80-base.qcow2"

	// Laufende VM: kein Snapshot.
	if _, err := h.CreateSnapshot(ctx, "web01", "a", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("laufende VM: %v", err)
	}
	h.R.Run(ctx, nil, "vmctl", "stop", "web01")

	a, err := h.CreateSnapshot(ctx, "web01", "Grundinstallation", "frisch")
	if err != nil {
		t.Fatal(err)
	}
	b, err := h.CreateSnapshot(ctx, "web01", "mit httpd", "")
	if err != nil {
		t.Fatal(err)
	}
	// Kette: aktive Disk → b → a → Template
	chain := h.backingChain(disk)
	if len(chain) != 3 || chain[0] != h.snapFile("web01", b.ID, 0) || chain[1] != h.snapFile("web01", a.ID, 0) || chain[2] != tmpl {
		t.Fatalf("Kette falsch: %v", chain)
	}
	if st, _ := os.Stat(h.P(h.snapFile("web01", a.ID, 0))); st.Mode().Perm() != 0o444 {
		t.Error("Snapshot-Disk ist nicht schreibgeschützt")
	}
	tree, _ := h.Snapshots(ctx, "web01")
	if tree.Current != b.ID || tree.Base != tmpl || findSnap(tree, b.ID).Parent != a.ID {
		t.Fatalf("Baum: current=%s base=%s", tree.Current, tree.Base)
	}
	// Template zählt web01 weiterhin als Klon, obwohl es nun tiefer in der Kette liegt.
	if tp, _ := h.template("openbsd80-base"); len(tp.Clones) != 1 {
		t.Errorf("Template-Klone nach Snapshots: %v", tp.Clones)
	}

	// Zurücksetzen auf a, aktuellen Zustand vorher sichern → Verzweigung.
	if err := h.RevertSnapshot(ctx, "web01", a.ID, "vor Revert"); err != nil {
		t.Fatal(err)
	}
	tree, _ = h.Snapshots(ctx, "web01")
	sa := findSnap(tree, a.ID)
	if tree.Current != a.ID || len(tree.Snapshots) != 3 || len(sa.Children) != 1 {
		t.Fatalf("nach Revert: current=%s snaps=%d kinder(a)=%v", tree.Current, len(tree.Snapshots), sa.Children)
	}

	// a zusammenführen: Kind b (mit dessen Kind) und der aktuelle Zustand hängen danach am Template.
	task, err := h.DeleteSnapshot(ctx, "web01", a.ID)
	runTask(t, task, err)
	tree, _ = h.Snapshots(ctx, "web01")
	if findSnap(tree, a.ID) != nil || tree.Current != "" || qcow2Backing(h.P(disk)) != tmpl {
		t.Fatalf("nach Zusammenführen: current=%q backing=%q", tree.Current, qcow2Backing(h.P(disk)))
	}
	if sb := findSnap(tree, b.ID); sb == nil || sb.Parent != "" || qcow2Backing(h.P(h.snapFile("web01", b.ID, 0))) != tmpl {
		t.Fatalf("b hängt nicht am Template")
	}

	// Konsolidieren entfernt alle Snapshots, das Template bleibt Basis (qemu-img vorhanden).
	task, err = h.ConsolidateSnapshots(ctx, "web01")
	runTask(t, task, err)
	tree, _ = h.Snapshots(ctx, "web01")
	if len(tree.Snapshots) != 0 || qcow2Backing(h.P(disk)) != tmpl {
		t.Fatalf("nach Konsolidieren: %d Snapshots, Basis %q", len(tree.Snapshots), qcow2Backing(h.P(disk)))
	}
}

func TestSnapshotWithoutQemuImg(t *testing.T) {
	h := newDemoHost(t)
	ctx := context.Background()
	os.Remove(h.P(qemuImg))
	a, err := h.CreateSnapshot(ctx, "build", "eins", "")
	if err != nil {
		t.Fatal(err)
	}
	// a hat den aktuellen Zustand als Kind → ohne qemu-img nicht zusammenführbar.
	if _, err := h.DeleteSnapshot(ctx, "build", a.ID); !errors.Is(err, ErrInvalid) {
		t.Errorf("erwartet Hinweis auf qemu-img: %v", err)
	}
	// Ein Blatt (nach Revert verwaister Zweig) lässt sich ohne qemu-img löschen.
	b, _ := h.CreateSnapshot(ctx, "build", "zwei", "")
	if err := h.RevertSnapshot(ctx, "build", a.ID, ""); err != nil {
		t.Fatal(err)
	}
	task, err := h.DeleteSnapshot(ctx, "build", b.ID)
	runTask(t, task, err)
	// Konsolidieren ohne qemu-img: vollständige Kopie ohne Basis.
	task, err = h.ConsolidateSnapshots(ctx, "build")
	runTask(t, task, err)
	if b := qcow2Backing(h.P("/var/vmd/build.qcow2")); b != "" {
		t.Errorf("Disk sollte eigenständig sein, Basis %q", b)
	}
	if n := h.snapshotCount("build"); n != 0 {
		t.Errorf("%d Snapshots übrig", n)
	}
	for _, bad := range []string{"", "x\ny"} {
		if _, err := h.CreateSnapshot(ctx, "build", bad, ""); !errors.Is(err, ErrInvalid) {
			t.Errorf("Name %q: %v", bad, err)
		}
	}
}
