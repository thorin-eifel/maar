package metrics

import (
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thorin-eifel/maar/internal/obsd"
)

func TestStoreResolutions(t *testing.T) {
	st := NewStore()
	now := time.Now()
	start := now.Add(-3 * time.Hour)
	for ts := start; ts.Before(now); ts = ts.Add(RawStep) {
		st.Add("x", ts, float64(ts.Minute()))
	}
	raw := st.Query("x", now.Add(-time.Hour))
	if len(raw) < 355 || len(raw) > 361 {
		t.Errorf("1 h Rohwerte: %d Punkte", len(raw))
	}
	agg := st.Query("x", now.Add(-6*time.Hour))
	if len(agg) < 35 || len(agg) > 38 {
		t.Errorf("6 h Mittelwerte: %d Punkte", len(agg))
	}
	if v, ok := st.Last("x", time.Minute); !ok || v != float64(now.Add(-RawStep).Minute()) && v != float64(now.Add(-2*RawStep).Minute()) {
		t.Errorf("Last: %v %v", v, ok)
	}
	p := filepath.Join(t.TempDir(), "m.json.gz")
	if err := st.Save(p); err != nil {
		t.Fatal(err)
	}
	st2 := NewStore()
	if err := st2.Load(p); err != nil || len(st2.Query("x", now.Add(-time.Hour))) != len(raw) {
		t.Errorf("Laden: %v", err)
	}
	st2.Add("x", now, 1) // Ring nach dem Laden weiter benutzbar
}

func TestMatchMetric(t *testing.T) {
	for _, c := range []struct {
		p, k string
		ok   bool
	}{
		{"host.cpu", "host.cpu", true},
		{"vm.*.cpu", "vm.web01.cpu", true},
		{"vm.*.cpu", "vm.web01.mem", false},
		{"host.fs.*", "host.fs./var/vmd", true},
		{"host.fs.*", "host.cpu", false},
		{"vm.*.cpu", "vm.cpu", false},
	} {
		if matchMetric(c.p, c.k) != c.ok {
			t.Errorf("%s ~ %s: erwartet %v", c.p, c.k, c.ok)
		}
	}
}

func demoCollector(t *testing.T) (*Collector, *obsd.Host) {
	t.Helper()
	d, root, err := obsd.NewDemo()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	h := obsd.NewHost(d, root, true)
	return NewCollector(h, log.New(io.Discard, "", 0)), h
}

func TestSamplerAndEvents(t *testing.T) {
	c, h := demoCollector(t)
	ctx := context.Background()
	c.collect(ctx, true)
	time.Sleep(1100 * time.Millisecond)
	c.collect(ctx, false)
	for _, k := range []string{"host.cpu", "host.mem", "host.load", "host.net.rx", "vm.web01.cpu", "vm.web01.mem", "vm.db01.tx", "host.fs./var/vmd"} {
		if v, ok := c.Store.Last(k, time.Minute); !ok || v < 0 {
			t.Errorf("%s fehlt (%v, %v)", k, v, ok)
		}
	}
	if v, _ := c.Store.Last("host.cpu", time.Minute); v <= 0 || v > 100 {
		t.Errorf("host.cpu = %v", v)
	}

	// Gewollter Stopp: kein Ereignis. Ungewollter: Ereignis.
	c.Alerts.SetSettings(nil, map[string]bool{"vm_stopped": true})
	h.VMAction(ctx, "web01", "stop", nil)
	c.collect(ctx, false)
	h.R.Run(ctx, nil, "vmctl", "stop", "db01") // an maar vorbei
	c.collect(ctx, false)
	_, hist := c.Alerts.Snapshot()
	var stopped []string
	for _, e := range hist {
		if e.Rule == "vm_stopped" {
			stopped = append(stopped, e.Subject)
		}
	}
	if len(stopped) != 1 || stopped[0] != "db01" {
		t.Errorf("VM-Stopp-Ereignisse: %v", stopped)
	}
	// Abgeschaltete Ereignisart wird nicht protokolliert.
	h.Emit("backup_failed", "Nächtlich", "Ziel nicht erreichbar")
	_, hist = c.Alerts.Snapshot()
	if hist[len(hist)-1].Rule == "backup_failed" {
		t.Error("abgeschaltetes Ereignis wurde protokolliert")
	}
}

func TestRuleEvaluation(t *testing.T) {
	c, _ := demoCollector(t)
	a := c.Alerts
	a.SetSettings([]string{"root"}, map[string]bool{})
	if err := a.SaveRule(&Rule{Name: "Test", Metric: "vm.*.cpu", Op: ">", Threshold: 80, For: 60, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	t0 := time.Now()
	a.Eval(map[string]float64{"vm.web01.cpu": 95}, t0)
	if act, _ := a.Snapshot(); len(act) != 0 {
		t.Fatal("Alarm vor Ablauf der Mindestdauer")
	}
	a.Eval(map[string]float64{"vm.web01.cpu": 97}, t0.Add(61*time.Second))
	act, hist := a.Snapshot()
	if len(act) != 1 || act[0].Subject != "vm.web01.cpu" || hist[len(hist)-1].Kind != "firing" {
		t.Fatalf("Alarm nicht ausgelöst: %+v", act)
	}
	a.Eval(map[string]float64{"vm.web01.cpu": 20}, t0.Add(70*time.Second))
	act, hist = a.Snapshot()
	if len(act) != 0 || hist[len(hist)-1].Kind != "resolved" {
		t.Errorf("Alarm nicht aufgehoben")
	}
	// Kurzer Ausreißer unter der Mindestdauer löst nichts aus.
	a.Eval(map[string]float64{"vm.web01.cpu": 99}, t0.Add(80*time.Second))
	a.Eval(map[string]float64{"vm.web01.cpu": 10}, t0.Add(100*time.Second))
	a.Eval(map[string]float64{"vm.web01.cpu": 99}, t0.Add(110*time.Second))
	if act, _ = a.Snapshot(); len(act) != 0 {
		t.Error("Ausreißer hat Alarm ausgelöst")
	}
	for _, bad := range []*Rule{
		{Name: "", Metric: "host.cpu", Op: ">"},
		{Name: "x", Metric: "host.cpu; rm", Op: ">"},
		{Name: "x", Metric: "host.cpu", Op: "=="},
	} {
		if err := a.SaveRule(bad); err == nil {
			t.Errorf("%+v akzeptiert", bad)
		}
	}
	if err := a.SetSettings([]string{"a@b.de", "x y@z"}, nil); err == nil {
		t.Error("ungültiger Empfänger akzeptiert")
	}
	time.Sleep(200 * time.Millisecond) // Mailversand im Hintergrund abwarten
}

func TestBackfill(t *testing.T) {
	st := NewStore()
	Backfill(st, time.Now())
	if n := len(st.Query("host.cpu", time.Now().Add(-7*24*time.Hour))); n < 2000 {
		t.Errorf("7 Tage: %d Punkte", n)
	}
	if len(st.Keys("vm.")) < 8 {
		t.Errorf("VM-Reihen: %v", st.Keys("vm."))
	}
}
