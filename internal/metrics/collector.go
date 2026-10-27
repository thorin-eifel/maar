package metrics

import (
	"context"
	"fmt"
	"log"
	"math"
	"math/rand"
	"strings"
	"time"

	"github.com/thorin-eifel/maar/internal/obsd"
)

const storePath = "/var/maar/metrics.json.gz"

type Collector struct {
	H       *obsd.Host
	Store   *Store
	Alerts  *Alerter
	Log     *log.Logger
	sampler *obsd.Sampler
	slow    map[string]float64
	failed  map[string]bool
	states  map[string]string
}

func NewCollector(h *obsd.Host, l *log.Logger) *Collector {
	c := &Collector{H: h, Store: NewStore(), Alerts: NewAlerter(h), Log: l, sampler: obsd.NewSampler(h), slow: map[string]float64{}}
	if err := c.Store.Load(h.P(storePath)); err != nil && h.Demo {
		Backfill(c.Store, time.Now())
	}
	h.SetEventHandler(c.Alerts.Event)
	return c
}

func (c *Collector) Run(ctx context.Context) {
	tick := time.NewTicker(RawStep)
	defer tick.Stop()
	n := 0
	for {
		c.collect(ctx, n%6 == 0)
		if n%30 == 29 {
			c.Store.Prune(8 * 24 * time.Hour)
			if err := c.Store.Save(c.H.P(storePath)); err != nil {
				c.Log.Printf("metrics: %v", err)
			}
		}
		n++
		select {
		case <-ctx.Done():
			c.Store.Save(c.H.P(storePath))
			return
		case <-tick.C:
		}
	}
}

func (c *Collector) collect(ctx context.Context, slow bool) {
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	s, err := c.sampler.Sample(cctx)
	if err != nil {
		c.Log.Printf("metrics: %v", err)
		return
	}
	if slow {
		vals, failed := c.sampler.SampleSlow(cctx)
		c.slow = vals
		c.serviceEvents(failed)
		for k, v := range vals {
			c.Store.Add(k, s.Time, v)
		}
	}
	for k, v := range s.Values {
		c.Store.Add(k, s.Time, v)
	}
	c.vmEvents(s.VMs)
	all := make(map[string]float64, len(s.Values)+len(c.slow))
	for k, v := range c.slow {
		all[k] = v
	}
	for k, v := range s.Values {
		all[k] = v
	}
	c.Alerts.Eval(all, s.Time)
}

// serviceEvents meldet Dienste, die neu ausgefallen sind.
func (c *Collector) serviceEvents(failed []string) {
	now := map[string]bool{}
	for _, f := range failed {
		now[f] = true
		if c.failed != nil && !c.failed[f] {
			c.Alerts.Event("service_failed", f, fmt.Sprintf("Dienst %s ist aktiviert, läuft aber nicht (rcctl ls failed).", f))
		}
	}
	c.failed = now
}

// vmEvents meldet VMs, die ohne Zutun von maar aufgehört haben zu laufen.
func (c *Collector) vmEvents(states map[string]string) {
	if c.states != nil {
		for vm, old := range c.states {
			if old != "running" {
				continue
			}
			if cur, ok := states[vm]; (!ok || cur == "stopped") && !c.H.StopExpected(vm) {
				c.Alerts.Event("vm_stopped", vm, fmt.Sprintf("VM %s läuft nicht mehr (zuvor: running). Absturz oder Abschaltung im Gast?", vm))
			}
		}
	}
	c.states = states
}

// ---- Demo: eine Woche plausibler Verlauf ----

func Backfill(st *Store, now time.Time) {
	r := rand.New(rand.NewSource(7))
	day := func(t time.Time) float64 { // Tagesgang: nachts ruhig, nachmittags Last
		h := float64(t.Hour()) + float64(t.Minute())/60
		return 0.5 + 0.5*math.Sin((h-9)/24*2*math.Pi)
	}
	gen := map[string]func(t time.Time) float64{
		"host.cpu":    func(t time.Time) float64 { return clamp(6+22*day(t)+r.NormFloat64()*3+spike(t, 2, 15, 55), 0, 100) },
		"host.load":   func(t time.Time) float64 { return math.Max(0, 0.2+1.4*day(t)+r.NormFloat64()*0.15) },
		"host.mem":    func(t time.Time) float64 { return clamp(31+6*day(t)+r.NormFloat64()*0.8, 0, 100) },
		"host.net.rx": func(t time.Time) float64 { return math.Max(0, 180e3+2.4e6*day(t)+r.NormFloat64()*2e5) },
		"host.net.tx": func(t time.Time) float64 {
			return math.Max(0, 90e3+0.9e6*day(t)+r.NormFloat64()*1e5+spike(t, 2, 15, 38e6))
		},
		"vm.web01.cpu":       func(t time.Time) float64 { return clamp(3+28*day(t)+r.NormFloat64()*4, 0, 100) },
		"vm.web01.mem":       func(t time.Time) float64 { return 742e6 + 40e6*day(t) },
		"vm.web01.rx":        func(t time.Time) float64 { return math.Max(0, 40e3+1.6e6*day(t)+r.NormFloat64()*1e5) },
		"vm.web01.tx":        func(t time.Time) float64 { return math.Max(0, 80e3+2.1e6*day(t)+r.NormFloat64()*1.5e5) },
		"vm.db01.cpu":        func(t time.Time) float64 { return clamp(8+35*day(t)+r.NormFloat64()*5+spike(t, 3, 0, 60), 0, 100) },
		"vm.db01.mem":        func(t time.Time) float64 { return 1.71e9 + 60e6*day(t) },
		"vm.db01.rx":         func(t time.Time) float64 { return math.Max(0, 20e3+0.6e6*day(t)+r.NormFloat64()*5e4) },
		"vm.db01.tx":         func(t time.Time) float64 { return math.Max(0, 15e3+0.4e6*day(t)+r.NormFloat64()*4e4) },
		"host.fs./":          func(time.Time) float64 { return 25 },
		"host.fs./var":       func(time.Time) float64 { return 5 },
		"host.fs./var/vmd":   func(t time.Time) float64 { return 70 + 2*float64(7*24*time.Hour-now.Sub(t))/float64(7*24*time.Hour) },
		"host.fs./home":      func(time.Time) float64 { return 12 },
		"host.fs./usr/local": func(time.Time) float64 { return 23 },
	}
	start := now.Add(-7 * 24 * time.Hour).Truncate(AggStep)
	rawFrom := now.Add(-time.Duration(RawLen) * RawStep)
	for t := start; t.Before(rawFrom); t = t.Add(AggStep) {
		for k, f := range gen {
			st.Add(k, t, f(t))
		}
	}
	for t := rawFrom; t.Before(now); t = t.Add(RawStep) {
		for k, f := range gen {
			if strings.HasPrefix(k, "host.fs.") && t.Second() != 0 {
				continue
			}
			st.Add(k, t, f(t))
		}
	}
}

// spike: nächtliche Last zur Sicherung (hh:mm, 12 Minuten).
func spike(t time.Time, hh, mm int, amount float64) float64 {
	m := t.Hour()*60 + t.Minute() - (hh*60 + mm)
	if m >= 0 && m < 12 {
		return amount
	}
	return 0
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }
