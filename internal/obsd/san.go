package obsd

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Geteilter Speicher (SAN): iSCSI über iscsid(8)/iscsictl(8), Fibre Channel über
// die HBA-Treiber (qla, qlw, qle) und Datastores darauf. NVMe-oF kennt OpenBSD
// nicht; die Oberfläche sagt das ausdrücklich. Gespeichert wird die Absicht in
// /var/maar/san.json, /etc/iscsi.conf und /etc/fstab bekommen erzeugte Blöcke.

const (
	sanConfPath   = "/var/maar/san.json"
	iscsiConfPath = "/etc/iscsi.conf"
	iscsiBegin    = "# >>> maar: iSCSI-Ziele (automatisch erzeugt, nicht von Hand ändern)"
	iscsiEnd      = "# <<< maar"
)

type ISCSITarget struct {
	Name      string `json:"name"`      // Abschnittsname in iscsi.conf
	Addr      string `json:"addr"`      // IP oder Hostname
	Port      int    `json:"port"`      // 0 = 3260
	IQN       string `json:"iqn"`       // leer bei Discovery
	Initiator string `json:"initiator"` // leer = Standard
	Discovery bool   `json:"discovery"`
	Enabled   bool   `json:"enabled"`
	Managed   bool   `json:"managed"`
	State     string `json:"state,omitempty"` // aus iscsictl, best effort
}

type Datastore struct {
	Name  string `json:"name"`
	Disk  string `json:"disk"` // zuletzt bekannter Gerätename
	DUID  string `json:"duid"`
	Part  string `json:"part"`
	Mount string `json:"mount"`
}

type SANConfig struct {
	Targets    []*ISCSITarget `json:"targets"`
	Datastores []*Datastore   `json:"datastores"`
}

type SANDisk struct {
	Name      string   `json:"name"`
	DUID      string   `json:"duid"`
	Vendor    string   `json:"vendor"`
	Model     string   `json:"model"`
	Rev       string   `json:"rev"`
	WWN       string   `json:"wwn,omitempty"`
	Bus       string   `json:"bus"`
	Transport string   `json:"transport"` // iscsi, fc, mpath, local
	Size      int64    `json:"size"`
	Mounts    []string `json:"mounts"`
	Datastore string   `json:"datastore,omitempty"`
	Labeled   bool     `json:"labeled"`
	CanInit   bool     `json:"canInit"`
	Reason    string   `json:"reason,omitempty"`
}

type FCHBA struct {
	Name  string   `json:"name"`
	Model string   `json:"model"`
	WWPN  string   `json:"wwpn,omitempty"`
	Disks []string `json:"disks"`
}

type DatastoreInfo struct {
	*Datastore
	Mounted bool     `json:"mounted"`
	Present bool     `json:"present"` // Disk gefunden
	Size    int64    `json:"size"`
	Used    int64    `json:"used"`
	Avail   int64    `json:"avail"`
	Percent int      `json:"percent"`
	Busy    []string `json:"busy"` // laufende VMs auf dem Datastore
}

type SANOverview struct {
	Disks      []SANDisk       `json:"disks"`
	HBAs       []FCHBA         `json:"hbas"`
	Targets    []*ISCSITarget  `json:"targets"`
	Datastores []DatastoreInfo `json:"datastores"`
	ISCSI      struct {
		Enabled bool   `json:"enabled"`
		Running bool   `json:"running"`
		Summary string `json:"summary"`
	} `json:"iscsi"`
	NVMe struct {
		Supported bool     `json:"supported"`
		Note      string   `json:"note"`
		Local     []string `json:"local"`
	} `json:"nvmeof"`
	Foreign []string `json:"foreignTargets"` // von Hand in iscsi.conf
}

var (
	reDiskSD   = regexp.MustCompile(`^sd[0-9]{1,3}$`)
	reSDLine   = regexp.MustCompile(`^(sd[0-9]+) at (scsibus[0-9]+) targ [0-9]+ lun [0-9]+: <([^,>]*),\s*([^,>]*),\s*([^>]*)>(.*)$`)
	reSDSize   = regexp.MustCompile(`^(sd[0-9]+): [0-9]+MB, ([0-9]+) bytes/sector, ([0-9]+) sectors`)
	reBus      = regexp.MustCompile(`^(scsibus[0-9]+) at ([a-z]+)([0-9]+)(?::|\s|$)(.*)$`)
	reWWN      = regexp.MustCompile(`\b((?:naa|eui|t10)\.[0-9A-Za-z]+)`)
	reWWPN     = regexp.MustCompile(`initiator WWPN ([0-9a-fA-F]{16})`)
	reHBA      = regexp.MustCompile(`^((?:qla|qlw|qle)[0-9]+) at pci[0-9]+ dev [0-9]+ function [0-9]+ "([^"]+)"`)
	reNVMeLine = regexp.MustCompile(`^(nvme[0-9]+) at pci[0-9]+ dev [0-9]+ function [0-9]+ "([^"]+)"`)
	reIQN      = regexp.MustCompile(`^(iqn\.[0-9]{4}-[0-9]{2}\.[a-z0-9.-]+|eui\.[0-9A-Fa-f]{16}|naa\.[0-9A-Fa-f]{16,32})(:[A-Za-z0-9._:-]{1,150})?$`)
	reHostAddr = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
	reTargetSc = regexp.MustCompile(`(?m)^\s*target\s+("[^"]+"|[A-Za-z0-9_-]+)\s*\{`)
)

func (h *Host) loadSAN() (*SANConfig, error) {
	c := &SANConfig{Targets: []*ISCSITarget{}, Datastores: []*Datastore{}}
	b, err := os.ReadFile(h.P(sanConfPath))
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("san.json: %w", err)
	}
	if c.Targets == nil {
		c.Targets = []*ISCSITarget{}
	}
	if c.Datastores == nil {
		c.Datastores = []*Datastore{}
	}
	return c, nil
}

func (h *Host) saveSAN(c *SANConfig) error {
	b, _ := json.MarshalIndent(c, "", "  ")
	p := h.P(sanConfPath)
	os.MkdirAll(filepath.Dir(p), 0o700)
	if err := os.WriteFile(p+".tmp", b, 0o600); err != nil {
		return err
	}
	return os.Rename(p+".tmp", p)
}

type dmesgInfo struct {
	disks map[string]*SANDisk
	order []string
	buses map[string]string // scsibusN → Treibername (iscsi, qla, mpath, …)
	hbas  []*FCHBA
	wwpn  map[string]string // HBA → WWPN
	busHB map[string]string // scsibusN → HBA-Name
	nvme  []string
}

func parseDmesg(out string) *dmesgInfo {
	d := &dmesgInfo{disks: map[string]*SANDisk{}, buses: map[string]string{}, wwpn: map[string]string{}, busHB: map[string]string{}}
	hbaSeen := map[string]*FCHBA{}
	for _, l := range lines(out) {
		l = strings.TrimSpace(l)
		if m := reHBA.FindStringSubmatch(l); m != nil {
			if _, ok := hbaSeen[m[1]]; !ok {
				hb := &FCHBA{Name: m[1], Model: m[2], Disks: []string{}}
				hbaSeen[m[1]] = hb
				d.hbas = append(d.hbas, hb)
			}
			continue
		}
		if m := reNVMeLine.FindStringSubmatch(l); m != nil {
			d.nvme = append(d.nvme, m[1]+": "+m[2])
			continue
		}
		if m := reBus.FindStringSubmatch(l); m != nil {
			d.buses[m[1]] = m[2]
			d.busHB[m[1]] = m[2] + m[3]
			if w := reWWPN.FindStringSubmatch(m[4]); w != nil {
				d.wwpn[m[2]+m[3]] = strings.ToLower(w[1])
			}
			continue
		}
		if m := reSDLine.FindStringSubmatch(l); m != nil {
			dk, ok := d.disks[m[1]]
			if !ok {
				dk = &SANDisk{Name: m[1], Mounts: []string{}}
				d.disks[m[1]] = dk
				d.order = append(d.order, m[1])
			}
			dk.Bus, dk.Vendor, dk.Model, dk.Rev = m[2], strings.TrimSpace(m[3]), strings.TrimSpace(m[4]), strings.TrimSpace(m[5])
			if w := reWWN.FindStringSubmatch(m[6]); w != nil {
				dk.WWN = w[1]
			}
			continue
		}
		if m := reSDSize.FindStringSubmatch(l); m != nil {
			if dk := d.disks[m[1]]; dk != nil {
				bs, _ := strconv.ParseInt(m[2], 10, 64)
				sec, _ := strconv.ParseInt(m[3], 10, 64)
				dk.Size = bs * sec
			}
		}
	}
	return d
}

func transportOf(driver string) string {
	switch driver {
	case "iscsi":
		return "iscsi"
	case "qla", "qlw", "qle":
		return "fc"
	case "mpath":
		return "mpath"
	}
	return "local"
}

// mountsByDisk ordnet gemountete Partitionen ihrer Disk zu ("/dev/sd2a on /mnt type ffs").
func mountTable(out string) map[string]string {
	m := map[string]string{} // Gerät → Einhängepunkt
	for _, l := range lines(out) {
		f := strings.Fields(l)
		if len(f) >= 3 && f[1] == "on" {
			m[f[0]] = f[2]
		}
	}
	return m
}

var rePartDev = regexp.MustCompile(`^/dev/(sd[0-9]+)[a-p]$`)

func (h *Host) SAN(ctx context.Context) (*SANOverview, error) {
	cfg, err := h.loadSAN()
	if err != nil {
		return nil, err
	}
	dm := parseDmesg(h.outLenient(ctx, "dmesg"))
	o := &SANOverview{Disks: []SANDisk{}, HBAs: []FCHBA{}, Targets: []*ISCSITarget{}, Datastores: []DatastoreInfo{}, Foreign: []string{}}

	duids := map[string]string{}
	if dn, err := h.out(ctx, "sysctl", "-n", "hw.disknames"); err == nil {
		for _, d := range strings.Split(strings.TrimSpace(dn), ",") {
			name, duid, _ := strings.Cut(d, ":")
			duids[name] = duid
		}
	}
	mt := mountTable(h.outLenient(ctx, "mount"))
	byMount := map[string]string{} // Einhängepunkt → Disk
	diskMounts := map[string][]string{}
	for dev, mp := range mt {
		if m := rePartDev.FindStringSubmatch(dev); m != nil {
			diskMounts[m[1]] = append(diskMounts[m[1]], mp)
			byMount[mp] = m[1]
		}
	}
	fstab, _ := h.readFile("/etc/fstab")
	rootDisk := ""
	if m := rePartDev.FindStringSubmatch(devOf(mt, "/")); m != nil {
		rootDisk = m[1]
	}
	dsByDUID := map[string]*Datastore{}
	for _, ds := range cfg.Datastores {
		dsByDUID[ds.DUID] = ds
	}

	for _, n := range dm.order {
		dk := dm.disks[n]
		dk.Transport = transportOf(dm.buses[dk.Bus])
		dk.DUID = duids[n]
		dk.Labeled = dk.DUID != "" && strings.Trim(dk.DUID, "0") != ""
		dk.Mounts = diskMounts[n]
		if dk.Mounts == nil {
			dk.Mounts = []string{}
		}
		if ds := dsByDUID[dk.DUID]; ds != nil && dk.Labeled {
			dk.Datastore = ds.Name
		}
		switch {
		case dk.Transport == "local":
			dk.Reason = "lokale Disk – wird hier nie initialisiert"
		case n == rootDisk:
			dk.Reason = "Systemdisk"
		case len(dk.Mounts) > 0:
			dk.Reason = "eingehängt"
		case dk.Datastore != "":
			dk.Reason = "gehört zum Datastore " + dk.Datastore
		case dk.Labeled && strings.Contains(fstab, dk.DUID):
			dk.Reason = "in /etc/fstab eingetragen"
		default:
			dk.CanInit = true
		}
		if hb := dm.busHB[dk.Bus]; dk.Transport == "fc" {
			for _, x := range dm.hbas {
				if x.Name == hb {
					x.Disks = append(x.Disks, n)
				}
			}
		}
		o.Disks = append(o.Disks, *dk)
	}
	for _, x := range dm.hbas {
		x.WWPN = dm.wwpn[x.Name]
		o.HBAs = append(o.HBAs, *x)
	}

	// iSCSI
	o.ISCSI.Enabled = h.run(ctx, "rcctl", "get", "iscsid", "status") == nil
	o.ISCSI.Running = h.run(ctx, "rcctl", "check", "iscsid") == nil
	if o.ISCSI.Running {
		o.ISCSI.Summary = strings.TrimSpace(h.outLenient(ctx, "iscsictl", "show", "summary"))
	}
	for _, t := range cfg.Targets {
		c := *t
		c.Managed = true
		c.State = targetState(o.ISCSI.Summary, t.Name, t.Enabled, o.ISCSI.Running)
		o.Targets = append(o.Targets, &c)
	}
	if src, err := h.readFile(iscsiConfPath); err == nil {
		if a := strings.Index(src, iscsiBegin); a >= 0 {
			if b := strings.Index(src[a:], iscsiEnd); b >= 0 {
				src = src[:a] + src[lineEnd(src, a+b):]
			}
		}
		for _, m := range reTargetSc.FindAllStringSubmatch(src, -1) {
			o.Foreign = append(o.Foreign, strings.Trim(m[1], `"`))
		}
	}

	// Datastores
	st, _ := h.Storage(ctx)
	for _, ds := range cfg.Datastores {
		di := DatastoreInfo{Datastore: ds, Busy: []string{}}
		for _, x := range o.Disks {
			if x.DUID == ds.DUID && x.Labeled {
				di.Present, di.Datastore = true, &Datastore{Name: ds.Name, Disk: x.Name, DUID: ds.DUID, Part: ds.Part, Mount: ds.Mount}
				di.Size = x.Size
			}
		}
		_, di.Mounted = reverse(mt, ds.Mount)
		if st != nil && di.Mounted {
			for _, m := range st.Mounts {
				if m.Mount == ds.Mount {
					di.Size, di.Used, di.Avail, di.Percent = m.Size, m.Used, m.Avail, m.Percent
				}
			}
		}
		if di.Mounted {
			di.Busy = h.vmsOn(ctx, ds.Mount, true)
		}
		o.Datastores = append(o.Datastores, di)
	}

	o.NVMe.Supported = false
	o.NVMe.Note = "OpenBSD 8.0 enthält keinen NVMe-over-Fabrics-Initiator (weder TCP noch RDMA noch FC-NVMe). nvme(4) bedient nur lokale PCIe-Laufwerke. Sobald OpenBSD das nachrüstet, kommt es hier als vierter Transport dazu; bis dahin den Speicher per iSCSI oder FC freigeben."
	o.NVMe.Local = dm.nvme
	if o.NVMe.Local == nil {
		o.NVMe.Local = []string{}
	}
	sort.Slice(o.Targets, func(i, j int) bool { return o.Targets[i].Name < o.Targets[j].Name })
	return o, nil
}

func devOf(m map[string]string, mp string) string {
	for d, p := range m {
		if p == mp {
			return d
		}
	}
	return ""
}

func reverse(m map[string]string, mp string) (string, bool) {
	d := devOf(m, mp)
	return d, d != ""
}

// targetState liest den Zustand best effort aus "iscsictl show summary".
func targetState(summary, name string, enabled, running bool) string {
	switch {
	case !enabled:
		return "deaktiviert"
	case !running:
		return "iscsid läuft nicht"
	}
	for _, l := range strings.Split(summary, "\n") {
		if strings.Contains(l, name) {
			low := strings.ToLower(l)
			if strings.Contains(low, "logged in") || strings.Contains(low, "loggedin") || strings.Contains(low, "full feature") || strings.Contains(low, "full_feature") {
				return "angemeldet"
			}
			return "nicht angemeldet"
		}
	}
	return "unbekannt"
}

// vmsOn nennt VMs, deren Disks unter dir liegen (nur laufende, wenn running).
func (h *Host) vmsOn(ctx context.Context, dir string, running bool) []string {
	conf, err := h.loadVMConf()
	if err != nil {
		return []string{}
	}
	state := map[string]string{}
	if st, err := h.vmctlStatus(ctx); err == nil {
		for _, s := range st {
			state[s.Name] = s.State
		}
	}
	out := []string{}
	for _, v := range conf.VMs {
		for _, d := range append(append([]string{}, v.Disks...), v.CDROM) {
			if d != "" && strings.HasPrefix(d, strings.TrimRight(dir, "/")+"/") {
				if !running || (state[v.Name] != "" && state[v.Name] != "stopped") {
					out = append(out, v.Name)
				}
				break
			}
		}
	}
	return out
}

// ---- Prüfung ----

func (t *ISCSITarget) validate() error {
	if err := checkName("Ziel", t.Name, reName); err != nil {
		return err
	}
	if strings.ContainsAny(t.Name, `."\`) {
		return invalid("Name des Ziels: Buchstaben, Ziffern, Bindestrich, Unterstrich")
	}
	if t.Addr == "" || len(t.Addr) > 253 || (net.ParseIP(t.Addr) == nil && !reHostAddr.MatchString(t.Addr)) {
		return invalid("Adresse %q", t.Addr)
	}
	if t.Port < 0 || t.Port > 65535 {
		return invalid("Port %d", t.Port)
	}
	if t.Discovery {
		t.IQN = ""
	} else if !reIQN.MatchString(t.IQN) {
		return invalid("Ziel-IQN %q (z. B. iqn.2003-01.org.example:vol1)", t.IQN)
	}
	if t.Initiator != "" && !reIQN.MatchString(t.Initiator) {
		return invalid("Initiator-Name %q", t.Initiator)
	}
	return nil
}

func (t *ISCSITarget) block() string {
	var b strings.Builder
	fmt.Fprintf(&b, "target %s {\n", t.Name)
	if !t.Enabled {
		b.WriteString("\tdisabled\n")
	}
	if t.Discovery {
		b.WriteString("\tdiscovery\n")
	}
	if t.Initiator != "" {
		fmt.Fprintf(&b, "\tinitiatorname %s\n", quote(t.Initiator))
	}
	fmt.Fprintf(&b, "\ttargetaddr %s", t.Addr)
	if t.Port != 0 && t.Port != 3260 {
		fmt.Fprintf(&b, " port %d", t.Port)
	}
	b.WriteString("\n")
	if t.IQN != "" {
		fmt.Fprintf(&b, "\ttargetname %s\n", quote(t.IQN))
	}
	b.WriteString("}\n")
	return b.String()
}

func iscsiConf(cur string, targets []*ISCSITarget) string {
	var b strings.Builder
	sorted := append([]*ISCSITarget{}, targets...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	for _, t := range sorted {
		b.WriteString("\n" + t.block())
	}
	return mergeBlock(cur, iscsiBegin, iscsiEnd, b.String())
}
