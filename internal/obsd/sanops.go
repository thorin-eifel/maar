package obsd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var reDUID = regexp.MustCompile(`(?m)^duid:\s*([0-9a-f]{16})\s*$`)

// ---- iSCSI-Ziele ----

// SaveISCSITarget legt ein Ziel an (old leer) oder ändert es.
func (h *Host) SaveISCSITarget(ctx context.Context, old string, t *ISCSITarget) (*Task, error) {
	if err := t.validate(); err != nil {
		return nil, err
	}
	check := func(c *SANConfig) error {
		for _, x := range c.Targets {
			if x.Name == t.Name && x.Name != old {
				return invalid("Ziel %q existiert schon", t.Name)
			}
			if old == "" && x.Addr == t.Addr && x.IQN == t.IQN && x.Port == t.Port && x.Discovery == t.Discovery {
				return invalid("Dieses Ziel gibt es schon als %q", x.Name)
			}
		}
		if old != "" && t.Name != old {
			return invalid("Der Name lässt sich nicht ändern – Ziel löschen und neu anlegen")
		}
		if old != "" {
			found := false
			for _, x := range c.Targets {
				found = found || x.Name == old
			}
			if !found {
				return invalid("Ziel %q gibt es nicht", old)
			}
		}
		return nil
	}
	cfg, err := h.loadSAN()
	if err != nil {
		return nil, err
	}
	if err := check(cfg); err != nil {
		return nil, err
	}
	title := "iSCSI-Ziel " + t.Name + " anlegen"
	if old != "" {
		title = "iSCSI-Ziel " + t.Name + " ändern"
	}
	in := *t
	return &Task{Title: title, Command: "maar iscsi", Run: func(ctx context.Context, w io.Writer) error {
		h.sanMu.Lock()
		defer h.sanMu.Unlock()
		cfg, err := h.loadSAN()
		if err != nil {
			return err
		}
		if err := check(cfg); err != nil {
			return err
		}
		if old != "" {
			for i, x := range cfg.Targets {
				if x.Name == old {
					cfg.Targets[i] = &in
				}
			}
		} else {
			cfg.Targets = append(cfg.Targets, &in)
		}
		return h.applyISCSI(ctx, w, cfg)
	}}, nil
}

func (h *Host) DeleteISCSITarget(ctx context.Context, name string) (*Task, error) {
	cfg, err := h.loadSAN()
	if err != nil {
		return nil, err
	}
	idx := -1
	for i, x := range cfg.Targets {
		if x.Name == name {
			idx = i
		}
	}
	if idx < 0 {
		return nil, invalid("Ziel %q gibt es nicht", name)
	}
	if err := h.noMountedOn(ctx, "iscsi"); err != nil {
		return nil, err
	}
	return &Task{Title: "iSCSI-Ziel " + name + " löschen", Command: "maar iscsi", Run: func(ctx context.Context, w io.Writer) error {
		h.sanMu.Lock()
		defer h.sanMu.Unlock()
		cfg, err := h.loadSAN()
		if err != nil {
			return err
		}
		keep := cfg.Targets[:0]
		for _, x := range cfg.Targets {
			if x.Name != name {
				keep = append(keep, x)
			}
		}
		cfg.Targets = keep
		return h.applyISCSI(ctx, w, cfg)
	}}, nil
}

// noMountedOn verweigert, solange ein Datastore auf einer Disk dieses Transports eingehängt ist.
func (h *Host) noMountedOn(ctx context.Context, transport string) error {
	o, err := h.SAN(ctx)
	if err != nil {
		return err
	}
	for _, d := range o.Disks {
		if d.Transport == transport && len(d.Mounts) > 0 {
			return invalid("%s ist noch eingehängt (%s) – Datastore zuerst aushängen", d.Name, strings.Join(d.Mounts, ", "))
		}
	}
	return nil
}

func (h *Host) applyISCSI(ctx context.Context, w io.Writer, cfg *SANConfig) error {
	cur, _ := h.readFile(iscsiConfPath)
	if _, err := h.putFile(ctx, w, iscsiConfPath, iscsiConf(cur, cfg.Targets)); err != nil {
		return err
	}
	if err := h.saveSAN(cfg); err != nil {
		return err
	}
	if len(cfg.Targets) == 0 && h.run(ctx, "rcctl", "check", "iscsid") != nil {
		return nil // nichts zu tun: kein Ziel, iscsid läuft nicht
	}
	if h.run(ctx, "rcctl", "get", "iscsid", "status") != nil {
		step(w, "iscsid aktivieren")
		if err := h.cmd(ctx, w, "rcctl", "enable", "iscsid"); err != nil {
			return err
		}
	}
	if h.run(ctx, "rcctl", "check", "iscsid") != nil {
		step(w, "iscsid starten")
		return h.cmd(ctx, w, "rcctl", "start", "iscsid")
	}
	step(w, "Konfiguration neu laden")
	return h.cmd(ctx, w, "iscsictl", "reload")
}

// ---- Datastores ----

var reDSName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,30}$`)

// InitDatastore initialisiert eine freie SAN-Disk (GPT, Label, FFS) und hängt sie unter /var/vmd/<name> ein.
func (h *Host) InitDatastore(ctx context.Context, disk, name string) (*Task, error) {
	if !reDiskSD.MatchString(disk) {
		return nil, invalid("Disk %q", disk)
	}
	if !reDSName.MatchString(name) || name == "iso" || name == "templates" || name == "snapshots" || name == "seed" {
		return nil, invalid("Datastore-Name %q (Kleinbuchstaben, Ziffern, Bindestrich)", name)
	}
	pre := func(ctx context.Context) (*SANDisk, *SANConfig, error) {
		o, err := h.SAN(ctx)
		if err != nil {
			return nil, nil, err
		}
		cfg, err := h.loadSAN()
		if err != nil {
			return nil, nil, err
		}
		for _, d := range cfg.Datastores {
			if d.Name == name {
				return nil, nil, invalid("Datastore %q existiert schon", name)
			}
		}
		if _, err := os.Stat(h.P(filepath.Join(h.VMDir, name))); err == nil {
			return nil, nil, invalid("%s/%s gibt es schon", h.VMDir, name)
		}
		for i := range o.Disks {
			if o.Disks[i].Name == disk {
				if !o.Disks[i].CanInit {
					return nil, nil, invalid("%s lässt sich nicht initialisieren: %s", disk, o.Disks[i].Reason)
				}
				return &o.Disks[i], cfg, nil
			}
		}
		return nil, nil, invalid("Disk %s gibt es nicht", disk)
	}
	if _, _, err := pre(ctx); err != nil {
		return nil, err
	}
	return &Task{Title: "Datastore " + name + " auf " + disk + " anlegen", Command: "fdisk · disklabel · newfs", Run: func(ctx context.Context, w io.Writer) error {
		h.sanMu.Lock()
		defer h.sanMu.Unlock()
		dk, cfg, err := pre(ctx)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "ACHTUNG: %s (%s %s) wird überschrieben.\n", disk, dk.Vendor, dk.Model)
		step(w, "GPT anlegen")
		if err := h.cmd(ctx, w, "fdisk", "-iyg", disk); err != nil {
			return err
		}
		step(w, "Partition a über die ganze Disk")
		fmt.Fprintf(w, "$ disklabel -E %s\n", disk)
		if r, err := h.R.Run(ctx, []byte("a a\n\n\n\nw\nq\n"), "disklabel", "-E", disk); err != nil {
			return fmt.Errorf("disklabel: %w\n%s", err, r.Stderr)
		}
		lab, err := h.out(ctx, "disklabel", disk)
		if err != nil {
			return err
		}
		m := reDUID.FindStringSubmatch(lab)
		if m == nil || strings.Trim(m[1], "0") == "" {
			return fmt.Errorf("keine DUID auf %s gefunden – Label wurde nicht geschrieben", disk)
		}
		duid := m[1]
		step(w, "Dateisystem anlegen (DUID %s)", duid)
		if err := h.cmd(ctx, w, "newfs", "-q", disk+"a"); err != nil {
			return err
		}
		ds := &Datastore{Name: name, Disk: disk, DUID: duid, Part: "a", Mount: filepath.Join(h.VMDir, name)}
		step(w, "Einhängepunkt %s und /etc/fstab", ds.Mount)
		if err := os.MkdirAll(h.P(ds.Mount), 0o755); err != nil {
			return err
		}
		if err := h.setFstab(ctx, ds, true); err != nil {
			return err
		}
		cfg.Datastores = append(cfg.Datastores, ds)
		if err := h.saveSAN(cfg); err != nil {
			return err
		}
		step(w, "Einhängen")
		if err := h.cmd(ctx, w, "mount", ds.Mount); err != nil {
			return err
		}
		fmt.Fprintf(w, "Datastore %s ist bereit. Ein FFS-Dateisystem darf immer nur von EINEM Host eingehängt sein.\n", name)
		return nil
	}}, nil
}

const fstabMark = "# maar datastore "

// setFstab trägt den Datastore ein (add) oder entfernt ihn. Die Zeile bleibt noauto:
// iSCSI-Disks sind beim Booten noch nicht da.
func (h *Host) setFstab(ctx context.Context, ds *Datastore, add bool) error {
	cur, err := h.readFile("/etc/fstab")
	if err != nil {
		return err
	}
	var out []string
	skip := false
	for _, l := range strings.Split(strings.TrimRight(cur, "\n"), "\n") {
		if strings.TrimSpace(l) == fstabMark+ds.Name {
			skip = true
			continue
		}
		if skip {
			skip = false
			continue
		}
		out = append(out, l)
	}
	if add {
		out = append(out, fstabMark+ds.Name, fmt.Sprintf("%s.%s %s ffs rw,noauto,nodev,nosuid,noatime 0 0", ds.DUID, ds.Part, ds.Mount))
	}
	_, err = h.WriteConfig(ctx, "/etc/fstab", strings.Join(out, "\n")+"\n", 0)
	return err
}

func (h *Host) datastore(name string) (*SANConfig, *Datastore, error) {
	cfg, err := h.loadSAN()
	if err != nil {
		return nil, nil, err
	}
	for _, d := range cfg.Datastores {
		if d.Name == name {
			return cfg, d, nil
		}
	}
	return nil, nil, invalid("Datastore %q gibt es nicht", name)
}

func (h *Host) isMounted(ctx context.Context, mp string) bool {
	_, ok := reverse(mountTable(h.outLenient(ctx, "mount")), mp)
	return ok
}

func (h *Host) MountDatastore(ctx context.Context, name string) error {
	h.sanMu.Lock()
	defer h.sanMu.Unlock()
	_, ds, err := h.datastore(name)
	if err != nil {
		return err
	}
	if h.isMounted(ctx, ds.Mount) {
		return invalid("%s ist schon eingehängt", name)
	}
	return h.run(ctx, "mount", ds.Mount)
}

func (h *Host) UnmountDatastore(ctx context.Context, name string) error {
	h.sanMu.Lock()
	defer h.sanMu.Unlock()
	_, ds, err := h.datastore(name)
	if err != nil {
		return err
	}
	if busy := h.vmsOn(ctx, ds.Mount, true); len(busy) > 0 {
		return invalid("Auf %s laufen noch VMs: %s", name, strings.Join(busy, ", "))
	}
	if !h.isMounted(ctx, ds.Mount) {
		return invalid("%s ist nicht eingehängt", name)
	}
	return h.run(ctx, "umount", ds.Mount)
}

// RemoveDatastore löst den Datastore aus maar. Die Daten auf der Disk bleiben unberührt.
func (h *Host) RemoveDatastore(ctx context.Context, name string) error {
	h.sanMu.Lock()
	defer h.sanMu.Unlock()
	cfg, ds, err := h.datastore(name)
	if err != nil {
		return err
	}
	if users := h.vmsOn(ctx, ds.Mount, false); len(users) > 0 {
		return invalid("VMs verwenden noch Disks auf %s: %s", name, strings.Join(users, ", "))
	}
	if h.isMounted(ctx, ds.Mount) {
		if err := h.run(ctx, "umount", ds.Mount); err != nil {
			return err
		}
	}
	if err := h.setFstab(ctx, ds, false); err != nil {
		return err
	}
	keep := cfg.Datastores[:0]
	for _, d := range cfg.Datastores {
		if d.Name != name {
			keep = append(keep, d)
		}
	}
	cfg.Datastores = keep
	if err := h.saveSAN(cfg); err != nil {
		return err
	}
	os.Remove(h.P(ds.Mount)) // nur, wenn leer
	return nil
}

// datastoreMounts liefert die Einhängepunkte aller Datastores (für Images und Disk-Anlage).
func (h *Host) datastoreMounts() []*Datastore {
	cfg, err := h.loadSAN()
	if err != nil {
		return nil
	}
	return cfg.Datastores
}
