package obsd

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
)

const ntpPath = "/etc/ntpd.conf"

type NTPServer struct {
	Address string `json:"address"`
	Pool    bool   `json:"pool"` // "servers": alle Adressen eines Namens
	Trusted bool   `json:"trusted"`
	Weight  int    `json:"weight"` // 1–10
}

type NTPSensor struct {
	Device  string `json:"device"` // * = alle timedelta-Sensoren
	Trusted bool   `json:"trusted"`
	Weight  int    `json:"weight"`
}

type NTPConstraint struct {
	URL  string `json:"url"`
	Pool bool   `json:"pool"` // "constraints": Median über alle Adressen
}

type NTPConf struct {
	Servers     []NTPServer     `json:"servers"`
	Sensors     []NTPSensor     `json:"sensors"`
	Constraints []NTPConstraint `json:"constraints"`
	Listen      []string        `json:"listen"` // * oder Adressen dieses Hosts
}

type NTPView struct {
	Conf       NTPConf  `json:"conf"`
	Status     string   `json:"status"`
	Enabled    bool     `json:"enabled"`
	Running    bool     `json:"running"`
	Sensors    []string `json:"availableSensors"`
	Addresses  []string `json:"addresses"`
	Preserved  []string `json:"preserved"` // Zeilen, die das Formular nicht kennt
	Suggestion []string `json:"suggestions"`
}

var reSensorName = regexp.MustCompile(`^[A-Za-z0-9_]{1,32}$`)
var reTimedelta = regexp.MustCompile(`(?m)^hw\.sensors\.([A-Za-z0-9_]+)\.timedelta[0-9]*=`)

func unquote(s string) string { return strings.Trim(s, `"`) }

// parseNTPLine versteht nur Zeilen vollständig, die das Formular auch wieder schreiben kann.
func parseNTPLine(t string, c *NTPConf) bool {
	f := strings.Fields(t)
	if len(f) == 0 {
		return false
	}
	opts := func(rest []string) (trusted bool, weight int, ok bool) {
		weight = 1
		for i := 0; i < len(rest); i++ {
			switch rest[i] {
			case "trusted":
				trusted = true
			case "weight":
				if i+1 >= len(rest) {
					return
				}
				weight = atoiSafe(rest[i+1])
				i++
			default:
				return
			}
		}
		return trusted, weight, true
	}
	switch strings.ToLower(f[0]) {
	case "listen":
		if len(f) == 3 && f[1] == "on" {
			if c != nil {
				c.Listen = append(c.Listen, f[2])
			}
			return true
		}
	case "server", "servers":
		if len(f) >= 2 {
			if tr, w, ok := opts(f[2:]); ok && w >= 1 && w <= 10 {
				if c != nil {
					c.Servers = append(c.Servers, NTPServer{Address: unquote(f[1]), Pool: strings.EqualFold(f[0], "servers"), Trusted: tr, Weight: w})
				}
				return true
			}
		}
	case "sensor":
		if len(f) >= 2 {
			if tr, w, ok := opts(f[2:]); ok && w >= 1 && w <= 10 {
				if c != nil {
					c.Sensors = append(c.Sensors, NTPSensor{Device: unquote(f[1]), Trusted: tr, Weight: w})
				}
				return true
			}
		}
	case "constraint", "constraints":
		if len(f) == 3 && f[1] == "from" {
			if c != nil {
				c.Constraints = append(c.Constraints, NTPConstraint{URL: unquote(f[2]), Pool: strings.EqualFold(f[0], "constraints")})
			}
			return true
		}
	}
	return false
}

func (h *Host) NTP(ctx context.Context) (*NTPView, error) {
	src, err := h.readFile(ntpPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	v := &NTPView{Conf: NTPConf{Servers: []NTPServer{}, Sensors: []NTPSensor{}, Constraints: []NTPConstraint{}, Listen: []string{}}, Sensors: []string{}, Preserved: []string{}}
	for _, l := range splitFileLines(src) {
		if isCommentOrBlank(l) {
			continue
		}
		if !parseNTPLine(strings.TrimSpace(l), &v.Conf) {
			v.Preserved = append(v.Preserved, strings.TrimSpace(l))
		}
	}
	v.Enabled, v.Running = h.rcActive(ctx, "ntpd")
	if v.Running {
		v.Status = strings.TrimSpace(h.outLenient(ctx, "ntpctl", "-s", "all"))
	}
	for _, m := range reTimedelta.FindAllStringSubmatch(h.outLenient(ctx, "sysctl", "hw.sensors"), -1) {
		v.Sensors = append(v.Sensors, m[1])
	}
	v.Addresses = h.localAddrs(ctx)
	v.Suggestion = []string{"pool.ntp.org", "ptbtime1.ptb.de", "de.pool.ntp.org", "time.cloudflare.com"}
	return v, nil
}

func (h *Host) validateNTP(ctx context.Context, c *NTPConf, cur *NTPView) error {
	if len(c.Servers)+len(c.Sensors) == 0 {
		return invalid("Mindestens eine Zeitquelle (Server oder Sensor) ist nötig")
	}
	if len(c.Servers) > 20 || len(c.Sensors) > 10 || len(c.Constraints) > 10 || len(c.Listen) > 20 {
		return invalid("zu viele Einträge")
	}
	seen := map[string]bool{}
	for i := range c.Servers {
		s := &c.Servers[i]
		if !validHostOrIP(s.Address) {
			return invalid("Zeitserver %q", s.Address)
		}
		if s.Weight == 0 {
			s.Weight = 1
		}
		if s.Weight < 1 || s.Weight > 10 {
			return invalid("Gewicht %d (1–10)", s.Weight)
		}
		if seen[s.Address] {
			return invalid("Zeitserver %s ist doppelt", s.Address)
		}
		seen[s.Address] = true
	}
	known := map[string]bool{"*": true}
	for _, s := range cur.Sensors {
		known[s] = true
	}
	for _, s := range cur.Conf.Sensors {
		known[s.Device] = true
	}
	for i := range c.Sensors {
		s := &c.Sensors[i]
		if !reSensorName.MatchString(s.Device) && s.Device != "*" {
			return invalid("Sensor %q", s.Device)
		}
		if !known[s.Device] {
			return invalid("Sensor %q gibt es auf diesem Host nicht", s.Device)
		}
		if s.Weight == 0 {
			s.Weight = 1
		}
		if s.Weight < 1 || s.Weight > 10 {
			return invalid("Gewicht %d (1–10)", s.Weight)
		}
	}
	for _, k := range c.Constraints {
		if !reURLHTTPS.MatchString(k.URL) {
			return invalid("Constraint-Adresse %q (https://… oder Name/IP)", k.URL)
		}
	}
	local := map[string]bool{"*": true}
	for _, a := range cur.Addresses {
		local[a] = true
	}
	for _, a := range cur.Conf.Listen {
		local[a] = true
	}
	for _, a := range c.Listen {
		if !local[a] {
			return invalid("%q ist keine Adresse dieses Hosts", a)
		}
	}
	return nil
}

func (c *NTPConf) lines() []string {
	var out []string
	for _, a := range c.Listen {
		out = append(out, "listen on "+a)
	}
	opt := func(tr bool, w int) string {
		s := ""
		if tr {
			s += " trusted"
		}
		if w > 1 {
			s += fmt.Sprintf(" weight %d", w)
		}
		return s
	}
	for _, s := range c.Servers {
		kw := "server"
		if s.Pool {
			kw = "servers"
		}
		out = append(out, kw+" "+s.Address+opt(s.Trusted, s.Weight))
	}
	for _, s := range c.Sensors {
		out = append(out, "sensor "+s.Device+opt(s.Trusted, s.Weight))
	}
	for _, k := range c.Constraints {
		kw := "constraint"
		if k.Pool {
			kw = "constraints"
		}
		out = append(out, fmt.Sprintf("%s from %q", kw, k.URL))
	}
	return out
}

// SaveNTP schreibt /etc/ntpd.conf. activate: ntpd aktivieren und starten, falls er aus ist.
func (h *Host) SaveNTP(ctx context.Context, c *NTPConf, activate, preview bool) (*ConfResult, error) {
	cur, err := h.NTP(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.validateNTP(ctx, c, cur); err != nil {
		return nil, err
	}
	src, _ := h.readFile(ntpPath)
	content := rewriteLines(src, func(t string) bool { return parseNTPLine(t, nil) }, c.lines(), nil)
	res := &ConfResult{Warnings: []string{}}
	if len(c.Listen) > 0 {
		res.Warnings = append(res.Warnings, "ntpd beantwortet jetzt Zeitanfragen auf den gewählten Adressen – der Paketfilter muss udp/123 dort erlauben.")
	}
	if len(c.Servers) > 0 && len(c.Constraints) == 0 {
		res.Warnings = append(res.Warnings, "Ohne Constraints vertraut ntpd den Zeitservern blind. Mit einer https-Quelle prüft er die Zeit zusätzlich.")
	}
	if err := h.putConf(ctx, ntpPath, content, preview, res); err != nil {
		return nil, err
	}
	if preview {
		return res, nil
	}
	enabled, running := h.rcActive(ctx, "ntpd")
	switch {
	case running:
		err = h.run(ctx, "rcctl", "restart", "ntpd")
		res.Applied = "ntpd neu gestartet."
	case activate && !enabled:
		if err = h.run(ctx, "rcctl", "enable", "ntpd"); err == nil {
			err = h.run(ctx, "rcctl", "start", "ntpd")
		}
		res.Applied = "ntpd aktiviert und gestartet."
	case activate:
		err = h.run(ctx, "rcctl", "start", "ntpd")
		res.Applied = "ntpd gestartet."
	default:
		res.Applied = "Gespeichert; ntpd läuft nicht und wurde nicht gestartet."
	}
	if err != nil {
		return nil, fmt.Errorf("Datei gespeichert, aber ntpd ließ sich nicht (neu) starten: %w", err)
	}
	return res, nil
}
