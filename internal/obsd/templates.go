package obsd

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Templates sind schreibgeschützte qcow2-Basisimages unter <vmdir>/templates
// mit einer JSON-Beschreibung daneben. Bereitgestellt wird entweder als
// verknüpfter Klon (qcow2-Overlay, vmctl create -b) oder als Vollkopie
// (vmctl create -i).

type Template struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	OS          string   `json:"os"` // openbsd, linux, other
	Memory      string   `json:"memory"`
	CPUs        int      `json:"cpus"`
	Source      string   `json:"source"`
	Created     int64    `json:"created"`
	Disk        string   `json:"disk"`
	Size        int64    `json:"size"`
	Clones      []string `json:"clones"`
}

var templateOS = map[string]bool{"openbsd": true, "linux": true, "other": true}

func (h *Host) templateDir() string { return filepath.Join(h.VMDir, "templates") }
func (h *Host) seedDir() string     { return filepath.Join(h.VMDir, "seed") }

func (h *Host) templatePaths(name string) (disk, meta string) {
	return filepath.Join(h.templateDir(), name+".qcow2"), filepath.Join(h.templateDir(), name+".json")
}

// qcow2Backing liest den Pfad des Basisimages aus dem qcow2-Header.
func qcow2Backing(p string) string {
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	var hdr [20]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil || string(hdr[:4]) != "QFI\xfb" {
		return ""
	}
	off := binary.BigEndian.Uint64(hdr[8:16])
	size := binary.BigEndian.Uint32(hdr[16:20])
	if off == 0 || size == 0 || size > 1023 {
		return ""
	}
	b := make([]byte, size)
	if _, err := f.ReadAt(b, int64(off)); err != nil {
		return ""
	}
	return string(b)
}

func (h *Host) Templates() ([]*Template, error) {
	list := []*Template{}
	metas, _ := filepath.Glob(filepath.Join(h.P(h.templateDir()), "*.json"))
	conf, _ := h.loadVMConf()
	backing := map[string][]string{}
	if conf != nil {
		for _, v := range conf.VMs {
			// Die ganze Kette verfolgen: nach Snapshots liegt das Template tiefer.
			seen := map[string]bool{}
			for _, d := range v.Disks {
				for _, b := range h.backingChain(d) {
					if !seen[b] {
						seen[b] = true
						backing[b] = append(backing[b], v.Name)
					}
				}
			}
		}
	}
	for _, m := range metas {
		b, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		t := &Template{}
		if json.Unmarshal(b, t) != nil || !reName.MatchString(t.Name) {
			continue
		}
		t.Disk, _ = h.templatePaths(t.Name)
		if st, err := os.Stat(h.P(t.Disk)); err == nil {
			t.Size = st.Size()
		}
		t.Clones = backing[t.Disk]
		if t.Clones == nil {
			t.Clones = []string{}
		}
		list = append(list, t)
	}
	sort.Slice(list, func(a, b int) bool { return list[a].Name < list[b].Name })
	return list, nil
}

func (h *Host) template(name string) (*Template, error) {
	list, err := h.Templates()
	if err != nil {
		return nil, err
	}
	for _, t := range list {
		if t.Name == name {
			return t, nil
		}
	}
	return nil, invalid("Template %q existiert nicht", name)
}

// TemplateSpec beschreibt ein neues oder geändertes Template.
type TemplateSpec struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	OS          string `json:"os"`
	Memory      string `json:"memory"`
	CPUs        int    `json:"cpus"`
	URL         string `json:"url"`  // Import aus dem Netz
	Path        string `json:"path"` // Import eines Images unter <vmdir>
}

func (s *TemplateSpec) validate() error {
	if err := checkName("Template-Name", s.Name, reName); err != nil {
		return err
	}
	if s.OS == "" {
		s.OS = "other"
	}
	if !templateOS[s.OS] {
		return invalid("Betriebssystem %q", s.OS)
	}
	if strings.ContainsAny(s.Description, "\n\x00") || len(s.Description) > 300 {
		return invalid("Beschreibung")
	}
	if s.Memory == "" {
		s.Memory = "1G"
	}
	if err := checkName("Arbeitsspeicher", s.Memory, reSize); err != nil {
		return err
	}
	if s.CPUs == 0 {
		s.CPUs = 1
	}
	if s.CPUs < 1 || s.CPUs > 64 {
		return invalid("vCPUs %d", s.CPUs)
	}
	return nil
}

func (h *Host) writeTemplateMeta(t *Template) error {
	_, meta := h.templatePaths(t.Name)
	b, _ := json.MarshalIndent(t, "", "  ")
	return os.WriteFile(h.P(meta), b, 0o644)
}

// Task ist ein mehrstufiger Vorgang für den Job-Manager.
type Task struct {
	Title   string
	Command string
	Run     func(ctx context.Context, w io.Writer) error
}

func step(w io.Writer, format string, a ...any) { fmt.Fprintf(w, "» "+format+"\n", a...) }

// finishTemplate schützt das Basisimage und legt die Beschreibung an.
func (h *Host) finishTemplate(s *TemplateSpec, source string) error {
	disk, _ := h.templatePaths(s.Name)
	if err := os.Chmod(h.P(disk), 0o444); err != nil {
		return err
	}
	return h.writeTemplateMeta(&Template{Name: s.Name, Description: s.Description, OS: s.OS, Memory: s.Memory, CPUs: s.CPUs, Source: source, Created: time.Now().Unix()})
}

func (h *Host) checkNewTemplate(name string) error {
	disk, meta := h.templatePaths(name)
	for _, p := range []string{disk, meta} {
		if _, err := os.Stat(h.P(p)); err == nil {
			return invalid("Template %q existiert bereits", name)
		}
	}
	return os.MkdirAll(h.P(h.templateDir()), 0o755)
}

// TemplateFromVM kopiert die erste Disk einer gestoppten VM in die Bibliothek.
func (h *Host) TemplateFromVM(ctx context.Context, vm string, s *TemplateSpec) (*Task, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	conf, err := h.loadVMConf()
	if err != nil {
		return nil, err
	}
	v := conf.VM(vm)
	if v == nil || len(v.Disks) == 0 {
		return nil, invalid("VM %q hat keine Disk in vm.conf", vm)
	}
	if st, _ := h.vmctlStatus(ctx); st != nil {
		for _, x := range st {
			if x.Name == vm && x.State != "stopped" {
				return nil, invalid("VM %q muss gestoppt sein", vm)
			}
		}
	}
	if err := h.checkNewTemplate(s.Name); err != nil {
		return nil, err
	}
	src := v.Disks[0]
	disk, _ := h.templatePaths(s.Name)
	return &Task{
		Title: "Template " + s.Name + " aus " + vm, Command: "vmctl create -i " + src + " " + disk,
		Run: func(ctx context.Context, w io.Writer) error {
			step(w, "Kopiere %s nach %s (zusammengeführt, ohne Basisimage)", src, disk)
			if err := h.R.Stream(ctx, w, "vmctl", "create", "-i", src, disk); err != nil {
				os.Remove(h.P(disk))
				return err
			}
			step(w, "Basisimage schreibgeschützt, Beschreibung angelegt")
			return h.finishTemplate(s, "VM "+vm)
		},
	}, nil
}

// ImportTemplate lädt ein Image herunter oder übernimmt eines aus <vmdir>.
func (h *Host) ImportTemplate(ctx context.Context, s *TemplateSpec) (*Task, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	if (s.URL == "") == (s.Path == "") {
		return nil, invalid("entweder Adresse oder vorhandenes Image angeben")
	}
	if s.URL != "" {
		u, err := url.Parse(s.URL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || strings.ContainsAny(s.URL, " \n\"'") {
			return nil, invalid("nur http(s)-Adressen")
		}
	} else if err := h.checkVMPath(s.Path); err != nil {
		return nil, err
	}
	if err := h.checkNewTemplate(s.Name); err != nil {
		return nil, err
	}
	disk, _ := h.templatePaths(s.Name)
	src, source := s.Path, s.Path
	if s.URL != "" {
		src, source = filepath.Join(h.templateDir(), "."+s.Name+".download"), s.URL
	}
	return &Task{
		Title: "Template " + s.Name + " importieren", Command: "vmctl create -i " + source + " " + disk,
		Run: func(ctx context.Context, w io.Writer) error {
			if s.URL != "" {
				step(w, "Lade %s", s.URL)
				defer os.Remove(h.P(src))
				if err := h.R.Stream(ctx, w, "ftp", "-o", src, s.URL); err != nil {
					return err
				}
			}
			step(w, "Wandle in qcow2 um: %s", disk)
			if err := h.R.Stream(ctx, w, "vmctl", "create", "-i", src, disk); err != nil {
				os.Remove(h.P(disk))
				return fmt.Errorf("Umwandlung fehlgeschlagen – vmd liest raw und qcow2 (ohne Kompression): %w", err)
			}
			step(w, "Basisimage schreibgeschützt, Beschreibung angelegt")
			return h.finishTemplate(s, source)
		},
	}, nil
}

func (h *Host) UpdateTemplate(name string, s *TemplateSpec) error {
	s.Name = name
	if err := s.validate(); err != nil {
		return err
	}
	t, err := h.template(name)
	if err != nil {
		return err
	}
	t.Description, t.OS, t.Memory, t.CPUs = s.Description, s.OS, s.Memory, s.CPUs
	t.Disk, t.Size, t.Clones = "", 0, nil
	return h.writeTemplateMeta(t)
}

func (h *Host) DeleteTemplate(name string) error {
	t, err := h.template(name)
	if err != nil {
		return err
	}
	if len(t.Clones) > 0 {
		return invalid("Template %q ist Basis für %s – zuerst diese VMs löschen", name, strings.Join(t.Clones, ", "))
	}
	disk, meta := h.templatePaths(name)
	os.Remove(h.P(disk))
	return os.Remove(h.P(meta))
}

// ---- Bereitstellung ----

// DeploySpec beschreibt eine neue VM aus einem Template oder einer VM.
type DeploySpec struct {
	Name     string       `json:"name"`
	Memory   string       `json:"memory"`
	CPUs     int          `json:"cpus"`
	Linked   bool         `json:"linked"`
	Switches []string     `json:"switches"`
	Local    bool         `json:"local"`
	Owner    string       `json:"owner"`
	Enabled  bool         `json:"enabled"`
	StartNow bool         `json:"startNow"`
	Guest    *GuestConfig `json:"guest"`
}

// Deploy bereitet eine Bereitstellung vor. source ist "template" oder "vm".
func (h *Host) Deploy(ctx context.Context, source, from string, d *DeploySpec) (*Task, error) {
	var base, os_ string
	switch source {
	case "template":
		t, err := h.template(from)
		if err != nil {
			return nil, err
		}
		base, os_ = t.Disk, t.OS
	case "vm":
		conf, err := h.loadVMConf()
		if err != nil {
			return nil, err
		}
		v := conf.VM(from)
		if v == nil || len(v.Disks) == 0 {
			return nil, invalid("VM %q hat keine Disk", from)
		}
		if st, _ := h.vmctlStatus(ctx); st != nil {
			for _, x := range st {
				if x.Name == from && x.State != "stopped" {
					return nil, invalid("VM %q muss zum Klonen gestoppt sein", from)
				}
			}
		}
		// Ein Overlay auf eine weiterhin benutzte Disk würde sie beschädigen.
		base, d.Linked = v.Disks[0], false
	default:
		return nil, invalid("Quelle %q", source)
	}
	vs := &VMSpec{Name: d.Name, Memory: d.Memory, CPUs: d.CPUs, Switches: d.Switches, Local: d.Local, Owner: d.Owner, Enabled: d.Enabled}
	if err := h.validateVMSpec(vs); err != nil {
		return nil, err
	}
	conf, err := h.loadVMConf()
	if err != nil {
		return nil, err
	}
	if conf.VM(d.Name) != nil {
		return nil, invalid("VM %q existiert bereits", d.Name)
	}
	vs.DiskPath = filepath.Join(h.VMDir, d.Name+".qcow2")
	if _, err := os.Stat(h.P(vs.DiskPath)); err == nil {
		return nil, invalid("Disk %s existiert bereits", vs.DiskPath)
	}
	var seed *seedFiles
	if d.Guest != nil {
		if d.Guest.OS == "" {
			d.Guest.OS = os_
		}
		if seed, err = h.buildSeed(ctx, d.Name, d.Guest); err != nil {
			return nil, err
		}
		vs.CDROM = filepath.Join(h.seedDir(), d.Name+"-cidata.iso")
	}
	mode, args := "Vollkopie", []string{"create", "-i", base, vs.DiskPath}
	if d.Linked {
		mode, args = "verknüpfter Klon", []string{"create", "-b", base, vs.DiskPath}
	}
	return &Task{
		Title: fmt.Sprintf("VM %s aus %s (%s)", d.Name, from, mode), Command: "vmctl " + strings.Join(args, " "),
		Run: func(ctx context.Context, w io.Writer) (err error) {
			defer func() {
				if err != nil {
					os.Remove(h.P(vs.DiskPath))
					if vs.CDROM != "" {
						os.Remove(h.P(vs.CDROM))
					}
				}
			}()
			step(w, "Disk anlegen: %s (%s)", vs.DiskPath, mode)
			if err = h.R.Stream(ctx, w, "vmctl", args...); err != nil {
				return err
			}
			if seed != nil {
				step(w, "Gastanpassung: Seed-ISO %s (Label CIDATA)", vs.CDROM)
				if err = h.writeSeedISO(ctx, seed, vs.CDROM); err != nil {
					return err
				}
			}
			step(w, "vm.conf ergänzen und neu laden")
			if err = h.appendVM(ctx, vs.Block()); err != nil {
				return err
			}
			if d.StartNow {
				step(w, "Starte %s", d.Name)
				if _, err := h.R.Run(ctx, nil, "vmctl", "start", d.Name); err != nil {
					fmt.Fprintf(w, "Start fehlgeschlagen: %v\n", err)
				}
			}
			step(w, "Fertig.")
			return nil
		},
	}, nil
}

// appendVM hängt einen VM-Block an vm.conf an und lädt neu.
func (h *Host) appendVM(ctx context.Context, block string) error {
	h.confMu.Lock()
	defer h.confMu.Unlock()
	conf, err := h.loadVMConf()
	if err != nil {
		return err
	}
	if _, err := h.WriteConfig(ctx, vmConfPath, conf.Append(block), 0); err != nil {
		return err
	}
	return h.run(ctx, "vmctl", "reload")
}
