package obsd

import (
	"context"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Overview struct {
	Hostname  string   `json:"hostname"`
	OS        string   `json:"os"`
	Release   string   `json:"release"`
	Version   string   `json:"version"`
	Machine   string   `json:"machine"`
	Model     string   `json:"model"`
	NCPU      int      `json:"ncpu"`
	PhysMem   int64    `json:"physmem"`
	BootTime  int64    `json:"boottime"`
	Uptime    int64    `json:"uptime"`
	Load      []string `json:"load"`
	CPUBusy   float64  `json:"cpuBusy"`
	MemActive int64    `json:"memActive"`
	MemTotal  int64    `json:"memTotal"`
	MemFree   int64    `json:"memFree"`
	MemCache  int64    `json:"memCache"`
	SwapUsed  int64    `json:"swapUsed"`
	SwapTotal int64    `json:"swapTotal"`
	Procs     string   `json:"procs"`
}

var (
	reTopLoad = regexp.MustCompile(`load averages:\s*([0-9.]+),\s*([0-9.]+),\s*([0-9.]+)`)
	reTopIdle = regexp.MustCompile(`([0-9.]+)% idle`)
	reTopMem  = regexp.MustCompile(`Real: (\S+)/(\S+) act/tot\s+Free: (\S+)\s+Cache: (\S+)\s+Swap: (\S+)/(\S+)`)
)

func (h *Host) Overview(ctx context.Context) (*Overview, error) {
	o := &Overview{}
	out, err := h.out(ctx, "sysctl", "kern.hostname", "kern.ostype", "kern.osrelease", "hw.machine",
		"hw.model", "hw.ncpuonline", "hw.physmem", "kern.boottime")
	if err != nil {
		return nil, err
	}
	for _, l := range lines(out) {
		k, v, ok := strings.Cut(l, "=")
		if !ok {
			continue
		}
		switch k {
		case "kern.hostname":
			o.Hostname = v
		case "kern.ostype":
			o.OS = v
		case "kern.osrelease":
			o.Release = v
		case "hw.machine":
			o.Machine = v
		case "hw.model":
			o.Model = v
		case "hw.ncpuonline":
			o.NCPU, _ = strconv.Atoi(v)
		case "hw.physmem":
			o.PhysMem, _ = strconv.ParseInt(v, 10, 64)
		case "kern.boottime":
			o.BootTime = parseBootTime(v)
		}
	}
	if o.BootTime > 0 {
		o.Uptime = time.Now().Unix() - o.BootTime
	}
	if v, err := h.out(ctx, "sysctl", "-n", "kern.version"); err == nil {
		o.Version = strings.TrimSpace(strings.SplitN(v, "\n", 2)[0])
	}
	top, _ := h.out(ctx, "top", "-b", "-1", "-d", "1")
	if m := reTopLoad.FindStringSubmatch(top); m != nil {
		o.Load = m[1:4]
	}
	for _, l := range strings.Split(top, "\n") {
		if strings.Contains(l, "CPU") && strings.Contains(l, "idle") {
			if m := reTopIdle.FindStringSubmatch(l); m != nil {
				idle, _ := strconv.ParseFloat(m[1], 64)
				o.CPUBusy = 100 - idle
			}
		}
		if strings.Contains(l, "processes:") {
			o.Procs = strings.TrimSpace(strings.SplitN(l, "  up ", 2)[0])
		}
	}
	if m := reTopMem.FindStringSubmatch(top); m != nil {
		o.MemActive, o.MemTotal = parseSize(m[1]), parseSize(m[2])
		o.MemFree, o.MemCache = parseSize(m[3]), parseSize(m[4])
		o.SwapUsed, o.SwapTotal = parseSize(m[5]), parseSize(m[6])
	}
	return o, nil
}

func parseBootTime(v string) int64 {
	if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
		return n
	}
	for _, layout := range []string{time.ANSIC, time.UnixDate} {
		if t, err := time.ParseInLocation(layout, strings.TrimSpace(v), time.Local); err == nil {
			return t.Unix()
		}
	}
	return 0
}

// Hostname liefert kern.hostname.
func (h *Host) Hostname(ctx context.Context) string {
	out, _ := h.out(ctx, "sysctl", "-n", "kern.hostname")
	return strings.TrimSpace(out)
}

// ---- Sysctl ----

type Sysctl struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func (h *Host) Sysctls(ctx context.Context) ([]Sysctl, error) {
	out, err := h.out(ctx, "sysctl", "-a")
	if err != nil && out == "" {
		return nil, err
	}
	var list []Sysctl
	for _, l := range lines(out) {
		if k, v, ok := strings.Cut(l, "="); ok && reSysctl.MatchString(k) {
			list = append(list, Sysctl{k, v})
		}
	}
	return list, nil
}

func (h *Host) SetSysctl(ctx context.Context, name, value string) error {
	if err := checkName("sysctl", name, reSysctl); err != nil {
		return err
	}
	if strings.ContainsAny(value, "\n\x00") || len(value) > 256 {
		return invalid("Wert")
	}
	return h.run(ctx, "sysctl", name+"="+value)
}

// ---- Prozesse ----

type Proc struct {
	PID     int     `json:"pid"`
	User    string  `json:"user"`
	CPU     float64 `json:"cpu"`
	Mem     float64 `json:"mem"`
	RSS     int64   `json:"rss"`
	State   string  `json:"state"`
	Started string  `json:"started"`
	Time    string  `json:"time"`
	Command string  `json:"command"`
}

func (h *Host) Procs(ctx context.Context) ([]Proc, error) {
	out, err := h.out(ctx, "ps", "-axww", "-o", "pid,user,%cpu,%mem,rss,state,start,time,command")
	if err != nil {
		return nil, err
	}
	var list []Proc
	for i, l := range lines(out) {
		if i == 0 {
			continue
		}
		f := strings.Fields(l)
		if len(f) < 9 {
			continue
		}
		p := Proc{User: f[1], State: f[5], Started: f[6], Time: f[7], Command: strings.Join(f[8:], " ")}
		p.PID, _ = strconv.Atoi(f[0])
		p.CPU, _ = strconv.ParseFloat(f[2], 64)
		p.Mem, _ = strconv.ParseFloat(f[3], 64)
		p.RSS, _ = strconv.ParseInt(f[4], 10, 64)
		p.RSS *= 1024
		list = append(list, p)
	}
	sort.Slice(list, func(a, b int) bool { return list[a].CPU > list[b].CPU })
	return list, nil
}

var signals = map[string]bool{"TERM": true, "KILL": true, "HUP": true, "INT": true, "STOP": true, "CONT": true}

func (h *Host) Kill(ctx context.Context, pid int, sig string) error {
	if pid <= 1 {
		return invalid("PID %d", pid)
	}
	if !signals[sig] {
		return invalid("Signal %q", sig)
	}
	return h.run(ctx, "kill", "-"+sig, strconv.Itoa(pid))
}

// ---- Speicher ----

type Mount struct {
	Device  string `json:"device"`
	Size    int64  `json:"size"`
	Used    int64  `json:"used"`
	Avail   int64  `json:"avail"`
	Percent int    `json:"percent"`
	Mount   string `json:"mount"`
	Options string `json:"options"`
}

type Disk struct {
	Name  string `json:"name"`
	DUID  string `json:"duid"`
	Label string `json:"label,omitempty"`
}

type Storage struct {
	Mounts []Mount `json:"mounts"`
	Disks  []Disk  `json:"disks"`
}

func (h *Host) Storage(ctx context.Context) (*Storage, error) {
	st := &Storage{}
	out, err := h.out(ctx, "df", "-kP")
	if err != nil {
		return nil, err
	}
	opts := map[string]string{}
	if m, err := h.out(ctx, "mount"); err == nil {
		// /dev/sd0a on / type ffs (local, wxallowed)
		for _, l := range lines(m) {
			f := strings.Fields(l)
			if len(f) >= 6 && f[1] == "on" {
				opts[f[2]] = strings.Trim(strings.Join(f[5:], " "), "()")
			}
		}
	}
	for i, l := range lines(out) {
		f := strings.Fields(l)
		if i == 0 || len(f) < 6 {
			continue
		}
		m := Mount{Device: f[0], Mount: strings.Join(f[5:], " ")}
		m.Size, _ = strconv.ParseInt(f[1], 10, 64)
		m.Used, _ = strconv.ParseInt(f[2], 10, 64)
		m.Avail, _ = strconv.ParseInt(f[3], 10, 64)
		m.Size, m.Used, m.Avail = m.Size*1024, m.Used*1024, m.Avail*1024
		m.Percent, _ = strconv.Atoi(strings.TrimSuffix(f[4], "%"))
		m.Options = opts[m.Mount]
		st.Mounts = append(st.Mounts, m)
	}
	if dn, err := h.out(ctx, "sysctl", "-n", "hw.disknames"); err == nil {
		for _, d := range strings.Split(strings.TrimSpace(dn), ",") {
			name, duid, _ := strings.Cut(d, ":")
			if name != "" {
				st.Disks = append(st.Disks, Disk{Name: name, DUID: duid})
			}
		}
	}
	return st, nil
}

var reDisk = regexp.MustCompile(`^(sd|wd|vd|cd|rd|vnd)[0-9]{1,3}$`)

func (h *Host) Disklabel(ctx context.Context, disk string) (string, error) {
	if !reDisk.MatchString(disk) {
		return "", invalid("Datenträger %q", disk)
	}
	return h.out(ctx, "disklabel", "-p", "g", disk)
}

// ---- Energie ----

func (h *Host) Power(ctx context.Context, action string) error {
	switch action {
	case "reboot":
		return h.run(ctx, "shutdown", "-r", "+1", "Neustart über maar")
	case "halt":
		return h.run(ctx, "shutdown", "-p", "+1", "Ausschalten über maar")
	case "cancel":
		return h.run(ctx, "pkill", "-x", "shutdown")
	}
	return invalid("Aktion %q", action)
}
