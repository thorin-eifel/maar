package obsd

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Sampler liest Zählerstände und bildet daraus Raten. Ein Aufruf alle ~10 s
// kostet nur sysctl, vmstat, netstat, ifconfig und ps – kein top.
//
// Schlüssel:
//
//	host.cpu           CPU-Auslastung in %
//	host.load          Load-Average (1 min)
//	host.mem           aktiver Speicher in % des verwalteten Speichers
//	host.net.rx/.tx    Bytes/s über alle physischen Schnittstellen
//	host.fs.<mount>    Belegung in % (nur bei SampleSlow)
//	vm.<name>.cpu      CPU des VM-Prozesses in % (100 % = ein Kern)
//	vm.<name>.mem      belegter Speicher (RSS) in Bytes
//	vm.<name>.rx/.tx   Bytes/s aus Sicht der VM
type Sampler struct {
	h      *Host
	mu     sync.Mutex
	cpu    []int64
	net    map[string][2]int64
	last   time.Time
	states map[string]string
}

func NewSampler(h *Host) *Sampler { return &Sampler{h: h, net: map[string][2]int64{}} }

// VMState enthält den beobachteten Zustand für die Ereigniserkennung.
type Sample struct {
	Time   time.Time
	Values map[string]float64
	VMs    map[string]string // Name → Zustand
}

var reTapDescr = regexp.MustCompile(`^vm[0-9]+-if[0-9]+-(.+)$`)

func (s *Sampler) Sample(ctx context.Context) (*Sample, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	out := &Sample{Time: now, Values: map[string]float64{}, VMs: map[string]string{}}
	dt := now.Sub(s.last).Seconds()
	first := s.last.IsZero()

	// CPU aus kern.cp_time: user,nice,sys,spin,intr,idle (Ticks seit Boot).
	if v, err := s.h.out(ctx, "sysctl", "-n", "kern.cp_time"); err == nil {
		var cur []int64
		for _, f := range strings.Split(strings.TrimSpace(v), ",") {
			n, _ := strconv.ParseInt(strings.TrimSpace(f), 10, 64)
			cur = append(cur, n)
		}
		if len(cur) >= 5 && len(s.cpu) == len(cur) {
			var total, idle int64
			for i := range cur {
				total += cur[i] - s.cpu[i]
			}
			idle = cur[len(cur)-1] - s.cpu[len(cur)-1]
			if total > 0 {
				out.Values["host.cpu"] = 100 * float64(total-idle) / float64(total)
			}
		}
		s.cpu = cur
	}
	if v, err := s.h.out(ctx, "sysctl", "-n", "vm.loadavg"); err == nil {
		if f := strings.Fields(v); len(f) > 0 {
			out.Values["host.load"], _ = strconv.ParseFloat(f[0], 64)
		}
	}
	if v, err := s.h.out(ctx, "vmstat", "-s"); err == nil {
		var managed, active float64
		for _, l := range lines(v) {
			f := strings.Fields(l)
			if len(f) < 3 {
				continue
			}
			n, _ := strconv.ParseFloat(f[0], 64)
			switch strings.Join(f[1:], " ") {
			case "pages managed":
				managed = n
			case "pages active":
				active = n
			}
		}
		if managed > 0 {
			out.Values["host.mem"] = 100 * active / managed
		}
	}

	// Netz: netstat -ibn, Link-Zeilen tragen die Bytezähler (letzte zwei Spalten).
	counters := map[string][2]int64{}
	if v, err := s.h.out(ctx, "netstat", "-ibn"); err == nil {
		for _, l := range lines(v) {
			f := strings.Fields(l)
			if len(f) < 5 || f[0] == "Name" || !strings.HasPrefix(f[2], "<Link") {
				continue
			}
			name := strings.TrimSuffix(f[0], "*")
			if _, seen := counters[name]; seen {
				continue
			}
			in, e1 := strconv.ParseInt(f[len(f)-2], 10, 64)
			outb, e2 := strconv.ParseInt(f[len(f)-1], 10, 64)
			if e1 == nil && e2 == nil {
				counters[name] = [2]int64{in, outb}
			}
		}
	}
	rate := func(name string) (rx, tx float64, ok bool) {
		c, p := counters[name], s.net[name]
		if first || dt <= 0 || c[0] < p[0] || c[1] < p[1] {
			return 0, 0, false
		}
		return float64(c[0]-p[0]) / dt, float64(c[1]-p[1]) / dt, true
	}
	var hostRx, hostTx float64
	hostOK := false
	for name := range counters {
		if !physicalIface(name) {
			continue
		}
		if rx, tx, ok := rate(name); ok {
			hostRx, hostTx, hostOK = hostRx+rx, hostTx+tx, true
		}
	}
	if hostOK {
		out.Values["host.net.rx"], out.Values["host.net.tx"] = hostRx, hostTx
	}

	// VMs: Zustand und PID aus vmctl, Taps über ihre Beschreibung „vmN-ifM-name“.
	taps := map[string][]string{}
	if v, err := s.h.out(ctx, "ifconfig", "-A"); err == nil {
		for _, i := range ParseIfconfig(v) {
			if m := reTapDescr.FindStringSubmatch(i.Descr); m != nil {
				taps[m[1]] = append(taps[m[1]], i.Name)
			}
		}
	}
	if st, err := s.h.vmctlStatus(ctx); err == nil {
		var pids []string
		byPID := map[string]string{}
		for _, vm := range st {
			out.VMs[vm.Name] = vm.State
			if vm.State != "running" || vm.PID == "-" {
				continue
			}
			pids = append(pids, vm.PID)
			byPID[vm.PID] = vm.Name
			var rx, tx float64
			ok := false
			for _, t := range taps[vm.Name] {
				// Was der Host über das Tap empfängt, hat die VM gesendet.
				if in, outb, good := rate(t); good {
					rx, tx, ok = rx+outb, tx+in, true
				}
			}
			if ok {
				out.Values["vm."+vm.Name+".rx"], out.Values["vm."+vm.Name+".tx"] = rx, tx
			}
		}
		if len(pids) > 0 {
			if v, err := s.h.out(ctx, "ps", "-o", "pid,%cpu,rss", "-p", strings.Join(pids, ",")); err == nil {
				for _, l := range lines(v) {
					f := strings.Fields(l)
					if len(f) < 3 || byPID[f[0]] == "" {
						continue
					}
					name := byPID[f[0]]
					out.Values["vm."+name+".cpu"], _ = strconv.ParseFloat(f[1], 64)
					rss, _ := strconv.ParseFloat(f[2], 64)
					out.Values["vm."+name+".mem"] = rss * 1024
				}
			}
		}
	}
	s.net, s.last = counters, now
	return out, nil
}

func physicalIface(name string) bool {
	for _, p := range []string{"lo", "pflog", "enc", "tap", "vport", "veb", "bridge", "vether", "tun", "pfsync", "carp", "wg", "gif", "gre", "vlan"} {
		if strings.HasPrefix(name, p) {
			return false
		}
	}
	return true
}

// SampleSlow liefert seltener benötigte Werte: Dateisystembelegung und
// ausgefallene Dienste.
func (s *Sampler) SampleSlow(ctx context.Context) (map[string]float64, []string) {
	vals := map[string]float64{}
	if st, err := s.h.Storage(ctx); err == nil {
		for _, m := range st.Mounts {
			vals["host.fs."+m.Mount] = float64(m.Percent)
		}
	}
	var failed []string
	for _, l := range lines(s.h.outLenient(ctx, "rcctl", "ls", "failed")) {
		failed = append(failed, strings.TrimSpace(l))
	}
	return vals, failed
}

// ---- Ereignisse ----

// Emit meldet ein Ereignis (z. B. fehlgeschlagene Sicherung) an den Alarmdienst.
func (h *Host) Emit(kind, subject, message string) {
	h.hookMu.Lock()
	fn := h.onEvent
	h.hookMu.Unlock()
	if fn != nil {
		fn(kind, subject, message)
	}
}

func (h *Host) SetEventHandler(fn func(kind, subject, message string)) {
	h.hookMu.Lock()
	h.onEvent = fn
	h.hookMu.Unlock()
}

// ExpectStop markiert einen gewollten Stopp, damit er keinen Alarm auslöst.
func (h *Host) ExpectStop(vm string) {
	h.hookMu.Lock()
	if h.expected == nil {
		h.expected = map[string]time.Time{}
	}
	h.expected[vm] = time.Now()
	h.hookMu.Unlock()
}

func (h *Host) StopExpected(vm string) bool {
	h.hookMu.Lock()
	defer h.hookMu.Unlock()
	t, ok := h.expected[vm]
	return ok && time.Since(t) < 15*time.Minute
}
