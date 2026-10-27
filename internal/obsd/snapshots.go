package obsd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Offline-Snapshots als qcow2-Ketten.
//
// Anlegen:       aktive Disk D wird eingefroren nach <vmdir>/snapshots/<vm>/<id>/diskN.qcow2,
//                D wird ein neues Overlay darauf (vmctl create -b). vm.conf bleibt unverändert.
// Zurücksetzen:  D verwerfen, neues Overlay auf dem gewählten Snapshot – es entsteht ein Baum.
// Zusammenführen: Kinder des Snapshots per „qemu-img rebase“ auf dessen Eltern umhängen,
//                dann den Snapshot entfernen. Blätter ohne Kinder werden einfach gelöscht.
// Konsolidieren: die aktive Disk vollständig zusammenführen und alle Snapshots entfernen.
//
// Alle Vorgänge verlangen eine gestoppte VM.

const qemuImg = "/usr/local/bin/qemu-img"

type Snapshot struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Created     int64    `json:"created"`
	Disks       []string `json:"disks"` // Pfade der VM-Disks zum Zeitpunkt des Snapshots
	Parent      string   `json:"parent"`
	Children    []string `json:"children"`
	Size        int64    `json:"size"`
	Current     bool     `json:"current"` // aktueller Zustand baut direkt hierauf auf
}

type SnapshotTree struct {
	VM        string      `json:"vm"`
	Snapshots []*Snapshot `json:"snapshots"`
	Current   string      `json:"current"` // ID, auf der der aktuelle Zustand aufbaut ("" = Wurzel)
	Base      string      `json:"base"`    // Basisimage unterhalb aller Snapshots (z. B. Template)
	Running   bool        `json:"running"`
	QemuImg   bool        `json:"qemuImg"`
	Supported bool        `json:"supported"`
	Reason    string      `json:"reason,omitempty"`
}

func (h *Host) snapDir(vm string) string { return filepath.Join(h.VMDir, "snapshots", vm) }

func (h *Host) snapFile(vm, id string, i int) string {
	return filepath.Join(h.snapDir(vm), id, fmt.Sprintf("disk%d.qcow2", i))
}

// isQcow2 prüft die Signatur einer Disk.
func isQcow2(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	var m [4]byte
	_, err = io.ReadFull(f, m[:])
	return err == nil && string(m[:]) == "QFI\xfb"
}

// backingChain liefert die Basisimages einer Disk von oben nach unten.
func (h *Host) backingChain(p string) []string {
	var chain []string
	for i := 0; i < 64; i++ {
		b := qcow2Backing(h.P(p))
		if b == "" {
			break
		}
		chain = append(chain, b)
		p = b
	}
	return chain
}

// snapIDFromPath erkennt, ob ein Pfad eine Snapshot-Disk dieser VM ist.
func (h *Host) snapIDFromPath(vm, p string) string {
	prefix := h.snapDir(vm) + "/"
	if !strings.HasPrefix(p, prefix) {
		return ""
	}
	return strings.SplitN(strings.TrimPrefix(p, prefix), "/", 2)[0]
}

func (h *Host) snapVM(ctx context.Context, name string) (*VMConf, bool, error) {
	conf, err := h.loadVMConf()
	if err != nil {
		return nil, false, err
	}
	v := conf.VM(name)
	if v == nil {
		return nil, false, invalid("VM %q ist nicht in vm.conf definiert", name)
	}
	running := false
	if st, err := h.vmctlStatus(ctx); err == nil {
		for _, x := range st {
			if x.Name == name && x.State != "stopped" {
				running = true
			}
		}
	}
	return v, running, nil
}

func (h *Host) Snapshots(ctx context.Context, vm string) (*SnapshotTree, error) {
	v, running, err := h.snapVM(ctx, vm)
	if err != nil {
		return nil, err
	}
	t := &SnapshotTree{VM: vm, Snapshots: []*Snapshot{}, Running: running, Supported: true}
	_, err = os.Stat(h.P(qemuImg))
	t.QemuImg = err == nil
	if len(v.Disks) == 0 {
		t.Supported, t.Reason = false, "Die VM hat keine Disk."
	}
	for _, d := range v.Disks {
		if !isQcow2(h.P(d)) {
			t.Supported, t.Reason = false, fmt.Sprintf("%s ist kein qcow2-Image. Snapshots brauchen qcow2 (z. B. per „Als Template“ oder vmctl create -i umwandeln).", d)
		}
	}
	metas, _ := filepath.Glob(filepath.Join(h.P(h.snapDir(vm)), "*", "meta.json"))
	byID := map[string]*Snapshot{}
	for _, m := range metas {
		b, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		s := &Snapshot{}
		if json.Unmarshal(b, s) != nil || !reName.MatchString(s.ID) {
			continue
		}
		s.Children = []string{}
		for i := range s.Disks {
			if st, err := os.Stat(h.P(h.snapFile(vm, s.ID, i))); err == nil {
				s.Size += st.Size()
			}
		}
		byID[s.ID] = s
		t.Snapshots = append(t.Snapshots, s)
	}
	// Eltern aus dem qcow2-Header der ersten Disk – die Dateien sind die Wahrheit.
	for _, s := range t.Snapshots {
		b := qcow2Backing(h.P(h.snapFile(vm, s.ID, 0)))
		if pid := h.snapIDFromPath(vm, b); byID[pid] != nil {
			s.Parent = pid
			byID[pid].Children = append(byID[pid].Children, s.ID)
		} else if b != "" {
			t.Base = b
		}
	}
	if len(v.Disks) > 0 {
		b := qcow2Backing(h.P(v.Disks[0]))
		if id := h.snapIDFromPath(vm, b); byID[id] != nil {
			t.Current = id
			byID[id].Current = true
		} else if t.Base == "" {
			t.Base = b
		}
	}
	sort.Slice(t.Snapshots, func(a, b int) bool { return t.Snapshots[a].Created < t.Snapshots[b].Created })
	return t, nil
}

func (h *Host) snapReady(ctx context.Context, vm string) (*VMConf, *SnapshotTree, error) {
	v, _, err := h.snapVM(ctx, vm)
	if err != nil {
		return nil, nil, err
	}
	t, err := h.Snapshots(ctx, vm)
	if err != nil {
		return nil, nil, err
	}
	if !t.Supported {
		return nil, nil, invalid("%s", t.Reason)
	}
	if t.Running {
		return nil, nil, invalid("VM %q muss für Snapshot-Vorgänge gestoppt sein", vm)
	}
	return v, t, nil
}

func newSnapID() string {
	b := make([]byte, 2)
	rand.Read(b)
	return time.Now().Format("20060102-150405") + "-" + hex.EncodeToString(b)
}

// CreateSnapshot friert den aktuellen Zustand aller Disks ein.
func (h *Host) CreateSnapshot(ctx context.Context, vm, name, desc string) (*Snapshot, error) {
	h.snapMu.Lock()
	defer h.snapMu.Unlock()
	return h.createSnapshot(ctx, vm, name, desc)
}

func (h *Host) createSnapshot(ctx context.Context, vm, name, desc string) (*Snapshot, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 80 || strings.ContainsAny(name, "\n\x00") || strings.ContainsAny(desc, "\x00") || len(desc) > 500 {
		return nil, invalid("Name (1–80 Zeichen) oder Beschreibung")
	}
	v, _, err := h.snapReady(ctx, vm)
	if err != nil {
		return nil, err
	}
	s := &Snapshot{ID: newSnapID(), Name: name, Description: desc, Created: time.Now().Unix(), Disks: v.Disks}
	dir := filepath.Join(h.snapDir(vm), s.ID)
	if err := os.MkdirAll(h.P(dir), 0o700); err != nil {
		return nil, err
	}
	var moved []int
	undo := func() {
		for _, i := range moved {
			os.Remove(h.P(v.Disks[i]))
			os.Chmod(h.P(h.snapFile(vm, s.ID, i)), 0o644)
			os.Rename(h.P(h.snapFile(vm, s.ID, i)), h.P(v.Disks[i]))
		}
		os.RemoveAll(h.P(dir))
	}
	for i, d := range v.Disks {
		sf := h.snapFile(vm, s.ID, i)
		if err := os.Rename(h.P(d), h.P(sf)); err != nil {
			undo()
			return nil, err
		}
		moved = append(moved, i)
		os.Chmod(h.P(sf), 0o444)
		if _, err := h.R.Run(ctx, nil, "vmctl", "create", "-b", sf, d); err != nil {
			undo()
			return nil, err
		}
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	if err := os.WriteFile(h.P(filepath.Join(dir, "meta.json")), b, 0o600); err != nil {
		undo()
		return nil, err
	}
	return s, nil
}

// RevertSnapshot verwirft den aktuellen Zustand und setzt auf den Snapshot zurück.
func (h *Host) RevertSnapshot(ctx context.Context, vm, id string, saveAs string) error {
	h.snapMu.Lock()
	defer h.snapMu.Unlock()
	if saveAs != "" {
		if _, err := h.createSnapshot(ctx, vm, saveAs, "vor dem Zurücksetzen gesichert"); err != nil {
			return err
		}
	}
	v, t, err := h.snapReady(ctx, vm)
	if err != nil {
		return err
	}
	s := findSnap(t, id)
	if s == nil {
		return invalid("Snapshot %q existiert nicht", id)
	}
	if len(s.Disks) != len(v.Disks) {
		return invalid("die VM hat seit dem Snapshot eine andere Anzahl Disks")
	}
	for i, d := range v.Disks {
		tmp := d + ".revert"
		if _, err := h.R.Run(ctx, nil, "vmctl", "create", "-b", h.snapFile(vm, id, i), tmp); err != nil {
			os.Remove(h.P(tmp))
			return err
		}
		if err := os.Rename(h.P(tmp), h.P(d)); err != nil {
			return err
		}
	}
	return nil
}

func findSnap(t *SnapshotTree, id string) *Snapshot {
	for _, s := range t.Snapshots {
		if s.ID == id {
			return s
		}
	}
	return nil
}

// DeleteSnapshot entfernt einen Snapshot. Hat er Kinder (Snapshots oder den
// aktuellen Zustand), werden diese vorher auf seine Eltern umgehängt.
func (h *Host) DeleteSnapshot(ctx context.Context, vm, id string) (*Task, error) {
	v, t, err := h.snapReady(ctx, vm)
	if err != nil {
		return nil, err
	}
	s := findSnap(t, id)
	if s == nil {
		return nil, invalid("Snapshot %q existiert nicht", id)
	}
	// Kinder je Disk: Snapshot-Dateien und ggf. die aktive Disk.
	type rebase struct{ child, parent string }
	var jobs []rebase
	for i := range s.Disks {
		parent := qcow2Backing(h.P(h.snapFile(vm, id, i)))
		for _, c := range s.Children {
			jobs = append(jobs, rebase{h.snapFile(vm, c, i), parent})
		}
		if s.Current && i < len(v.Disks) {
			jobs = append(jobs, rebase{v.Disks[i], parent})
		}
	}
	if len(jobs) > 0 && !t.QemuImg {
		return nil, invalid("Snapshot %q hat Nachfolger. Zum Zusammenführen wird qemu-img benötigt (Paket qemu)", s.Name)
	}
	mode := "löschen"
	if len(jobs) > 0 {
		mode = "zusammenführen"
	}
	return &Task{
		Title: fmt.Sprintf("Snapshot „%s“ von %s %s", s.Name, vm, mode), Command: "qemu-img rebase",
		Run: func(ctx context.Context, w io.Writer) error {
			h.snapMu.Lock()
			defer h.snapMu.Unlock()
			for _, j := range jobs {
				step(w, "%s → Basis %s", j.child, orNone(j.parent))
				os.Chmod(h.P(j.child), 0o644)
				err := h.R.Stream(ctx, w, qemuImg, "rebase", "-f", "qcow2", "-F", "qcow2", "-b", j.parent, j.child)
				if h.snapIDFromPath(vm, j.child) != "" {
					os.Chmod(h.P(j.child), 0o444)
				}
				if err != nil {
					return err
				}
			}
			step(w, "Entferne %s", filepath.Join(h.snapDir(vm), id))
			if err := os.RemoveAll(h.P(filepath.Join(h.snapDir(vm), id))); err != nil {
				return err
			}
			step(w, "Fertig.")
			return nil
		},
	}, nil
}

func orNone(s string) string {
	if s == "" {
		return "(keine – eigenständig)"
	}
	return s
}

// ConsolidateSnapshots führt den aktuellen Zustand zusammen und entfernt alle
// Snapshots. Mit qemu-img bleibt ein Template als Basis erhalten, ohne wird
// die Disk eigenständig (vmctl create -i).
func (h *Host) ConsolidateSnapshots(ctx context.Context, vm string) (*Task, error) {
	v, t, err := h.snapReady(ctx, vm)
	if err != nil {
		return nil, err
	}
	if len(t.Snapshots) == 0 {
		return nil, invalid("VM %q hat keine Snapshots", vm)
	}
	return &Task{
		Title: "Snapshots von " + vm + " konsolidieren", Command: "qemu-img rebase / vmctl create -i",
		Run: func(ctx context.Context, w io.Writer) error {
			h.snapMu.Lock()
			defer h.snapMu.Unlock()
			for _, d := range v.Disks {
				chain := h.backingChain(d)
				base := ""
				for _, b := range chain {
					if h.snapIDFromPath(vm, b) == "" {
						base = b // erstes Image unterhalb der Snapshots, z. B. ein Template
						break
					}
				}
				if t.QemuImg {
					step(w, "%s → Basis %s", d, orNone(base))
					if err := h.R.Stream(ctx, w, qemuImg, "rebase", "-f", "qcow2", "-F", "qcow2", "-b", base, d); err != nil {
						return err
					}
					continue
				}
				tmp := d + ".consolidate"
				step(w, "%s vollständig zusammenführen (vmctl create -i, ohne Basis)", d)
				if err := h.R.Stream(ctx, w, "vmctl", "create", "-i", d, tmp); err != nil {
					os.Remove(h.P(tmp))
					return err
				}
				if err := os.Rename(h.P(tmp), h.P(d)); err != nil {
					return err
				}
			}
			step(w, "Entferne alle Snapshots unter %s", h.snapDir(vm))
			if err := os.RemoveAll(h.P(h.snapDir(vm))); err != nil {
				return err
			}
			step(w, "Fertig.")
			return nil
		},
	}, nil
}

func (h *Host) RenameSnapshot(ctx context.Context, vm, id, name, desc string) error {
	h.snapMu.Lock()
	defer h.snapMu.Unlock()
	t, err := h.Snapshots(ctx, vm)
	if err != nil {
		return err
	}
	s := findSnap(t, id)
	if s == nil {
		return invalid("Snapshot %q existiert nicht", id)
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 80 || strings.ContainsAny(name, "\n\x00") || len(desc) > 500 {
		return invalid("Name oder Beschreibung")
	}
	s.Name, s.Description = name, desc
	meta := *s
	meta.Parent, meta.Children, meta.Size, meta.Current = "", nil, 0, false
	b, _ := json.MarshalIndent(meta, "", "  ")
	return os.WriteFile(h.P(filepath.Join(h.snapDir(vm), id, "meta.json")), b, 0o600)
}

// snapshotCount für die VM-Übersicht.
func (h *Host) snapshotCount(vm string) int {
	m, _ := filepath.Glob(filepath.Join(h.P(h.snapDir(vm)), "*", "meta.json"))
	return len(m)
}
