package obsd

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Strukturierte VM-Einstellungen: Die Oberfläche bietet nur an, was auf diesem
// Host existiert (Images, ISOs, Konten, CPUs, Speicher); vm.conf wird im
// Hintergrund geschrieben und von vmd -n geprüft.

var reVMMem = regexp.MustCompile(`^[0-9]{1,6}[MG]$`)

type BootOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type VMOptions struct {
	Current    VMEdit            `json:"current"`
	Running    bool              `json:"running"`
	HostCPUs   int               `json:"hostCpus"`
	MaxCPUs    int               `json:"maxCpus"`
	HostMem    int64             `json:"hostMem"`
	SEV        bool              `json:"sevAvailable"`
	SEVReason  string            `json:"sevReason,omitempty"`
	Images     []VMImage         `json:"images"` // frei oder von dieser VM belegt
	ISOs       []VMImage         `json:"isos"`
	Users      []string          `json:"users"`
	Groups     []string          `json:"groups"`
	Boot       []BootOption      `json:"boot"`
	Switches   []string          `json:"switches"`
	Datastores []string          `json:"datastores"`
	VMDir      string            `json:"vmDir"`
	Formats    map[string]string `json:"formats"`
}

// VMOptions liefert die Auswahlmöglichkeiten; name leer = für eine neue VM.
func (h *Host) VMOptions(ctx context.Context, name string) (*VMOptions, error) {
	ov, err := h.VMs(ctx)
	if err != nil {
		return nil, err
	}
	o := &VMOptions{MaxCPUs: 64, Images: []VMImage{}, ISOs: ov.ISOs, Users: []string{}, Groups: []string{}, Switches: []string{}, VMDir: h.VMDir,
		Datastores: ov.Datastores, Current: VMEdit{Memory: "1G", CPUs: 1, Enabled: true, Disks: []string{}}, Formats: map[string]string{}}
	if o.Datastores == nil {
		o.Datastores = []string{}
	}
	if name != "" {
		found := false
		for _, v := range ov.VMs {
			if v.Name == name {
				found = true
				o.Running = v.State == "running" || v.State == "paused"
				if v.Conf == nil {
					return nil, invalid("VM %q ist nicht in vm.conf definiert", name)
				}
				o.Current = v.Conf.Edit()
			}
		}
		if !found {
			return nil, invalid("VM %q gibt es nicht", name)
		}
	}
	for _, i := range ov.Images {
		if len(i.UsedBy) == 0 || (name != "" && len(i.UsedBy) == 1 && i.UsedBy[0] == name) {
			o.Images = append(o.Images, i)
		}
		o.Formats[i.Path] = h.diskInfo(i.Path).Format
	}
	for _, s := range ov.Switches {
		o.Switches = append(o.Switches, s.Name)
	}
	if sys, err := h.Overview(ctx); err == nil {
		o.HostCPUs, o.HostMem = sys.NCPU, sys.PhysMem
		o.SEV = strings.Contains(sys.Model, "AMD")
		if !o.SEV {
			o.SEVReason = "Nur AMD-Prozessoren unterstützen SEV (psp(4))."
		}
	}
	if us, err := h.Users(); err == nil {
		for _, u := range us {
			if !u.System || u.UID == 0 {
				o.Users = append(o.Users, u.Name)
			}
		}
	}
	if gs, err := h.Groups(); err == nil {
		for _, g := range gs {
			if g.GID >= 1000 || g.Name == "wheel" || g.Name == "staff" || g.GID == 0 {
				o.Groups = append(o.Groups, g.Name)
			}
		}
	}
	o.Boot = []BootOption{{"", "Standard (BIOS-Reihenfolge)"}, {"disk", "Festplatte"}, {"cdrom", "CD-ROM"}, {"net", "Netzwerk (autoinstall)"}}
	return o, nil
}

func (h *Host) validateVMEdit(ctx context.Context, name string, e *VMEdit) error {
	if !reVMMem.MatchString(e.Memory) {
		return invalid("Arbeitsspeicher %q (z. B. 512M oder 4G)", e.Memory)
	}
	if mb := parseSize(e.Memory) >> 20; mb < 64 {
		return invalid("Arbeitsspeicher: mindestens 64M")
	}
	if sys, err := h.Overview(ctx); err == nil && sys.PhysMem > 0 && parseSize(e.Memory) > sys.PhysMem {
		return invalid("Arbeitsspeicher %s übersteigt den Hostspeicher (%d MB)", e.Memory, sys.PhysMem>>20)
	}
	if e.CPUs < 1 || e.CPUs > 64 {
		return invalid("vCPUs %d (1–64)", e.CPUs)
	}
	if e.SEV != "" && e.SEV != "sev" && e.SEV != "seves" {
		return invalid("SEV-Modus %q", e.SEV)
	}
	if e.SEV != "" {
		if sys, err := h.Overview(ctx); err == nil && !strings.Contains(sys.Model, "AMD") {
			return invalid("SEV gibt es nur auf AMD-Prozessoren")
		}
	}
	if len(e.Disks) > 8 {
		return invalid("höchstens 8 Disks")
	}
	conf, err := h.loadVMConf()
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, d := range e.Disks {
		if err := h.checkVMPath(d); err != nil {
			return err
		}
		if _, err := os.Stat(h.P(d)); err != nil {
			return invalid("Disk %s gibt es nicht", d)
		}
		if seen[d] {
			return invalid("Disk %s ist doppelt angegeben", d)
		}
		seen[d] = true
		for _, v := range conf.VMs {
			if v.Name == name {
				continue
			}
			for _, vd := range v.Disks {
				if vd == d {
					return invalid("Disk %s gehört schon zu VM %q", d, v.Name)
				}
			}
		}
	}
	if e.CDROM != "" {
		if err := h.checkVMPath(e.CDROM); err != nil {
			return err
		}
		if _, err := os.Stat(h.P(e.CDROM)); err != nil {
			return invalid("Medium %s gibt es nicht", e.CDROM)
		}
	}
	switch e.BootDevice {
	case "":
	case "cdrom":
		if e.CDROM == "" {
			return invalid("Booten von CD-ROM braucht ein Medium")
		}
	case "disk":
		if len(e.Disks) == 0 {
			return invalid("Booten von Festplatte braucht eine Disk")
		}
	case "net":
	default:
		return invalid("Boot-Gerät %q", e.BootDevice)
	}
	if e.Owner != "" {
		if !reOwner.MatchString(e.Owner) {
			return invalid("Besitzer %q", e.Owner)
		}
		user, group, _ := strings.Cut(e.Owner, ":")
		if user != "" {
			ok := false
			if us, err := h.Users(); err == nil {
				for _, u := range us {
					ok = ok || u.Name == user
				}
			}
			if !ok {
				return invalid("Benutzer %q gibt es nicht", user)
			}
		}
		if group != "" {
			ok := false
			if gs, err := h.Groups(); err == nil {
				for _, g := range gs {
					ok = ok || g.Name == group
				}
			}
			if !ok {
				return invalid("Gruppe %q gibt es nicht", group)
			}
		}
	}
	return nil
}

// UpdateVMSettings schreibt die Einstellungen in den vm.conf-Block der VM.
func (h *Host) UpdateVMSettings(ctx context.Context, name string, e *VMEdit) error {
	if err := checkName("VM", name, reName); err != nil {
		return err
	}
	if err := h.validateVMEdit(ctx, name, e); err != nil {
		return err
	}
	h.confMu.Lock()
	defer h.confMu.Unlock()
	conf, err := h.loadVMConf()
	if err != nil {
		return err
	}
	v := conf.VM(name)
	if v == nil {
		return invalid("VM %q ist nicht in vm.conf definiert", name)
	}
	formats := map[string]string{}
	for _, d := range e.Disks {
		formats[d] = h.diskInfo(d).Format
	}
	block := v.ApplyEdit(*e, formats)
	if p := ParseVMConf(block); len(p.VMs) != 1 || p.VMs[0].Name != name {
		return fmt.Errorf("interner Fehler: der erzeugte Block ist ungültig")
	}
	src, err := conf.ReplaceVM(name, block)
	if err != nil {
		return err
	}
	if _, err := h.WriteConfig(ctx, vmConfPath, src, 0); err != nil {
		return err
	}
	return h.run(ctx, "vmctl", "reload")
}
