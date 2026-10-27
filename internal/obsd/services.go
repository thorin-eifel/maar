package obsd

import (
	"context"
	"sort"
	"strings"
)

type Service struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Running bool   `json:"running"`
	Failed  bool   `json:"failed"`
	Special bool   `json:"special"` // Variablen wie pf, library_aslr – nicht start-/stoppbar
}

// Spezialvariablen aus rc.conf, die rcctl ls all mit aufführt.
var rcSpecial = map[string]bool{
	"accounting": true, "check_quotas": true, "ipsec": true, "library_aslr": true,
	"multicast": true, "pf": true, "spamd_black": true,
}

func (h *Host) Services(ctx context.Context) ([]Service, error) {
	all, err := h.out(ctx, "rcctl", "ls", "all")
	if err != nil {
		return nil, err
	}
	set := func(arg string) map[string]bool {
		m := map[string]bool{}
		for _, l := range lines(h.outLenient(ctx, "rcctl", "ls", arg)) {
			m[strings.TrimSpace(l)] = true
		}
		return m
	}
	on, started, failed := set("on"), set("started"), set("failed")
	var list []Service
	for _, l := range lines(all) {
		n := strings.TrimSpace(l)
		list = append(list, Service{Name: n, Enabled: on[n], Running: started[n], Failed: failed[n], Special: rcSpecial[n]})
	}
	sort.Slice(list, func(a, b int) bool { return list[a].Name < list[b].Name })
	return list, nil
}

type ServiceDetail struct {
	Name    string `json:"name"`
	Flags   string `json:"flags"`
	Class   string `json:"class"`
	Timeout string `json:"timeout"`
	User    string `json:"user"`
	Status  string `json:"status"`
}

func (h *Host) Service(ctx context.Context, name string) (*ServiceDetail, error) {
	if err := checkName("Dienst", name, reService); err != nil {
		return nil, err
	}
	d := &ServiceDetail{Name: name}
	// rcctl get <dienst> liefert z. B. httpd_class=daemon, httpd_flags=, …
	for _, l := range lines(h.outLenient(ctx, "rcctl", "get", name)) {
		k, v, ok := strings.Cut(l, "=")
		if !ok {
			continue
		}
		switch strings.TrimPrefix(k, name+"_") {
		case "flags":
			d.Flags = v
		case "class":
			d.Class = v
		case "timeout":
			d.Timeout = v
		case "user":
			d.User = v
		case name:
			d.Status = v
		}
	}
	return d, nil
}

func (h *Host) ServiceAction(ctx context.Context, name, action string) (string, error) {
	if err := checkName("Dienst", name, reService); err != nil {
		return "", err
	}
	switch action {
	case "start", "stop", "restart", "reload", "check", "enable", "disable":
	default:
		return "", invalid("Aktion %q", action)
	}
	r, err := h.R.Run(ctx, nil, "rcctl", action, name)
	return strings.TrimSpace(string(r.Stdout) + string(r.Stderr)), err
}

func (h *Host) SetServiceFlags(ctx context.Context, name, flags string) error {
	if err := checkName("Dienst", name, reService); err != nil {
		return err
	}
	if strings.ContainsAny(flags, "\n\x00`$") || len(flags) > 512 {
		return invalid("Flags")
	}
	args := []string{"set", name, "flags"}
	if f := strings.Fields(flags); len(f) > 0 {
		args = append(args, f...)
	} else {
		args = append(args, "")
	}
	return h.run(ctx, "rcctl", args...)
}
