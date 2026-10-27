package obsd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Sicherungsmodi für laufende VMs:
//
//	snapshot  Herunterfahren → Snapshot „maar-backup“ → sofort wieder starten →
//	          aus dem eingefrorenen Snapshot sichern, während die VM läuft.
//	          Der vorige Backup-Snapshot wird im selben Stopp-Fenster zusammengeführt.
//	          Ausfall: Herunterfahren + Booten, unabhängig von der Diskgröße.
//	offline   Herunterfahren, sichern, wieder starten. Ausfall: gesamte Kopierdauer.
//	pause     Pausieren, sichern, fortsetzen. Kein Neustart, aber nur absturzkonsistent.
//
// Gestoppte VMs werden immer direkt gesichert.

const backupSnapName = "maar-backup"

// BackupRequest beschreibt einen Lauf (aus einem Job oder „Jetzt sichern“).
type BackupRequest struct {
	Job    string   `json:"job,omitempty"`
	VMs    []string `json:"vms"`
	Target string   `json:"target"`
	Mode   string   `json:"mode"`
	Keep   int      `json:"keep"`
}

func (h *Host) RequestForJob(id string) (*BackupRequest, string, error) {
	c, err := h.BackupConfig()
	if err != nil {
		return nil, "", err
	}
	j := c.job(id)
	if j == nil {
		return nil, "", invalid("Job %q existiert nicht", id)
	}
	return &BackupRequest{Job: j.ID, VMs: j.VMs, Target: j.Target, Mode: j.Mode, Keep: j.Keep}, j.Name, nil
}

func (h *Host) BackupTask(ctx context.Context, r *BackupRequest, title string) (*Task, error) {
	t, err := h.targetByID(r.Target)
	if err != nil {
		return nil, err
	}
	if !backupModes[r.Mode] {
		return nil, invalid("Modus %q", r.Mode)
	}
	conf, err := h.loadVMConf()
	if err != nil {
		return nil, err
	}
	vms := r.VMs
	if len(vms) == 0 {
		for _, v := range conf.VMs {
			vms = append(vms, v.Name)
		}
	}
	for _, v := range vms {
		if c := conf.VM(v); c == nil || len(c.Disks) == 0 {
			return nil, invalid("VM %q fehlt in vm.conf oder hat keine Disk", v)
		}
	}
	if len(vms) == 0 {
		return nil, invalid("keine VMs zu sichern")
	}
	if title == "" {
		title = "Sicherung " + strings.Join(vms, ", ")
	}
	return &Task{
		Title: title + " → " + t.Name, Command: fmt.Sprintf("backup %s → %s (%s)", strings.Join(vms, " "), t.Location(), r.Mode),
		Run: func(ctx context.Context, w io.Writer) error {
			s := h.newStore(t)
			if err := s.open(ctx, w); err != nil {
				return err
			}
			defer s.close(ctx)
			var failed []string
			for _, vm := range vms {
				fmt.Fprintf(w, "\n━━ %s ━━\n", vm)
				if err := h.backupVM(ctx, w, s, vm, r.Mode); err != nil {
					fmt.Fprintf(w, "FEHLER %s: %v\n", vm, err)
					failed = append(failed, vm)
					continue
				}
				if r.Keep > 0 {
					h.applyRetention(ctx, w, s, vm, r.Keep)
				}
			}
			if len(failed) > 0 {
				return fmt.Errorf("Sicherung fehlgeschlagen für %s", strings.Join(failed, ", "))
			}
			step(w, "Alle Sicherungen abgeschlossen.")
			return nil
		},
	}, nil
}

func (h *Host) vmState(ctx context.Context, vm string) string {
	st, _ := h.vmctlStatus(ctx)
	for _, x := range st {
		if x.Name == vm {
			return x.State
		}
	}
	return "stopped"
}

func (h *Host) stopWait(ctx context.Context, w io.Writer, vm string) error {
	step(w, "Fahre %s herunter", vm)
	h.ExpectStop(vm)
	cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if _, err := h.R.Run(cctx, nil, "vmctl", "stop", "-w", vm); err != nil {
		return fmt.Errorf("Herunterfahren fehlgeschlagen (Gast reagiert nicht auf ACPI?): %w", err)
	}
	return nil
}

func (h *Host) startVM(w io.Writer, vm string) {
	step(w, "Starte %s", vm)
	if _, err := h.R.Run(context.Background(), nil, "vmctl", "start", vm); err != nil {
		fmt.Fprintf(w, "WARNUNG: %s konnte nicht gestartet werden: %v\n", vm, err)
	}
}

func (h *Host) backupVM(ctx context.Context, w io.Writer, s store, vm, mode string) error {
	conf, err := h.loadVMConf()
	if err != nil {
		return err
	}
	v := conf.VM(vm)
	if v == nil {
		return invalid("VM %q nicht in vm.conf", vm)
	}
	sources := append([]string(nil), v.Disks...)
	state := h.vmState(ctx, vm)
	running := state == "running" || state == "paused"

	if running && mode == "snapshot" {
		_, qerr := os.Stat(h.P(qemuImg))
		allQcow := true
		for _, d := range v.Disks {
			allQcow = allQcow && isQcow2(h.P(d))
		}
		if qerr != nil || !allQcow {
			fmt.Fprintln(w, "Hinweis: Snapshot-Modus braucht qemu-img und qcow2-Disks – sichere offline.")
			mode = "offline"
		}
	}

	switch {
	case !running:
		step(w, "%s ist gestoppt – direkte Sicherung", vm)
	case mode == "pause":
		step(w, "Pausiere %s (absturzkonsistent)", vm)
		if _, err := h.R.Run(ctx, nil, "vmctl", "pause", vm); err != nil {
			return err
		}
		defer func() {
			step(w, "Setze %s fort", vm)
			h.R.Run(context.Background(), nil, "vmctl", "unpause", vm)
		}()
	case mode == "offline":
		if err := h.stopWait(ctx, w, vm); err != nil {
			return err
		}
		defer h.startVM(w, vm)
	case mode == "snapshot":
		if err := h.stopWait(ctx, w, vm); err != nil {
			return err
		}
		snap, err := h.rotateBackupSnapshot(ctx, w, vm)
		h.startVM(w, vm)
		if err != nil {
			return err
		}
		for i := range sources {
			sources[i] = h.snapFile(vm, snap.ID, i)
		}
		step(w, "%s läuft wieder – sichere aus Snapshot %s", vm, snap.ID)
	}

	id := time.Now().Format("20060102-150405")
	m := &BackupManifest{VM: vm, ID: id, Created: time.Now().Unix(), Mode: mode, Host: h.Hostname(ctx), Block: v.Raw}
	if !running {
		m.Mode = "stopped"
	}
	dir := filepath.Join(vm, id)
	for i, src := range sources {
		file := fmt.Sprintf("disk%d.qcow2", i)
		step(w, "Disk %d: %s → %s", i, src, filepath.Join(dir, file))
		n, err := s.writeDisk(ctx, w, src, filepath.Join(dir, file))
		if err != nil {
			return err
		}
		m.Disks = append(m.Disks, BackupDisk{Source: v.Disks[i], File: file, Size: n})
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	if err := s.writeFile(ctx, filepath.Join(dir, "manifest.json"), b); err != nil {
		return err
	}
	step(w, "Sicherung %s/%s fertig (%d Disks)", vm, id, len(m.Disks))
	return nil
}

// rotateBackupSnapshot führt alte Backup-Snapshots zusammen und legt einen neuen an.
// Läuft im Stopp-Fenster der VM.
func (h *Host) rotateBackupSnapshot(ctx context.Context, w io.Writer, vm string) (*Snapshot, error) {
	tree, err := h.Snapshots(ctx, vm)
	if err != nil {
		return nil, err
	}
	for _, old := range tree.Snapshots {
		if old.Name != backupSnapName {
			continue
		}
		step(w, "Führe vorigen Backup-Snapshot %s zusammen", old.ID)
		task, err := h.DeleteSnapshot(ctx, vm, old.ID)
		if err != nil {
			return nil, err
		}
		if err := task.Run(ctx, w); err != nil {
			return nil, err
		}
	}
	step(w, "Friere Disks als Snapshot ein")
	return h.CreateSnapshot(ctx, vm, backupSnapName, "von der Sicherung angelegt; wird beim nächsten Lauf zusammengeführt")
}

func (h *Host) applyRetention(ctx context.Context, w io.Writer, s store, vm string, keep int) {
	list, err := s.manifests(ctx)
	if err != nil {
		fmt.Fprintf(w, "Aufbewahrung übersprungen: %v\n", err)
		return
	}
	var mine []*BackupManifest
	for _, m := range list {
		if m.VM == vm {
			mine = append(mine, m)
		}
	}
	sort.Slice(mine, func(a, b int) bool { return mine[a].ID > mine[b].ID })
	for _, m := range mine[min(keep, len(mine)):] {
		step(w, "Aufbewahrung: entferne %s/%s", m.VM, m.ID)
		if err := s.remove(ctx, m); err != nil {
			fmt.Fprintf(w, "WARNUNG: %v\n", err)
		}
	}
}

func (h *Host) findBackup(ctx context.Context, s store, vm, id string) (*BackupManifest, error) {
	if !reName.MatchString(vm) || !reID.MatchString(id) {
		return nil, invalid("Sicherung %s/%s", vm, id)
	}
	list, err := s.manifests(ctx)
	if err != nil {
		return nil, err
	}
	for _, m := range list {
		if m.VM == vm && m.ID == id {
			return m, nil
		}
	}
	return nil, invalid("Sicherung %s/%s nicht gefunden", vm, id)
}

func (h *Host) DeleteBackup(ctx context.Context, targetID, vm, id string) error {
	t, err := h.targetByID(targetID)
	if err != nil {
		return err
	}
	s := h.newStore(t)
	if err := s.open(ctx, io.Discard); err != nil {
		return err
	}
	defer s.close(ctx)
	m, err := h.findBackup(ctx, s, vm, id)
	if err != nil {
		return err
	}
	return s.remove(ctx, m)
}

// RestoreRequest: als neue VM oder über die bestehende VM.
type RestoreRequest struct {
	VM        string `json:"vm"`
	Backup    string `json:"backup"`
	Mode      string `json:"mode"` // new, overwrite
	Name      string `json:"name"`
	StartNow  bool   `json:"startNow"`
	NoNetwork bool   `json:"noNetwork"` // Kopie ohne Netz – vermeidet IP-Konflikte mit dem Original
}

func (h *Host) RestoreTask(ctx context.Context, targetID string, r *RestoreRequest) (*Task, error) {
	t, err := h.targetByID(targetID)
	if err != nil {
		return nil, err
	}
	if r.Mode != "new" && r.Mode != "overwrite" {
		return nil, invalid("Wiederherstellungsart %q", r.Mode)
	}
	s := h.newStore(t)
	if err := s.open(ctx, io.Discard); err != nil {
		return nil, err
	}
	m, err := h.findBackup(ctx, s, r.VM, r.Backup)
	s.close(ctx)
	if err != nil {
		return nil, err
	}
	saved := ParseVMConf(m.Block).VM(m.VM)
	if saved == nil {
		return nil, fmt.Errorf("Manifest enthält keinen gültigen vm.conf-Block")
	}

	var targets []string // Zielpfade der Disks
	var spec *VMSpec
	if r.Mode == "new" {
		spec = &VMSpec{Name: r.Name, Memory: saved.Memory, CPUs: saved.CPUs, SEV: saved.SEV, Local: saved.Local, Owner: saved.Owner, Enabled: false}
		if spec.Memory == "" {
			spec.Memory = "512M"
		}
		for _, i := range saved.Interfaces {
			if i.Switch != "" {
				spec.Switches = append(spec.Switches, i.Switch)
			}
		}
		if r.NoNetwork {
			spec.Switches, spec.Local = nil, false
		}
		if err := h.validateVMSpec(spec); err != nil {
			return nil, err
		}
		conf, _ := h.loadVMConf()
		if conf.VM(r.Name) != nil {
			return nil, invalid("VM %q existiert bereits", r.Name)
		}
		for i := range m.Disks {
			p := filepath.Join(h.VMDir, r.Name+".qcow2")
			if i > 0 {
				p = filepath.Join(h.VMDir, fmt.Sprintf("%s-disk%d.qcow2", r.Name, i))
			}
			if _, err := os.Stat(h.P(p)); err == nil {
				return nil, invalid("%s existiert bereits", p)
			}
			targets = append(targets, p)
		}
	} else {
		conf, _ := h.loadVMConf()
		cur := conf.VM(m.VM)
		if cur == nil {
			return nil, invalid("VM %q existiert nicht mehr – als neue VM wiederherstellen", m.VM)
		}
		if h.vmState(ctx, m.VM) != "stopped" {
			return nil, invalid("VM %q muss zum Überschreiben gestoppt sein", m.VM)
		}
		if len(cur.Disks) != len(m.Disks) {
			return nil, invalid("VM hat %d Disks, Sicherung %d – als neue VM wiederherstellen", len(cur.Disks), len(m.Disks))
		}
		targets = cur.Disks
	}

	vm := m.VM
	if r.Mode == "new" {
		vm = r.Name
	}
	title := fmt.Sprintf("Wiederherstellung %s (%s) → %s", m.VM, m.ID, vm)
	return &Task{
		Title: title, Command: "restore " + t.Location() + "/" + m.VM + "/" + m.ID,
		Run: func(ctx context.Context, w io.Writer) (err error) {
			if err := s.open(ctx, w); err != nil {
				return err
			}
			defer s.close(ctx)
			if r.Mode == "overwrite" {
				// Sicherheitsnetz: aktuellen Zustand als Snapshot behalten.
				if tree, e := h.Snapshots(ctx, vm); e == nil && tree.Supported {
					if snap, e := h.CreateSnapshot(ctx, vm, "vor Wiederherstellung "+m.ID, "automatisch vor dem Überschreiben"); e == nil {
						step(w, "Aktueller Zustand als Snapshot %s gesichert", snap.ID)
					}
				}
			}
			var written []string
			defer func() {
				if err != nil && r.Mode == "new" {
					for _, p := range written {
						os.Remove(h.P(p))
					}
				}
			}()
			for i, d := range m.Disks {
				tmp := targets[i] + ".restore"
				os.Remove(h.P(tmp))
				step(w, "Disk %d: %s → %s", i, d.File, targets[i])
				if err := s.fetchDisk(ctx, w, filepath.Join(m.VM, m.ID, d.File), tmp); err != nil {
					os.Remove(h.P(tmp))
					return err
				}
				if err := os.Rename(h.P(tmp), h.P(targets[i])); err != nil {
					return err
				}
				written = append(written, targets[i])
			}
			if r.Mode == "new" {
				spec.DiskPath = targets[0]
				block := spec.Block()
				// Weitere Disks ergänzen.
				for _, p := range targets[1:] {
					block = strings.Replace(block, "\tdisk "+quote(targets[0])+"\n", "\tdisk "+quote(targets[0])+"\n\tdisk "+quote(p)+"\n", 1)
				}
				step(w, "Trage %s in vm.conf ein", vm)
				if err := h.appendVM(ctx, block); err != nil {
					return err
				}
			}
			if r.StartNow {
				h.startVM(w, vm)
			}
			step(w, "Wiederherstellung abgeschlossen.")
			return nil
		},
	}, nil
}
