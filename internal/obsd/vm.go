package obsd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const vmConfPath = "/etc/vm.conf"

type VM struct {
	ID      string  `json:"id"`
	PID     string  `json:"pid"`
	VCPUs   int     `json:"vcpus"`
	MaxMem  string  `json:"maxmem"`
	CurMem  string  `json:"curmem"`
	TTY     string  `json:"tty"`
	OwnerRT string  `json:"owner"`
	State   string  `json:"state"`
	Name    string  `json:"name"`
	Conf    *VMConf `json:"conf,omitempty"`
	Snaps   int     `json:"snapshots"`
}

type VMOverview struct {
	VMs      []*VM         `json:"vms"`
	Switches []*SwitchConf `json:"switches"`
	VMDRun   bool          `json:"vmdRunning"`
	VMDOn    bool          `json:"vmdEnabled"`
	Images   []VMImage     `json:"images"`
	ISOs     []VMImage     `json:"isos"`
	// Datastores sind eingehängte Datenträger aus geteiltem Speicher, auf denen Images liegen dürfen.
	Datastores []string `json:"datastores"`
	VMDir      string   `json:"vmDir"`
	ConfErr    string   `json:"confError,omitempty"`
}

type VMImage struct {
	Path   string   `json:"path"`
	Name   string   `json:"name"`
	Size   int64    `json:"size"`
	UsedBy []string `json:"usedBy"`
}

// vmctlStatus parst "vmctl status":
//
//	ID   PID VCPUS  MAXMEM  CURMEM     TTY        OWNER    STATE   NAME
//	 1 12345     1    1.0G    1.0G   ttyp0         root  running   web01
func (h *Host) vmctlStatus(ctx context.Context) ([]*VM, error) {
	out, err := h.out(ctx, "vmctl", "status")
	if err != nil {
		return nil, err
	}
	var vms []*VM
	for i, l := range lines(out) {
		f := strings.Fields(l)
		if i == 0 || len(f) < 9 {
			continue
		}
		vm := &VM{ID: f[0], PID: f[1], MaxMem: f[3], CurMem: f[4], TTY: f[5], OwnerRT: f[6], State: f[7], Name: strings.Join(f[8:], " ")}
		vm.VCPUs, _ = strconv.Atoi(f[2])
		vms = append(vms, vm)
	}
	return vms, nil
}

func (h *Host) loadVMConf() (*VMConfFile, error) {
	src, err := h.readFile(vmConfPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return ParseVMConf(src), nil
}

func (h *Host) VMs(ctx context.Context) (*VMOverview, error) {
	ov := &VMOverview{VMDir: h.VMDir, VMs: []*VM{}, Switches: []*SwitchConf{}}
	ov.VMDOn = h.run(ctx, "rcctl", "get", "vmd", "status") == nil
	ov.VMDRun = h.run(ctx, "rcctl", "check", "vmd") == nil
	conf, err := h.loadVMConf()
	if err != nil {
		ov.ConfErr = err.Error()
		conf = &VMConfFile{}
	}
	ov.Switches = append(ov.Switches, conf.Switches...)
	seen := map[string]bool{}
	if ov.VMDRun {
		st, err := h.vmctlStatus(ctx)
		if err != nil {
			return nil, err
		}
		for _, vm := range st {
			vm.Conf = conf.VM(vm.Name)
			seen[vm.Name] = true
			ov.VMs = append(ov.VMs, vm)
		}
	}
	for _, c := range conf.VMs {
		if !seen[c.Name] {
			ov.VMs = append(ov.VMs, &VM{ID: "-", PID: "-", Name: c.Name, State: "stopped", MaxMem: c.Memory, Conf: c, TTY: "-"})
		}
	}
	sort.SliceStable(ov.VMs, func(a, b int) bool { return ov.VMs[a].Name < ov.VMs[b].Name })
	for _, vm := range ov.VMs {
		vm.Snaps = h.snapshotCount(vm.Name)
	}

	used := map[string][]string{}
	for _, c := range conf.VMs {
		for _, d := range c.Disks {
			used[d] = append(used[d], c.Name)
		}
		if c.CDROM != "" {
			used[c.CDROM] = append(used[c.CDROM], c.Name)
		}
	}
	ov.Images = h.listImages(h.VMDir, []string{".qcow2", ".img", ".raw"}, used)
	ov.Datastores = []string{}
	for _, ds := range h.datastoreMounts() {
		if h.isMounted(ctx, ds.Mount) {
			ov.Datastores = append(ov.Datastores, ds.Name)
			ov.Images = append(ov.Images, h.listImages(ds.Mount, []string{".qcow2", ".img", ".raw"}, used)...)
		}
	}
	ov.ISOs = h.listImages(filepath.Join(h.VMDir, "iso"), []string{".iso", ".img", ".fs"}, used)
	return ov, nil
}

func (h *Host) listImages(dir string, exts []string, used map[string][]string) []VMImage {
	list := []VMImage{}
	ents, err := os.ReadDir(h.P(dir))
	if err != nil {
		return list
	}
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		ok := false
		for _, x := range exts {
			ok = ok || ext == x
		}
		if !ok {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		p := filepath.Join(dir, e.Name())
		u := used[p]
		if u == nil {
			u = []string{}
		}
		list = append(list, VMImage{Path: p, Name: e.Name(), Size: info.Size(), UsedBy: u})
	}
	return list
}

// VMAction führt Lebenszyklus-Aktionen aus.
func (h *Host) VMAction(ctx context.Context, name, action string, opt map[string]string) error {
	if err := checkName("VM", name, reName); err != nil {
		return err
	}
	switch action {
	case "start":
		args := []string{"start"}
		if iso := opt["cdrom"]; iso != "" {
			if err := h.checkVMPath(iso); err != nil {
				return err
			}
			args = append(args, "-r", iso, "-B", "cdrom")
		}
		return h.run(ctx, "vmctl", append(args, name)...)
	case "stop":
		h.ExpectStop(name)
		return h.run(ctx, "vmctl", "stop", name)
	case "kill":
		h.ExpectStop(name)
		return h.run(ctx, "vmctl", "stop", "-f", name)
	case "pause":
		return h.run(ctx, "vmctl", "pause", name)
	case "unpause":
		return h.run(ctx, "vmctl", "unpause", name)
	}
	return invalid("Aktion %q", action)
}

// checkVMPath erlaubt nur Dateien unterhalb des VM-Verzeichnisses.
func (h *Host) checkVMPath(p string) error {
	clean := filepath.Clean(p)
	if clean != p || !strings.HasPrefix(clean, h.VMDir+"/") || strings.ContainsAny(p, "\"\n\\") {
		return invalid("Pfad %q liegt nicht unter %s", p, h.VMDir)
	}
	return nil
}

// validateVMSpec prüft Name, Speicher, vCPUs, SEV, Besitzer und Switches.
func (h *Host) validateVMSpec(s *VMSpec) error {
	if err := checkName("VM-Name", s.Name, reName); err != nil {
		return err
	}
	if err := checkName("Arbeitsspeicher", s.Memory, reSize); err != nil {
		return err
	}
	if s.CPUs == 0 {
		s.CPUs = 1
	}
	if s.CPUs < 1 || s.CPUs > 64 {
		return invalid("vCPUs %d (erlaubt 1–64)", s.CPUs)
	}
	if s.SEV != "" && s.SEV != "sev" && s.SEV != "seves" {
		return invalid("SEV-Modus %q", s.SEV)
	}
	if s.Owner != "" {
		if err := checkName("Besitzer", s.Owner, reOwner); err != nil {
			return err
		}
	}
	for _, sw := range s.Switches {
		if err := checkName("Switch", sw, reName); err != nil {
			return err
		}
	}
	return nil
}

func (h *Host) CreateVM(ctx context.Context, s *VMSpec) error {
	if err := h.validateVMSpec(s); err != nil {
		return err
	}
	h.confMu.Lock()
	defer h.confMu.Unlock()
	conf, err := h.loadVMConf()
	if err != nil {
		return err
	}
	if conf.VM(s.Name) != nil {
		return invalid("VM %q existiert bereits", s.Name)
	}
	if s.CDROM != "" {
		if err := h.checkVMPath(s.CDROM); err != nil {
			return err
		}
	}
	if s.DiskSize != "" || s.BaseDisk != "" {
		if s.DiskFmt != "raw" {
			s.DiskFmt = "qcow2"
		}
		s.DiskPath = filepath.Join(h.VMDir, s.Name+"."+s.DiskFmt)
		if _, err := os.Stat(h.P(s.DiskPath)); err == nil {
			return invalid("Disk %s existiert bereits", s.DiskPath)
		}
		args := []string{"create"}
		if s.BaseDisk != "" {
			if err := h.checkVMPath(s.BaseDisk); err != nil {
				return err
			}
			args = append(args, "-b", s.BaseDisk)
		} else if err := checkName("Diskgröße", s.DiskSize, reSize); err != nil {
			return err
		} else {
			args = append(args, "-s", s.DiskSize)
		}
		if err := h.run(ctx, "vmctl", append(args, s.DiskPath)...); err != nil {
			return err
		}
	} else if s.DiskPath != "" {
		if err := h.checkVMPath(s.DiskPath); err != nil {
			return err
		}
	}
	if _, err := h.WriteConfig(ctx, vmConfPath, conf.Append(s.Block()), 0); err != nil {
		return err
	}
	if err := h.run(ctx, "vmctl", "reload"); err != nil {
		return fmt.Errorf("vm.conf gespeichert, aber vmctl reload schlug fehl: %w", err)
	}
	if s.StartNow {
		return h.run(ctx, "vmctl", "start", s.Name)
	}
	return nil
}

// UpdateVMBlock ersetzt den vm.conf-Block einer VM durch eigenen Text.
func (h *Host) UpdateVMBlock(ctx context.Context, name, block string) error {
	h.confMu.Lock()
	defer h.confMu.Unlock()
	conf, err := h.loadVMConf()
	if err != nil {
		return err
	}
	parsed := ParseVMConf(block)
	if len(parsed.VMs) != 1 || parsed.VMs[0].Name != name || len(parsed.Switches) != 0 {
		return invalid("der Block muss genau eine VM namens %q definieren", name)
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

func (h *Host) DeleteVM(ctx context.Context, name string, deleteDisks bool) error {
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
	if st, err := h.vmctlStatus(ctx); err == nil {
		for _, s := range st {
			if s.Name == name && s.State != "stopped" {
				return invalid("VM %q läuft noch – bitte zuerst stoppen", name)
			}
		}
	}
	src, _ := conf.ReplaceVM(name, "")
	if _, err := h.WriteConfig(ctx, vmConfPath, src, 0); err != nil {
		return err
	}
	if err := h.run(ctx, "vmctl", "reload"); err != nil {
		return err
	}
	if deleteDisks {
		for _, d := range v.Disks {
			if h.checkVMPath(d) == nil {
				os.Remove(h.P(d))
			}
		}
	}
	if deleteDisks {
		os.RemoveAll(h.P(h.snapDir(name)))
	}
	// Die Seed-ISO der Gastanpassung gehört allein zu dieser VM.
	if strings.HasPrefix(v.CDROM, h.seedDir()+"/") {
		os.Remove(h.P(v.CDROM))
	}
	return nil
}

// CreateDisk legt ein Disk-Image im VM-Verzeichnis oder (store) in einem Datastore an.
func (h *Host) CreateDisk(ctx context.Context, name, size, format, base, store string) error {
	if err := checkName("Dateiname", name, reName); err != nil {
		return err
	}
	dir := h.VMDir
	if store != "" {
		var mp string
		for _, d := range h.datastoreMounts() {
			if d.Name == store {
				mp = d.Mount
			}
		}
		if mp == "" {
			return invalid("Datastore %q gibt es nicht", store)
		}
		if !h.isMounted(ctx, mp) {
			return invalid("Datastore %q ist nicht eingehängt", store)
		}
		dir = mp
	}
	if format != "raw" {
		format = "qcow2"
	}
	p := filepath.Join(dir, strings.TrimSuffix(name, "."+format)+"."+format)
	args := []string{"create"}
	if base != "" {
		if err := h.checkVMPath(base); err != nil {
			return err
		}
		args = append(args, "-b", base)
	} else {
		if err := checkName("Größe", size, reSize); err != nil {
			return err
		}
		args = append(args, "-s", size)
	}
	return h.run(ctx, "vmctl", append(args, p)...)
}

func (h *Host) DeleteImage(ctx context.Context, p string) error {
	if err := h.checkVMPath(p); err != nil {
		return err
	}
	conf, _ := h.loadVMConf()
	for _, v := range conf.VMs {
		for _, d := range append(v.Disks, v.CDROM) {
			if d == p {
				return invalid("%s wird noch von VM %q verwendet", p, v.Name)
			}
		}
	}
	return os.Remove(h.P(p))
}

// ISOPath liefert den Zielpfad für ISO-Downloads und Uploads.
func (h *Host) ISOPath(name string) (string, error) {
	name = filepath.Base(name)
	if !reName.MatchString(name) {
		return "", invalid("Dateiname %q", name)
	}
	ext := strings.ToLower(filepath.Ext(name))
	if ext != ".iso" && ext != ".img" && ext != ".fs" {
		return "", invalid("nur .iso, .img oder .fs")
	}
	dir := filepath.Join(h.VMDir, "iso")
	if err := os.MkdirAll(h.P(dir), 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

func (h *Host) AddSwitch(ctx context.Context, name, iface string) error {
	if err := checkName("Switch", name, reName); err != nil {
		return err
	}
	if err := checkName("Interface", iface, reIface); err != nil {
		return err
	}
	h.confMu.Lock()
	defer h.confMu.Unlock()
	conf, err := h.loadVMConf()
	if err != nil {
		return err
	}
	for _, s := range conf.Switches {
		if s.Name == name {
			return invalid("Switch %q existiert bereits", name)
		}
	}
	block := fmt.Sprintf("switch %s {\n\tinterface %s\n}\n", quote(name), iface)
	// Switches gehören vor die VM-Blöcke, damit vmd sie beim Laden kennt.
	src := conf.Src
	if len(conf.VMs) > 0 {
		at := conf.VMs[0].start
		src = src[:at] + block + "\n" + src[at:]
	} else {
		src = conf.Append(block)
	}
	if _, err := h.WriteConfig(ctx, vmConfPath, src, 0); err != nil {
		return err
	}
	return h.run(ctx, "vmctl", "reload")
}

// VMTTY liefert das Terminalgerät einer laufenden VM (z. B. /dev/ttyp3).
func (h *Host) VMTTY(ctx context.Context, name string) (string, error) {
	st, err := h.vmctlStatus(ctx)
	if err != nil {
		return "", err
	}
	for _, v := range st {
		if v.Name == name || v.ID == name {
			if v.State != "running" || v.TTY == "-" || v.TTY == "" {
				return "", invalid("VM %q läuft nicht", name)
			}
			if !strings.HasPrefix(v.TTY, "tty") {
				return "", fmt.Errorf("unerwartetes TTY %q", v.TTY)
			}
			return "/dev/" + v.TTY, nil
		}
	}
	return "", invalid("VM %q nicht gefunden", name)
}
