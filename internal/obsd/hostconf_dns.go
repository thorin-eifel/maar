package obsd

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"sort"
	"strings"
)

const (
	resolvPath = "/etc/resolv.conf"
	hostsPath  = "/etc/hosts"
)

// ---- resolv.conf ----

type ResolvConf struct {
	Nameservers []string `json:"nameservers"`
	Domain      string   `json:"domain"`
	Search      []string `json:"search"`
	Lookup      []string `json:"lookup"` // bind, file
	Family      []string `json:"family"` // inet4, inet6
	EDNS0       bool     `json:"edns0"`
	TCP         bool     `json:"tcp"`
	TrustAD     bool     `json:"trustAd"`
	NDots       int      `json:"ndots"` // 0 = Standard (1)
}

type ResolvSuggestion struct {
	Address string `json:"address"`
	Label   string `json:"label"`
}

type ResolvView struct {
	Conf        ResolvConf         `json:"conf"`
	Preserved   []string           `json:"preserved"`
	OtherOpts   []string           `json:"otherOptions"`
	Resolvd     bool               `json:"resolvd"`
	Suggestions []ResolvSuggestion `json:"suggestions"`
}

var resolvKnownOpt = map[string]bool{"edns0": true, "tcp": true, "trust-ad": true}

func parseResolv(src string) (c ResolvConf, preserved, other []string) {
	c = ResolvConf{Nameservers: []string{}, Search: []string{}, Lookup: []string{}, Family: []string{}}
	for _, l := range splitFileLines(src) {
		if isCommentOrBlank(l) {
			continue
		}
		f := strings.Fields(strings.SplitN(strings.SplitN(l, "#", 2)[0], ";", 2)[0])
		if len(f) == 0 {
			continue
		}
		switch strings.ToLower(f[0]) {
		case "nameserver":
			if len(f) >= 2 {
				c.Nameservers = append(c.Nameservers, f[1])
			}
		case "domain":
			if len(f) >= 2 {
				c.Domain = f[1]
			}
		case "search":
			c.Search = f[1:]
		case "lookup":
			c.Lookup = f[1:]
		case "family":
			c.Family = f[1:]
		case "options":
			for _, o := range f[1:] {
				switch {
				case o == "edns0":
					c.EDNS0 = true
				case o == "tcp":
					c.TCP = true
				case o == "trust-ad":
					c.TrustAD = true
				case strings.HasPrefix(o, "ndots:"):
					c.NDots = atoiSafe(strings.TrimPrefix(o, "ndots:"))
				default:
					other = append(other, o)
				}
			}
		default:
			preserved = append(preserved, strings.TrimSpace(l))
		}
	}
	return
}

func isResolvManaged(t string) bool {
	switch firstWord(t) {
	case "nameserver", "domain", "search", "lookup", "family", "options":
		return true
	}
	return false
}

func (h *Host) Resolv(ctx context.Context) (*ResolvView, error) {
	src, err := h.readFile(resolvPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	c, pres, other := parseResolv(src)
	v := &ResolvView{Conf: c, Preserved: append([]string{}, pres...), OtherOpts: append([]string{}, other...)}
	en, run := h.rcActive(ctx, "resolvd")
	v.Resolvd = en || run
	local := false
	for _, s := range []string{"unbound", "unwind"} {
		if _, r := h.rcActive(ctx, s); r {
			local = true
		}
	}
	if local {
		v.Suggestions = append(v.Suggestions, ResolvSuggestion{"127.0.0.1", "Lokaler Resolver (unbound/unwind läuft)"})
	}
	if n, err := h.Network(ctx); err == nil && n.Gateway != "" {
		v.Suggestions = append(v.Suggestions, ResolvSuggestion{n.Gateway, "Standard-Gateway (Router)"})
	}
	v.Suggestions = append(v.Suggestions, ResolvSuggestion{"9.9.9.9", "Quad9"}, ResolvSuggestion{"2620:fe::fe", "Quad9 (IPv6)"}, ResolvSuggestion{"1.1.1.1", "Cloudflare"}, ResolvSuggestion{"8.8.8.8", "Google"})
	return v, nil
}

func (c *ResolvConf) lines(other []string) []string {
	var out []string
	if len(c.Lookup) > 0 {
		out = append(out, "lookup "+strings.Join(c.Lookup, " "))
	}
	if len(c.Family) > 0 {
		out = append(out, "family "+strings.Join(c.Family, " "))
	}
	if c.Domain != "" {
		out = append(out, "domain "+c.Domain)
	}
	if len(c.Search) > 0 {
		out = append(out, "search "+strings.Join(c.Search, " "))
	}
	for _, n := range c.Nameservers {
		out = append(out, "nameserver "+n)
	}
	var opts []string
	if c.EDNS0 {
		opts = append(opts, "edns0")
	}
	if c.TCP {
		opts = append(opts, "tcp")
	}
	if c.TrustAD {
		opts = append(opts, "trust-ad")
	}
	if c.NDots > 0 {
		opts = append(opts, fmt.Sprintf("ndots:%d", c.NDots))
	}
	opts = append(opts, other...)
	if len(opts) > 0 {
		out = append(out, "options "+strings.Join(opts, " "))
	}
	return out
}

func (h *Host) SaveResolv(ctx context.Context, c *ResolvConf, preview bool) (*ConfResult, error) {
	if len(c.Nameservers) > 5 {
		return nil, invalid("höchstens 5 Nameserver")
	}
	seen := map[string]bool{}
	for _, n := range c.Nameservers {
		if _, err := netip.ParseAddr(n); err != nil {
			return nil, invalid("Nameserver %q ist keine gültige Adresse", n)
		}
		if seen[n] {
			return nil, invalid("Nameserver %s ist doppelt", n)
		}
		seen[n] = true
	}
	c.Domain = strings.ToLower(strings.TrimSpace(c.Domain))
	if c.Domain != "" && (len(c.Domain) > 253 || !reDomainSet.MatchString(c.Domain)) {
		return nil, invalid("Domain %q", c.Domain)
	}
	if len(c.Search) > 6 {
		return nil, invalid("höchstens 6 Suchdomänen")
	}
	total := 0
	for i, d := range c.Search {
		d = strings.ToLower(strings.TrimSpace(d))
		c.Search[i] = d
		if !reDomainSet.MatchString(d) {
			return nil, invalid("Suchdomäne %q", d)
		}
		total += len(d) + 1
	}
	if total > 1024 {
		return nil, invalid("Suchdomänen insgesamt höchstens 1024 Zeichen")
	}
	if c.Domain != "" && len(c.Search) > 0 {
		c.Domain = "" // search überschreibt domain; nicht beides schreiben
	}
	if len(c.Lookup) == 0 {
		return nil, invalid("Die Auflösungsreihenfolge darf nicht leer sein")
	}
	check := func(what string, vals []string, ok ...string) error {
		s := map[string]bool{}
		for _, v := range vals {
			if !contains(ok, v) || s[v] {
				return invalid("%s %q", what, v)
			}
			s[v] = true
		}
		return nil
	}
	if err := check("Auflösung", c.Lookup, "bind", "file"); err != nil {
		return nil, err
	}
	if err := check("Adressfamilie", c.Family, "inet4", "inet6"); err != nil {
		return nil, err
	}
	if c.NDots < 0 || c.NDots > 15 {
		return nil, invalid("ndots %d (0–15)", c.NDots)
	}
	src, _ := h.readFile(resolvPath)
	_, _, other := parseResolv(src)
	content := rewriteLines(src, isResolvManaged, c.lines(other), nil)
	res := &ConfResult{Warnings: []string{}}
	if len(c.Nameservers) == 0 {
		res.Warnings = append(res.Warnings, "Ohne Nameserver fragt der Host den lokalen Resolver (127.0.0.1) – läuft dort keiner, schlägt jede Namensauflösung fehl.")
	}
	for _, n := range c.Nameservers {
		if n == "127.0.0.1" || n == "::1" {
			ok := false
			for _, s := range []string{"unbound", "unwind"} {
				if _, r := h.rcActive(ctx, s); r {
					ok = true
				}
			}
			if !ok {
				res.Warnings = append(res.Warnings, "Der lokale Resolver (127.0.0.1) ist eingetragen, aber weder unbound noch unwind läuft.")
			}
			break
		}
	}
	if en, run := h.rcActive(ctx, "resolvd"); en || run {
		res.Warnings = append(res.Warnings, "resolvd ist aktiv und kann resolv.conf selbst umschreiben (z. B. bei DHCP-Änderungen). Dauerhaft eigene Nameserver gehen nur, wenn resolvd sie nicht überschreibt.")
	}
	if err := h.putConf(ctx, resolvPath, content, preview, res); err != nil {
		return nil, err
	}
	if !preview {
		res.Applied = "Gespeichert. Der Resolver liest die Datei bei der nächsten Abfrage neu."
	}
	return res, nil
}

// ---- hosts ----

type HostsEntry struct {
	ID      int      `json:"id"` // Zeilennummer in der Datei, 0 = neu
	IP      string   `json:"ip"`
	Names   []string `json:"names"`
	Comment string   `json:"comment"`
}

type HostsView struct {
	Entries  []HostsEntry `json:"entries"`
	Hostname string       `json:"hostname"`
}

func parseHostsLine(l string) (HostsEntry, bool) {
	if isCommentOrBlank(l) {
		return HostsEntry{}, false
	}
	body, comment, _ := strings.Cut(l, "#")
	f := strings.Fields(body)
	if len(f) < 2 {
		return HostsEntry{}, false
	}
	return HostsEntry{IP: f[0], Names: f[1:], Comment: strings.TrimSpace(comment)}, true
}

func (h *Host) Hosts(ctx context.Context) (*HostsView, error) {
	src, err := h.readFile(hostsPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	v := &HostsView{Entries: []HostsEntry{}, Hostname: h.Hostname(ctx)}
	for i, l := range splitFileLines(src) {
		if e, ok := parseHostsLine(l); ok {
			e.ID = i + 1
			v.Entries = append(v.Entries, e)
		}
	}
	return v, nil
}

func formatHosts(e HostsEntry) string {
	s := fmt.Sprintf("%s\t%s", e.IP, strings.Join(e.Names, " "))
	if e.Comment != "" {
		s += "\t# " + e.Comment
	}
	return s
}

func (h *Host) SaveHosts(ctx context.Context, entries []HostsEntry, preview bool) (*ConfResult, error) {
	if len(entries) > 2000 {
		return nil, invalid("zu viele Einträge")
	}
	src, _ := h.readFile(hostsPath)
	orig := splitFileLines(src)
	byID := map[int]HostsEntry{}
	for i := range entries {
		e := &entries[i]
		a, err := netip.ParseAddr(strings.TrimSpace(e.IP))
		if err != nil {
			return nil, invalid("IP-Adresse %q", e.IP)
		}
		e.IP = a.String()
		if len(e.Names) == 0 || len(e.Names) > 8 {
			return nil, invalid("%s: ein bis acht Namen", e.IP)
		}
		for j, n := range e.Names {
			n = strings.TrimSpace(n)
			e.Names[j] = n
			if len(n) > 253 || !reHostLabel.MatchString(n) {
				return nil, invalid("Name %q (Buchstaben, Ziffern, Bindestrich, Punkt)", n)
			}
		}
		if strings.ContainsAny(e.Comment, "\n\r") || len(e.Comment) > 200 {
			return nil, invalid("Kommentar")
		}
		if e.ID > 0 {
			if e.ID > len(orig) {
				return nil, invalid("Eintrag %d gibt es nicht mehr – bitte neu laden", e.ID)
			}
			if _, ok := parseHostsLine(orig[e.ID-1]); !ok {
				return nil, invalid("Zeile %d hat sich geändert – bitte neu laden", e.ID)
			}
			byID[e.ID] = *e
		}
	}
	// Die Loopback-Zuordnung muss bleiben, wenn sie da war.
	hasLocal := func(ip string, list func(func(HostsEntry))) bool {
		ok := false
		list(func(e HostsEntry) {
			if e.IP == ip && contains(e.Names, "localhost") {
				ok = true
			}
		})
		return ok
	}
	var before []HostsEntry
	for _, l := range orig {
		if e, ok := parseHostsLine(l); ok {
			if a, err := netip.ParseAddr(e.IP); err == nil {
				e.IP = a.String()
			}
			before = append(before, e)
		}
	}
	for _, ip := range []string{"127.0.0.1", "::1"} {
		if hasLocal(ip, func(f func(HostsEntry)) {
			for _, e := range before {
				f(e)
			}
		}) && !hasLocal(ip, func(f func(HostsEntry)) {
			for _, e := range entries {
				f(e)
			}
		}) {
			return nil, invalid("Der Eintrag %s → localhost darf nicht fehlen", ip)
		}
	}
	var out []string
	for i, l := range orig {
		e, isEntry := parseHostsLine(l)
		if !isEntry {
			out = append(out, l)
			continue
		}
		if ne, ok := byID[i+1]; ok {
			out = append(out, formatHosts(ne))
		} // sonst gelöscht
		_ = e
	}
	for _, e := range entries {
		if e.ID == 0 {
			out = append(out, formatHosts(e))
		}
	}
	content := strings.Join(out, "\n") + "\n"
	res := &ConfResult{Warnings: []string{}}
	own := h.Hostname(ctx)
	if own != "" {
		found := false
		short, _, _ := strings.Cut(own, ".")
		for _, e := range entries {
			found = found || contains(e.Names, own) || contains(e.Names, short)
		}
		if !found {
			res.Warnings = append(res.Warnings, fmt.Sprintf("Der eigene Hostname %s steht in keinem Eintrag – manche Dienste (sendmail, smtpd) erwarten das.", own))
		}
	}
	names := map[string]string{}
	var dup []string
	for _, e := range entries {
		for _, n := range e.Names {
			if ip, ok := names[strings.ToLower(n)]; ok && ip != e.IP && !(isLoop(ip) && isLoop(e.IP)) {
				dup = append(dup, n)
			}
			names[strings.ToLower(n)] = e.IP
		}
	}
	sort.Strings(dup)
	for _, n := range dup {
		res.Warnings = append(res.Warnings, fmt.Sprintf("Der Name %s steht bei mehreren Adressen; die erste gewinnt.", n))
	}
	if err := h.putConf(ctx, hostsPath, content, preview, res); err != nil {
		return nil, err
	}
	if !preview {
		res.Applied = "Gespeichert. Wirkt sofort."
	}
	return res, nil
}

func isLoop(ip string) bool {
	a, err := netip.ParseAddr(ip)
	return err == nil && a.IsLoopback()
}
