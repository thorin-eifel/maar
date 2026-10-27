package obsd

import (
	"context"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type VMDisk struct {
	Path    string   `json:"path"`
	Name    string   `json:"name"`
	Format  string   `json:"format"`  // qcow2, raw
	Size    int64    `json:"size"`    // belegt auf dem Datenträger
	Virtual int64    `json:"virtual"` // Größe aus Sicht der VM
	Backing []string `json:"backing"` // Kette: Basisimages von oben nach unten
	Store   string   `json:"store"`   // Datastore, "" = lokal
	Missing bool     `json:"missing"`
}

type VMBackupJob struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Target   string   `json:"target"`
	Mode     string   `json:"mode"`
	Schedule Schedule `json:"schedule"`
	Keep     int      `json:"keep"`
	Enabled  bool     `json:"enabled"`
	LastRun  int64    `json:"lastRun"`
	LastOK   string   `json:"lastState,omitempty"`
}

type VMDetail struct {
	VM        *VM           `json:"vm"`
	Disks     []VMDisk      `json:"disks"`
	Snapshots int           `json:"snapshots"`
	Backups   []VMBackupJob `json:"backups"`
	NICs      []VMNIC       `json:"nics"`
	Groups    []PortGroup   `json:"portGroups"`
	Policy    VMPolicy      `json:"policy"`
	Rules     []*FWRule     `json:"rules"`
	Template  string        `json:"template,omitempty"` // Template, auf dem die Disk aufbaut
}

func qcow2Virtual(p string) int64 {
	f, err := os.Open(p)
	if err != nil {
		return 0
	}
	defer f.Close()
	var hdr [32]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil || string(hdr[:4]) != "QFI\xfb" {
		return 0
	}
	return int64(binary.BigEndian.Uint64(hdr[24:32]))
}

func (h *Host) diskInfo(p string) VMDisk {
	d := VMDisk{Path: p, Name: filepath.Base(p), Format: "raw", Backing: []string{}}
	st, err := os.Stat(h.P(p))
	if err != nil {
		d.Missing = true
		return d
	}
	d.Size, d.Virtual = st.Size(), st.Size()
	if v := qcow2Virtual(h.P(p)); v > 0 {
		d.Format, d.Virtual = "qcow2", v
		if ch := h.backingChain(p); len(ch) > 0 {
			d.Backing = ch
		}
	}
	for _, ds := range h.datastoreMounts() {
		if strings.HasPrefix(p, ds.Mount+"/") {
			d.Store = ds.Name
		}
	}
	return d
}

// VMDetail sammelt alles für die Detailseite einer VM.
func (h *Host) VMDetail(ctx context.Context, name string) (*VMDetail, error) {
	if err := checkName("VM", name, reName); err != nil {
		return nil, err
	}
	ov, err := h.VMs(ctx)
	if err != nil {
		return nil, err
	}
	d := &VMDetail{Disks: []VMDisk{}, Backups: []VMBackupJob{}, NICs: []VMNIC{}, Groups: []PortGroup{}, Rules: []*FWRule{}, Policy: VMPolicy{In: "allow", Out: "allow"}}
	for _, v := range ov.VMs {
		if v.Name == name {
			d.VM = v
		}
	}
	if d.VM == nil {
		return nil, invalid("VM %q gibt es nicht", name)
	}
	d.Snapshots = d.VM.Snaps
	if d.VM.Conf != nil {
		for _, p := range d.VM.Conf.Disks {
			di := h.diskInfo(p)
			d.Disks = append(d.Disks, di)
			if d.Template == "" {
				for _, b := range di.Backing {
					if strings.HasPrefix(b, h.templateDir()+"/") {
						d.Template = strings.TrimSuffix(filepath.Base(b), ".qcow2")
					}
				}
			}
		}
	}
	if bc, err := h.BackupConfig(); err == nil {
		for _, j := range bc.Jobs {
			if len(j.VMs) > 0 && !contains(j.VMs, name) {
				continue
			}
			bj := VMBackupJob{ID: j.ID, Name: j.Name, Target: j.Target, Mode: j.Mode, Schedule: j.Schedule, Keep: j.Keep, Enabled: j.Enabled, LastRun: bc.LastRun[j.ID]}
			for i := len(bc.History) - 1; i >= 0; i-- {
				if bc.History[i].Job == j.ID {
					bj.LastOK = bc.History[i].State
					break
				}
			}
			d.Backups = append(d.Backups, bj)
		}
	}
	if net, err := h.VNet(ctx); err == nil {
		for _, n := range net.NICs {
			if n.VM == name {
				d.NICs = append(d.NICs, n)
			}
		}
		used := map[string]bool{}
		for _, n := range d.NICs {
			used[n.Switch] = true
		}
		for _, pg := range net.PortGroups {
			if used[pg.Name] {
				d.Groups = append(d.Groups, *pg.PortGroup)
			}
		}
		d.Policy = net.Policies[name]
		if r := net.Rules[name]; r != nil {
			d.Rules = r
		}
	}
	return d, nil
}

// InventoryNames liefert Namen für den Inventarbaum: Templates, Datastores, Portgruppen.
func (h *Host) InventoryNames(ctx context.Context) (templates, datastores, portGroups []string) {
	templates, datastores, portGroups = []string{}, []string{}, []string{}
	if ts, err := h.Templates(); err == nil {
		for _, t := range ts {
			templates = append(templates, t.Name)
		}
	}
	for _, d := range h.datastoreMounts() {
		datastores = append(datastores, d.Name)
	}
	if c, err := h.loadNetConfig(); err == nil {
		for _, p := range c.PortGroups {
			portGroups = append(portGroups, p.Name)
		}
	}
	return
}
