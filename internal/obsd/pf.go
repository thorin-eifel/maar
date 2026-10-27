package obsd

import (
	"context"
	"strings"
)

type PFStatus struct {
	Enabled bool     `json:"enabled"`
	Info    string   `json:"info"`
	Rules   []string `json:"rules"`
	States  int      `json:"states"`
	Tables  []string `json:"tables"`
}

func (h *Host) PF(ctx context.Context) (*PFStatus, error) {
	info, err := h.out(ctx, "pfctl", "-si")
	if err != nil {
		return nil, err
	}
	st := &PFStatus{Info: info, Rules: []string{}, Tables: []string{}}
	st.Enabled = strings.HasPrefix(strings.TrimSpace(info), "Status: Enabled")
	for _, l := range lines(info) {
		f := strings.Fields(l)
		if len(f) >= 3 && f[0] == "current" && f[1] == "entries" {
			st.States = atoi(f[2])
		}
	}
	if r, err := h.out(ctx, "pfctl", "-sr"); err == nil {
		st.Rules = lines(r)
	}
	if t, err := h.out(ctx, "pfctl", "-sT"); err == nil {
		for _, l := range lines(t) {
			st.Tables = append(st.Tables, strings.TrimSpace(l))
		}
	}
	return st, nil
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}

type PFState struct {
	Raw string `json:"raw"`
}

// PFStates liefert höchstens limit Zeilen aus "pfctl -ss".
func (h *Host) PFStates(ctx context.Context, limit int) ([]string, error) {
	out, err := h.out(ctx, "pfctl", "-ss")
	if err != nil {
		return nil, err
	}
	l := lines(out)
	if len(l) > limit {
		l = l[:limit]
	}
	return l, nil
}

func (h *Host) PFTable(ctx context.Context, table string) ([]string, error) {
	if err := checkName("Tabelle", table, reTable); err != nil {
		return nil, err
	}
	out, err := h.out(ctx, "pfctl", "-t", table, "-T", "show")
	if err != nil {
		return nil, err
	}
	var addrs []string
	for _, l := range lines(out) {
		addrs = append(addrs, strings.TrimSpace(l))
	}
	return addrs, nil
}

func (h *Host) PFTableEdit(ctx context.Context, table, op, addr string) error {
	if err := checkName("Tabelle", table, reTable); err != nil {
		return err
	}
	if !reAddr.MatchString(addr) {
		return invalid("Adresse %q", addr)
	}
	if op != "add" && op != "delete" {
		return invalid("Operation %q", op)
	}
	return h.run(ctx, "pfctl", "-t", table, "-T", op, addr)
}

func (h *Host) PFAction(ctx context.Context, action string) (string, error) {
	var args []string
	switch action {
	case "enable":
		args = []string{"-e"}
	case "disable":
		args = []string{"-d"}
	case "reload":
		args = []string{"-f", "/etc/pf.conf"}
	case "flush-states":
		args = []string{"-F", "states"}
	default:
		return "", invalid("Aktion %q", action)
	}
	r, err := h.R.Run(ctx, nil, "pfctl", args...)
	return strings.TrimSpace(string(r.Stdout) + string(r.Stderr)), err
}
