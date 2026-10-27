package obsd

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Formulare für ntpd.conf, resolv.conf, hosts und sshd_config: Die Oberfläche
// bietet nur an, was es auf dem Host gibt; geschrieben wird im Hintergrund.
// Kommentare und alles, was das Formular nicht kennt, bleiben unverändert.

// ConfResult beschreibt, was Speichern bewirkt hat.
type ConfResult struct {
	Content  string   `json:"content,omitempty"` // Vorschau (Trockenlauf)
	Warnings []string `json:"warnings"`
	Output   string   `json:"output,omitempty"`
	Applied  string   `json:"applied,omitempty"`
	Pending  int      `json:"pendingSeconds,omitempty"` // Frist zum Bestätigen
}

func splitFileLines(src string) []string {
	src = strings.TrimRight(src, "\n")
	if src == "" {
		return nil
	}
	return strings.Split(src, "\n")
}

func isCommentOrBlank(l string) bool {
	t := strings.TrimSpace(l)
	return t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, ";")
}

// rewriteLines entfernt alle verwalteten Zeilen und setzt repl an die Stelle der ersten.
// Gab es keine, steht repl vor der ersten Zeile, für die before true ist, sonst am Ende.
func rewriteLines(src string, managed func(trimmed string) bool, repl []string, before func(trimmed string) bool) string {
	var out []string
	pos := -1
	for _, l := range splitFileLines(src) {
		t := strings.TrimSpace(l)
		if !isCommentOrBlank(l) && managed(t) {
			if pos < 0 {
				pos = len(out)
			}
			continue
		}
		out = append(out, l)
	}
	if pos < 0 {
		pos = len(out)
		for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" && before == nil {
			out = out[:len(out)-1]
			pos = len(out)
		}
		if before != nil {
			for i, l := range out {
				if !isCommentOrBlank(l) && before(strings.TrimSpace(l)) {
					pos = i
					break
				}
			}
		}
	}
	merged := append(append(append([]string{}, out[:pos]...), repl...), out[pos:]...)
	if len(merged) == 0 {
		return ""
	}
	return strings.Join(merged, "\n") + "\n"
}

// firstWord liefert das erste Wort einer Zeile in Kleinbuchstaben.
func firstWord(t string) string {
	f := strings.Fields(t)
	if len(f) == 0 {
		return ""
	}
	return strings.ToLower(f[0])
}

// putConf schreibt über WriteConfig (Prüfung, Sicherung) – oder liefert nur die Vorschau.
func (h *Host) putConf(ctx context.Context, path, content string, preview bool, res *ConfResult) error {
	if preview {
		res.Content = content
		return nil
	}
	if cur, err := h.readFile(path); err == nil && cur == content {
		res.Output = "Keine Änderung."
		return nil
	}
	r, err := h.WriteConfig(ctx, path, content, 0)
	if err != nil {
		return err
	}
	res.Output = r.Check
	return nil
}

func (h *Host) rcActive(ctx context.Context, svc string) (enabled, running bool) {
	return h.run(ctx, "rcctl", "get", svc, "status") == nil, h.run(ctx, "rcctl", "check", svc) == nil
}

// ---- Hilfen für Adressen ----

var (
	reHostLabel = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$`)
	reDomainSet = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)
	reURLHTTPS  = regexp.MustCompile(`^(https://)?(\[[0-9A-Fa-f:.]+\]|[A-Za-z0-9.-]{1,253})(:[0-9]{1,5})?(/[A-Za-z0-9._~/%-]{0,200})?$`)
)

func validHostOrIP(s string) bool {
	if s == "" || len(s) > 253 {
		return false
	}
	if _, err := netip.ParseAddr(s); err == nil {
		return true
	}
	return reHostLabel.MatchString(s)
}

// localAddrs sind die Adressen der Interfaces dieses Hosts (für „listen on“ u. ä.).
func (h *Host) localAddrs(ctx context.Context) []string {
	out := []string{}
	n, err := h.Network(ctx)
	if err != nil {
		return out
	}
	for _, i := range n.Interfaces {
		for _, a := range i.Addrs {
			if !strings.HasPrefix(a.Addr, "fe80") {
				out = append(out, a.Addr)
			}
		}
	}
	return out
}

func atoiSafe(s string) int { n, _ := strconv.Atoi(s); return n }

var _ = fmt.Sprintf
var _ = os.Stat
