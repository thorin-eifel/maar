package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/thorin-eifel/maar/internal/obsd"
)

// Alarme: Schwellwertregeln auf Messreihen (mit Mindestdauer) und Ereignisse
// (Dienst ausgefallen, VM unerwartet gestoppt, Sicherung fehlgeschlagen).
// Benachrichtigung per mail(1) über den lokalen smtpd.

const alertConfPath = "/var/maar/alerts.json"

type Rule struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Metric    string  `json:"metric"` // Schlüssel, * als Platzhalter für ein Segment
	Op        string  `json:"op"`     // > oder <
	Threshold float64 `json:"threshold"`
	For       int     `json:"for"` // Sekunden
	Severity  string  `json:"severity"`
	Enabled   bool    `json:"enabled"`
}

type AlertConfig struct {
	Recipients []string        `json:"recipients"`
	Events     map[string]bool `json:"events"`
	Rules      []*Rule         `json:"rules"`
}

type Event struct {
	Time     int64   `json:"time"`
	Kind     string  `json:"kind"` // firing, resolved, event
	Rule     string  `json:"rule"`
	Name     string  `json:"name"`
	Subject  string  `json:"subject"`
	Value    float64 `json:"value,omitempty"`
	Message  string  `json:"message"`
	Severity string  `json:"severity"`
	Mailed   bool    `json:"mailed"`
	MailErr  string  `json:"mailError,omitempty"`
}

type Active struct {
	Rule     string  `json:"rule"`
	Name     string  `json:"name"`
	Subject  string  `json:"subject"`
	Since    int64   `json:"since"`
	Value    float64 `json:"value"`
	Severity string  `json:"severity"`
	Firing   bool    `json:"firing"`
}

// EventKinds sind die abschaltbaren Ereignisalarme.
var EventKinds = map[string]string{
	"service_failed": "Dienst ausgefallen",
	"vm_stopped":     "VM unerwartet gestoppt",
	"backup_failed":  "Sicherung fehlgeschlagen",
	"access_denied":  "Zugriff verweigert (Rolle)",
	"login_failed":   "Anmeldung fehlgeschlagen",
	"pf_reverted":    "Paketfilter-Änderung automatisch zurückgenommen",
}

func DefaultAlertConfig() *AlertConfig {
	return &AlertConfig{
		Recipients: []string{},
		Events:     map[string]bool{"service_failed": true, "vm_stopped": true, "backup_failed": true},
		Rules: []*Rule{
			{ID: "host-cpu", Name: "Host-CPU hoch", Metric: "host.cpu", Op: ">", Threshold: 90, For: 300, Severity: "warning", Enabled: true},
			{ID: "host-mem", Name: "Host-Speicher knapp", Metric: "host.mem", Op: ">", Threshold: 90, For: 300, Severity: "warning", Enabled: true},
			{ID: "fs-full", Name: "Dateisystem fast voll", Metric: "host.fs.*", Op: ">", Threshold: 90, For: 0, Severity: "critical", Enabled: true},
			{ID: "vm-cpu", Name: "VM dauerhaft unter Volllast", Metric: "vm.*.cpu", Op: ">", Threshold: 95, For: 900, Severity: "warning", Enabled: true},
		},
	}
}

type Alerter struct {
	H        *obsd.Host
	mu       sync.Mutex
	cfg      *AlertConfig
	state    map[string]*Active // Regel|Schlüssel
	history  []Event
	lastMail map[string]time.Time
	hostname string
}

func NewAlerter(h *obsd.Host) *Alerter {
	a := &Alerter{H: h, state: map[string]*Active{}, lastMail: map[string]time.Time{}, hostname: h.Hostname(context.Background())}
	a.cfg = DefaultAlertConfig()
	if b, err := os.ReadFile(h.P(alertConfPath)); err == nil {
		var saved struct {
			AlertConfig
			History []Event `json:"history"`
		}
		if json.Unmarshal(b, &saved) == nil {
			if saved.Events == nil {
				saved.Events = a.cfg.Events
			}
			if saved.Recipients == nil {
				saved.Recipients = []string{}
			}
			cfg := saved.AlertConfig
			a.cfg, a.history = &cfg, saved.History
		}
	}
	return a
}

func (a *Alerter) save() {
	b, _ := json.MarshalIndent(struct {
		*AlertConfig
		History []Event `json:"history"`
	}{a.cfg, a.history}, "", "  ")
	p := a.H.P(alertConfPath)
	os.MkdirAll(filepath.Dir(p), 0o700)
	os.WriteFile(p+".tmp", b, 0o600)
	os.Rename(p+".tmp", p)
}

func (a *Alerter) Config() AlertConfig {
	a.mu.Lock()
	defer a.mu.Unlock()
	c := *a.cfg
	c.Rules = append([]*Rule(nil), a.cfg.Rules...)
	return c
}

func (a *Alerter) Snapshot() ([]Active, []Event) {
	a.mu.Lock()
	defer a.mu.Unlock()
	act := []Active{}
	for _, s := range a.state {
		if s.Firing {
			act = append(act, *s)
		}
	}
	h := append([]Event(nil), a.history...)
	if h == nil {
		h = []Event{}
	}
	return act, h
}

var (
	reMail   = regexp.MustCompile(`^[A-Za-z0-9._%+-]{1,64}@[A-Za-z0-9.-]{1,253}$|^[a-z_][a-z0-9_-]{0,30}$`)
	reMetric = regexp.MustCompile(`^[a-z]+(\.[A-Za-z0-9_*/.-]+)+$`)
)

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", obsd.ErrInvalid, fmt.Sprintf(format, a...))
}

func (a *Alerter) SetSettings(recipients []string, events map[string]bool) error {
	clean := []string{}
	for _, r := range recipients {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		if !reMail.MatchString(r) {
			return invalid("Empfänger %q", r)
		}
		clean = append(clean, r)
	}
	if len(clean) > 10 {
		return invalid("höchstens 10 Empfänger")
	}
	ev := map[string]bool{}
	for k := range EventKinds {
		ev[k] = events[k]
	}
	a.mu.Lock()
	a.cfg.Recipients, a.cfg.Events = clean, ev
	a.save()
	a.mu.Unlock()
	return nil
}

func (a *Alerter) SaveRule(r *Rule) error {
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" || len(r.Name) > 60 || strings.ContainsAny(r.Name, "\n\x00") {
		return invalid("Name der Regel")
	}
	if !reMetric.MatchString(r.Metric) {
		return invalid("Messwert %q", r.Metric)
	}
	if r.Op != ">" && r.Op != "<" {
		return invalid("Vergleich %q", r.Op)
	}
	if r.For < 0 || r.For > 86400 {
		return invalid("Dauer")
	}
	if r.Severity != "warning" && r.Severity != "critical" {
		r.Severity = "warning"
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if r.ID == "" {
		base := strings.Trim(regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(strings.ToLower(r.Name), "-"), "-")
		if base == "" {
			base = "regel"
		}
		id := base
		for i := 2; a.rule(id) != nil; i++ {
			id = fmt.Sprintf("%s-%d", base, i)
		}
		r.ID = id
		a.cfg.Rules = append(a.cfg.Rules, r)
	} else {
		found := false
		for i, x := range a.cfg.Rules {
			if x.ID == r.ID {
				a.cfg.Rules[i], found = r, true
			}
		}
		if !found {
			return invalid("Regel %q existiert nicht", r.ID)
		}
		a.clearRule(r.ID)
	}
	a.save()
	return nil
}

func (a *Alerter) DeleteRule(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i, x := range a.cfg.Rules {
		if x.ID == id {
			a.cfg.Rules = append(a.cfg.Rules[:i], a.cfg.Rules[i+1:]...)
			a.clearRule(id)
			a.save()
			return nil
		}
	}
	return invalid("Regel %q existiert nicht", id)
}

func (a *Alerter) rule(id string) *Rule {
	for _, r := range a.cfg.Rules {
		if r.ID == id {
			return r
		}
	}
	return nil
}

func (a *Alerter) clearRule(id string) {
	for k := range a.state {
		if strings.HasPrefix(k, id+"|") {
			delete(a.state, k)
		}
	}
}

// matchMetric prüft Schlüssel gegen ein Muster; * steht für genau ein Segment
// (bei host.fs.* für den Rest, da Einhängepunkte Punkte enthalten können).
func matchMetric(pattern, key string) bool {
	if pattern == key {
		return true
	}
	ps, ks := strings.Split(pattern, "."), strings.Split(key, ".")
	for i, p := range ps {
		if i >= len(ks) {
			return false
		}
		if p == "*" {
			if i == len(ps)-1 {
				return true
			}
			continue
		}
		if p != ks[i] {
			return false
		}
	}
	return len(ps) == len(ks)
}

// Eval wertet alle Regeln gegen die aktuellen Werte aus.
func (a *Alerter) Eval(values map[string]float64, now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	seen := map[string]bool{}
	for _, r := range a.cfg.Rules {
		if !r.Enabled {
			continue
		}
		for key, v := range values {
			if !matchMetric(r.Metric, key) {
				continue
			}
			id := r.ID + "|" + key
			seen[id] = true
			bad := (r.Op == ">" && v > r.Threshold) || (r.Op == "<" && v < r.Threshold)
			st := a.state[id]
			switch {
			case bad && st == nil:
				st = &Active{Rule: r.ID, Name: r.Name, Subject: key, Since: now.Unix(), Value: v, Severity: r.Severity}
				a.state[id] = st
				fallthrough
			case bad:
				st.Value = v
				if !st.Firing && now.Unix()-st.Since >= int64(r.For) {
					st.Firing = true
					a.record(Event{Kind: "firing", Rule: r.ID, Name: r.Name, Subject: key, Value: v, Severity: r.Severity,
						Message: fmt.Sprintf("%s: %s %s %s seit %s", r.Name, key, r.Op, fmtNum(r.Threshold), time.Unix(st.Since, 0).Format("15:04"))}, now)
				}
			case st != nil:
				if st.Firing {
					a.record(Event{Kind: "resolved", Rule: r.ID, Name: r.Name, Subject: key, Value: v, Severity: r.Severity,
						Message: fmt.Sprintf("%s behoben: %s = %s", r.Name, key, fmtNum(v))}, now)
				}
				delete(a.state, id)
			}
		}
	}
	// Reihen, die verschwunden sind (z. B. VM gestoppt), beenden ihren Alarm still.
	for id := range a.state {
		if !seen[id] {
			delete(a.state, id)
		}
	}
}

func fmtNum(v float64) string {
	return strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%.1f", v), "0"), ".")
}

// Event meldet ein Ereignis; abgeschaltete Arten werden nur protokolliert.
func (a *Alerter) Event(kind, subject, message string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.cfg.Events[kind] {
		return
	}
	a.record(Event{Kind: "event", Rule: kind, Name: EventKinds[kind], Subject: subject, Message: message, Severity: "critical"}, time.Now())
}

// record speichert ein Ereignis und verschickt die Mail außerhalb der Sperre.
func (a *Alerter) record(e Event, now time.Time) {
	e.Time = now.Unix()
	key := e.Rule + "|" + e.Subject + "|" + e.Kind
	mail := len(a.cfg.Recipients) > 0 && now.Sub(a.lastMail[key]) > 30*time.Minute
	if mail {
		a.lastMail[key] = now
	}
	a.history = append(a.history, e)
	if len(a.history) > 300 {
		a.history = a.history[len(a.history)-300:]
	}
	idx := len(a.history) - 1
	a.save()
	if !mail {
		return
	}
	rcpt := append([]string(nil), a.cfg.Recipients...)
	go func() {
		err := a.send(rcpt, e)
		a.mu.Lock()
		if idx < len(a.history) && a.history[idx].Time == e.Time && a.history[idx].Subject == e.Subject {
			a.history[idx].Mailed = err == nil
			if err != nil {
				a.history[idx].MailErr = err.Error()
			}
			a.save()
		}
		a.mu.Unlock()
	}()
}

func (a *Alerter) send(rcpt []string, e Event) error {
	state := map[string]string{"firing": "ALARM", "resolved": "BEHOBEN", "event": "EREIGNIS"}[e.Kind]
	subject := fmt.Sprintf("[maar %s] %s: %s", a.hostname, state, e.Name)
	body := fmt.Sprintf("%s\n\nHost:     %s\nBetreff:  %s\nZeit:     %s\nSchwere:  %s\n\n-- \nmaar – Alarme lassen sich unter „Leistung“ verwalten.\n",
		e.Message, a.hostname, e.Subject, time.Unix(e.Time, 0).Format("02.01.2006 15:04:05"), e.Severity)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := a.H.R.Run(ctx, []byte(body), "mail", append([]string{"-s", subject}, rcpt...)...)
	return err
}

// Test verschickt eine Probemail an alle Empfänger.
func (a *Alerter) Test() error {
	a.mu.Lock()
	rcpt := append([]string(nil), a.cfg.Recipients...)
	a.mu.Unlock()
	if len(rcpt) == 0 {
		return invalid("keine Empfänger eingetragen")
	}
	return a.send(rcpt, Event{Kind: "event", Name: "Testnachricht", Subject: "maar", Message: "Diese Nachricht bestätigt, dass Alarme per Mail ankommen.", Severity: "info", Time: time.Now().Unix()})
}
