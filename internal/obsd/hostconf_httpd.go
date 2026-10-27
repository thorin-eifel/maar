package obsd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const httpdPath = "/etc/httpd.conf"

// Webseiten (server-Blöcke) von httpd.conf(5) als Formular. Bekannte Anweisungen
// werden aus den Auswahlfeldern neu geschrieben; location-Blöcke und alles
// Unbekannte bleibt wörtlich erhalten. Identität eines Blocks = seine Nummer in der Datei.

type HTTPListen struct {
	Addr string `json:"addr"` // *, IP, Name
	Port string `json:"port"`
	TLS  bool   `json:"tls"`
}

type HTTPServer struct {
	ID        int          `json:"id"` // 1-basiert in der Datei, 0 = neu
	Name      string       `json:"name"`
	Aliases   []string     `json:"aliases"`
	Listen    []HTTPListen `json:"listen"`
	Root      string       `json:"root"`
	TLSCert   string       `json:"tlsCertificate"`
	TLSKey    string       `json:"tlsKey"`
	AutoIndex string       `json:"autoIndex"` // "", yes, no
	IndexFile string       `json:"indexFile"`
	LogStyle  string       `json:"logStyle"` // "", common, combined, forwarded, connection
	NoLog     bool         `json:"noLog"`
	HSTS      int          `json:"hstsMaxAge"` // Sekunden, 0 = aus
	HSTSSub   bool         `json:"hstsSubdomains"`
	RedirCode int          `json:"redirectCode"` // 0 = keine Weiterleitung
	RedirURL  string       `json:"redirectUrl"`
	Notes     []string     `json:"notes"` // unverändert bleibende Anweisungen, nur Anzeige
	Locations int          `json:"locations"`
	Leading   []string     `json:"-"`
	kept      []string     // erhaltene Anweisungen in Originalform
}

type HTTPConf struct {
	Prefork int          `json:"prefork"` // 0 = Standard
	Servers []HTTPServer `json:"servers"`
}

type HTTPView struct {
	Conf      HTTPConf `json:"conf"`
	Running   bool     `json:"running"`
	Enabled   bool     `json:"enabled"`
	Addresses []string `json:"addresses"`
	Roots     []string `json:"roots"`
	Certs     []string `json:"certificates"`
	Keys      []string `json:"keys"`
	Chroot    string   `json:"chroot"`
	Other     []string `json:"other"` // übrige Anweisungen außerhalb der Server (types, Makros …)
}

var (
	reHTTPName  = regexp.MustCompile(`^[A-Za-z0-9*._-]{1,253}$`)
	reHTTPIndex = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
	reHTTPRoot  = regexp.MustCompile(`^/[A-Za-z0-9._/-]{0,200}$`)
	reHTTPURL   = regexp.MustCompile(`^[A-Za-z0-9:/._~%$?&=#+@-]{1,300}$`)
	reHTTPPort  = regexp.MustCompile(`^[0-9]{1,5}$`)
)

// braceDelta zählt { und } einer Zeile ohne Zeichenketten und Kommentare.
func braceDelta(l string) int {
	d, q := 0, false
	for i := 0; i < len(l); i++ {
		switch c := l[i]; {
		case c == '\\' && q:
			i++
		case c == '"':
			q = !q
		case !q && c == '#':
			return d
		case !q && c == '{':
			d++
		case !q && c == '}':
			d--
		}
	}
	return d
}

type httpFile struct {
	servers []*httpBlock
	other   []string // Zeilen außerhalb der server-Blöcke (ohne Leerzeilen)
	prefork int
	order   []any // Reihenfolge: *httpBlock oder string
}

type httpBlock struct {
	header  string
	leading []string
	stmts   [][]string // Anweisungen auf Tiefe 1 (jeweils ihre Zeilen)
	oneLine bool
}

var reServerHead = regexp.MustCompile(`^\s*server\s+(match\s+)?("[^"]*"|\S+)\s*\{\s*$`)

func parseHTTPD(src string) *httpFile {
	f := &httpFile{}
	lines := splitFileLines(src)
	var comments []string
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		t := strings.TrimSpace(l)
		if reServerHead.MatchString(l) {
			b := &httpBlock{header: t, leading: comments}
			comments = nil
			depth := 1
			var cur []string
			i++
			for ; i < len(lines) && depth > 0; i++ {
				ln := lines[i]
				d := braceDelta(ln)
				if depth == 1 && d < 0 { // schließende Klammer des Blocks
					depth += d
					break
				}
				if strings.TrimSpace(ln) == "" && len(cur) == 0 {
					continue
				}
				cur = append(cur, ln)
				depth += d
				if depth == 1 {
					b.stmts = append(b.stmts, cur)
					cur = nil
				}
			}
			f.servers = append(f.servers, b)
			f.order = append(f.order, b)
			continue
		}
		if strings.HasPrefix(t, "server") && strings.Contains(t, "{") {
			// Block in einer Zeile oder unbekannte Schreibweise: unverändert behalten
			b := &httpBlock{header: t, oneLine: true, leading: comments}
			comments = nil
			if braceDelta(l) > 0 {
				depth := braceDelta(l)
				b.stmts = append(b.stmts, []string{l})
				for i++; i < len(lines) && depth > 0; i++ {
					b.stmts[0] = append(b.stmts[0], lines[i])
					depth += braceDelta(lines[i])
				}
				i--
			} else {
				b.stmts = append(b.stmts, []string{l})
			}
			f.servers = append(f.servers, b)
			f.order = append(f.order, b)
			continue
		}
		switch {
		case t == "":
			if len(comments) > 0 { // freistehender Kommentar: gehört zur Datei
				f.order = append(f.order, append([]string{}, comments...))
				f.other = append(f.other, comments...)
				comments = nil
			}
		case strings.HasPrefix(t, "#"):
			comments = append(comments, l)
		default:
			f.other = append(f.other, comments...)
			f.other = append(f.other, l)
			f.order = append(f.order, append(comments, l)) // wird unten zu string-Gruppen
			comments = nil
			if strings.HasPrefix(t, "prefork") {
				f.prefork = atoiSafe(strings.TrimSpace(strings.TrimPrefix(t, "prefork")))
			}
			// mehrzeilige Blöcke (types { … }) vollständig übernehmen
			if d := braceDelta(l); d > 0 {
				for i++; i < len(lines) && d > 0; i++ {
					f.other = append(f.other, lines[i])
					f.order[len(f.order)-1] = append(f.order[len(f.order)-1].([]string), lines[i])
					d += braceDelta(lines[i])
				}
				i--
			}
		}
	}
	return f
}

func unq(s string) string { return strings.Trim(s, `"`) }

// parseServer liest die bekannten Anweisungen eines Blocks.
func (b *httpBlock) server(id int) HTTPServer {
	s := HTTPServer{ID: id, Aliases: []string{}, Listen: []HTTPListen{}, Notes: []string{}, Leading: b.leading}
	f := strings.Fields(strings.Trim(strings.TrimSuffix(b.header, "{"), " "))
	if len(f) >= 2 {
		s.Name = unq(f[len(f)-1])
	}
	if b.oneLine || (len(f) >= 3 && f[1] == "match") {
		s.kept = nil
		s.Notes = append(s.Notes, "Dieser Block ist in Einzeilen- oder match-Schreibweise und bleibt unverändert.")
		for _, st := range b.stmts {
			s.kept = append(s.kept, st...)
		}
		return s
	}
	for _, st := range b.stmts {
		first := strings.Fields(st[0])
		if len(first) == 0 {
			continue
		}
		kw := first[0]
		text := strings.Join(st, "\n")
		keep := func() { s.kept = append(s.kept, st...) }
		switch {
		case kw == "listen" && len(first) >= 5 && first[1] == "on" && len(st) == 1:
			l := HTTPListen{Addr: unq(first[2])}
			rest := first[3:]
			if rest[0] == "tls" {
				l.TLS, rest = true, rest[1:]
			}
			if len(rest) == 2 && rest[0] == "port" {
				l.Port = rest[1]
				s.Listen = append(s.Listen, l)
				continue
			}
			keep()
		case kw == "alias" && len(first) == 2 && len(st) == 1:
			s.Aliases = append(s.Aliases, unq(first[1]))
		case kw == "root" && len(first) == 2 && len(st) == 1:
			s.Root = unq(first[1])
		case kw == "tls" && strings.HasSuffix(strings.TrimSpace(st[0]), "{"):
			inner, extra := subBlock(st)
			s.TLSCert, s.TLSKey = inner["certificate"], inner["key"]
			for _, e := range extra {
				s.Notes = append(s.Notes, "tls: "+strings.TrimSpace(e))
			}
			if len(extra) > 0 {
				s.kept = append(s.kept, "\ttls-extra:"+strings.Join(extra, "\n"))
			}
		case kw == "directory" && strings.HasSuffix(strings.TrimSpace(st[0]), "{"):
			inner, extra := subBlock(st)
			if _, ok := inner["auto"]; ok {
				s.AutoIndex = "yes"
			}
			if _, ok := inner["no auto"]; ok {
				s.AutoIndex = "no"
			}
			s.IndexFile = inner["index"]
			if len(extra) > 0 {
				s.kept = append(s.kept, "\tdir-extra:"+strings.Join(extra, "\n"))
			}
		case kw == "log" && strings.HasSuffix(strings.TrimSpace(st[0]), "{"):
			inner, extra := subBlock(st)
			s.LogStyle = inner["style"]
			if len(extra) > 0 {
				s.kept = append(s.kept, "\tlog-extra:"+strings.Join(extra, "\n"))
			}
		case kw == "no" && len(first) == 2 && first[1] == "log":
			s.NoLog = true
		case kw == "hsts":
			s.HSTS, s.HSTSSub = parseHSTS(text)
			if s.HSTS == 0 {
				s.HSTS = 31536000
			}
		case kw == "block" && len(first) >= 3 && first[1] == "return" && len(st) == 1:
			code := atoiSafe(first[2])
			url := ""
			if len(first) >= 4 {
				url = unq(strings.Join(first[3:], " "))
			}
			if code >= 300 && code < 400 && url != "" {
				s.RedirCode, s.RedirURL = code, url
				continue
			}
			keep()
		default:
			keep()
			if kw == "location" {
				s.Locations++
			} else {
				s.Notes = append(s.Notes, strings.TrimSpace(st[0]))
			}
		}
	}
	return s
}

func parseHSTS(t string) (maxAge int, sub bool) {
	f := strings.Fields(strings.NewReplacer("{", " ", "}", " ").Replace(t))
	for i, x := range f {
		if x == "max-age" && i+1 < len(f) {
			maxAge = atoiSafe(f[i+1])
		}
		if x == "subdomains" {
			sub = true
		}
	}
	return
}

// subBlock liest "name { zeile … }" und liefert bekannte Schlüssel sowie übrige Innenzeilen.
func subBlock(st []string) (known map[string]string, extra []string) {
	known = map[string]string{}
	for _, l := range st[1:] {
		t := strings.TrimSpace(l)
		if t == "}" || t == "" {
			continue
		}
		f := strings.Fields(t)
		switch {
		case (f[0] == "certificate" || f[0] == "key" || f[0] == "style") && len(f) == 2:
			known[f[0]] = unq(f[1])
		case f[0] == "index" && len(f) == 2:
			known["index"] = unq(f[1])
		case len(f) == 2 && f[0] == "auto" && f[1] == "index":
			known["auto"] = "index"
		case len(f) == 3 && f[0] == "no" && f[1] == "auto" && f[2] == "index":
			known["no auto"] = "index"
		default:
			extra = append(extra, l)
		}
	}
	return
}

func (h *Host) HTTPD(ctx context.Context) (*HTTPView, error) {
	src, err := h.readFile(httpdPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	f := parseHTTPD(src)
	v := &HTTPView{Conf: HTTPConf{Prefork: f.prefork, Servers: []HTTPServer{}}, Addresses: []string{"*"}, Roots: []string{}, Certs: []string{}, Keys: []string{}, Other: []string{}, Chroot: "/var/www"}
	for i, b := range f.servers {
		v.Conf.Servers = append(v.Conf.Servers, b.server(i+1))
	}
	for _, l := range f.other {
		if t := strings.TrimSpace(l); t != "" && !strings.HasPrefix(t, "#") && !strings.HasPrefix(t, "prefork") {
			v.Other = append(v.Other, t)
		}
	}
	for _, l := range f.other {
		if t := strings.TrimSpace(l); strings.HasPrefix(t, "chroot ") {
			v.Chroot = unq(strings.TrimSpace(strings.TrimPrefix(t, "chroot ")))
		}
	}
	v.Enabled, v.Running = h.rcActive(ctx, "httpd")
	v.Addresses = append(v.Addresses, h.localAddrs(ctx)...)
	// Wurzelverzeichnisse: Ordner unter <chroot>/htdocs
	base := filepath.Join(v.Chroot, "htdocs")
	if ents, err := os.ReadDir(h.P(base)); err == nil {
		for _, e := range ents {
			if e.IsDir() {
				v.Roots = append(v.Roots, "/htdocs/"+e.Name())
			}
		}
	}
	v.Roots = append(v.Roots, "/htdocs")
	sort.Strings(v.Roots)
	list := func(dir string, ok func(string) bool) []string {
		var out []string
		if ents, err := os.ReadDir(h.P(dir)); err == nil {
			for _, e := range ents {
				if !e.IsDir() && ok(e.Name()) {
					out = append(out, filepath.Join(dir, e.Name()))
				}
			}
		}
		return out
	}
	v.Certs = list("/etc/ssl", func(n string) bool { return strings.HasSuffix(n, ".crt") || strings.HasSuffix(n, ".pem") })
	v.Keys = list("/etc/ssl/private", func(n string) bool { return strings.HasSuffix(n, ".key") || strings.HasSuffix(n, ".pem") })
	return v, nil
}

func (s *HTTPServer) lines() []string {
	var out []string
	q := func(x string) string { return `"` + x + `"` }
	out = append(out, "server "+q(s.Name)+" {")
	for _, a := range s.Aliases {
		out = append(out, "\talias "+q(a))
	}
	for _, l := range s.Listen {
		tls := ""
		if l.TLS {
			tls = " tls"
		}
		out = append(out, fmt.Sprintf("\tlisten on %s%s port %s", l.Addr, tls, l.Port))
	}
	if s.Root != "" {
		out = append(out, "\troot "+q(s.Root))
	}
	var tlsExtra, dirExtra, logExtra string
	var rest []string
	for _, k := range s.kept {
		switch {
		case strings.HasPrefix(k, "\ttls-extra:"):
			tlsExtra = strings.TrimPrefix(k, "\ttls-extra:")
		case strings.HasPrefix(k, "\tdir-extra:"):
			dirExtra = strings.TrimPrefix(k, "\tdir-extra:")
		case strings.HasPrefix(k, "\tlog-extra:"):
			logExtra = strings.TrimPrefix(k, "\tlog-extra:")
		default:
			rest = append(rest, k)
		}
	}
	if s.TLSCert != "" || s.TLSKey != "" || tlsExtra != "" {
		out = append(out, "\ttls {")
		if s.TLSCert != "" {
			out = append(out, "\t\tcertificate "+q(s.TLSCert))
		}
		if s.TLSKey != "" {
			out = append(out, "\t\tkey "+q(s.TLSKey))
		}
		if tlsExtra != "" {
			out = append(out, tlsExtra)
		}
		out = append(out, "\t}")
	}
	if s.AutoIndex != "" || s.IndexFile != "" || dirExtra != "" {
		out = append(out, "\tdirectory {")
		switch s.AutoIndex {
		case "yes":
			out = append(out, "\t\tauto index")
		case "no":
			out = append(out, "\t\tno auto index")
		}
		if s.IndexFile != "" {
			out = append(out, "\t\tindex "+q(s.IndexFile))
		}
		if dirExtra != "" {
			out = append(out, dirExtra)
		}
		out = append(out, "\t}")
	}
	if s.NoLog {
		out = append(out, "\tno log")
	} else if s.LogStyle != "" || logExtra != "" {
		out = append(out, "\tlog {")
		if s.LogStyle != "" {
			out = append(out, "\t\tstyle "+s.LogStyle)
		}
		if logExtra != "" {
			out = append(out, logExtra)
		}
		out = append(out, "\t}")
	}
	if s.HSTS > 0 {
		h := fmt.Sprintf("\thsts max-age %d", s.HSTS)
		if s.HSTSSub {
			h += " subdomains"
		}
		out = append(out, h)
	}
	if s.RedirCode != 0 {
		out = append(out, fmt.Sprintf("\tblock return %d %s", s.RedirCode, q(s.RedirURL)))
	}
	out = append(out, rest...)
	out = append(out, "}")
	return out
}

func (h *Host) validateHTTPD(ctx context.Context, c *HTTPConf, cur *HTTPView) error {
	if c.Prefork < 0 || c.Prefork > 32 {
		return invalid("prefork %d (1–32)", c.Prefork)
	}
	if len(c.Servers) > 50 {
		return invalid("höchstens 50 Webseiten")
	}
	addrs := map[string]bool{"*": true, "0.0.0.0": true, "::": true}
	for _, a := range cur.Addresses {
		addrs[a] = true
	}
	roots := map[string]bool{}
	for _, r := range cur.Roots {
		roots[r] = true
	}
	files := func(p string, list []string) bool {
		for _, x := range list {
			if x == p {
				return true
			}
		}
		return false
	}
	for i := range c.Servers {
		s := &c.Servers[i]
		n := i + 1
		if !reHTTPName.MatchString(s.Name) {
			return invalid("Webseite %d: Name %q (Buchstaben, Ziffern, . - _ *)", n, s.Name)
		}
		for _, a := range s.Aliases {
			if !reHTTPName.MatchString(a) {
				return invalid("%s: Alias %q", s.Name, a)
			}
		}
		if len(s.Listen) == 0 && s.ID == 0 {
			return invalid("%s: mindestens eine Adresse zum Lauschen", s.Name)
		}
		for _, l := range s.Listen {
			if !addrs[l.Addr] {
				return invalid("%s: %q ist keine Adresse dieses Hosts", s.Name, l.Addr)
			}
			if !reHTTPPort.MatchString(l.Port) || atoiSafe(l.Port) < 1 || atoiSafe(l.Port) > 65535 {
				return invalid("%s: Port %q", s.Name, l.Port)
			}
		}
		if s.Root != "" {
			if !reHTTPRoot.MatchString(s.Root) || strings.Contains(s.Root, "..") {
				return invalid("%s: Wurzelverzeichnis %q", s.Name, s.Root)
			}
			if !roots[s.Root] {
				if st, err := os.Stat(h.P(filepath.Join(cur.Chroot, s.Root))); err != nil || !st.IsDir() {
					return invalid("%s: %s gibt es unter %s nicht", s.Name, s.Root, cur.Chroot)
				}
			}
		}
		if s.TLSCert != "" && !files(s.TLSCert, cur.Certs) {
			return invalid("%s: Zertifikat %q gibt es nicht in /etc/ssl", s.Name, s.TLSCert)
		}
		if s.TLSKey != "" && !files(s.TLSKey, cur.Keys) {
			return invalid("%s: Schlüssel %q gibt es nicht in /etc/ssl/private", s.Name, s.TLSKey)
		}
		hasTLS := false
		for _, l := range s.Listen {
			hasTLS = hasTLS || l.TLS
		}
		if hasTLS && (s.TLSCert == "") != (s.TLSKey == "") {
			return invalid("%s: Zertifikat und Schlüssel gehören zusammen", s.Name)
		}
		if s.AutoIndex != "" && s.AutoIndex != "yes" && s.AutoIndex != "no" {
			return invalid("%s: Verzeichnisliste", s.Name)
		}
		if s.IndexFile != "" && !reHTTPIndex.MatchString(s.IndexFile) {
			return invalid("%s: Indexdatei %q", s.Name, s.IndexFile)
		}
		switch s.LogStyle {
		case "", "common", "combined", "forwarded", "connection":
		default:
			return invalid("%s: Protokollformat %q", s.Name, s.LogStyle)
		}
		if s.HSTS < 0 || s.HSTS > 63072000 {
			return invalid("%s: HSTS max-age", s.Name)
		}
		if s.RedirCode != 0 {
			if s.RedirCode < 300 || s.RedirCode > 399 || !reHTTPURL.MatchString(s.RedirURL) {
				return invalid("%s: Weiterleitung %d %q", s.Name, s.RedirCode, s.RedirURL)
			}
		}
	}
	return nil
}

// SaveHTTPD schreibt httpd.conf neu: bekannte Anweisungen aus dem Formular, Rest wörtlich.
func (h *Host) SaveHTTPD(ctx context.Context, c *HTTPConf, activate, preview bool) (*ConfResult, error) {
	cur, err := h.HTTPD(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.validateHTTPD(ctx, c, cur); err != nil {
		return nil, err
	}
	src, _ := h.readFile(httpdPath)
	f := parseHTTPD(src)
	byID := map[int]*httpBlock{}
	for i, b := range f.servers {
		byID[i+1] = b
	}
	var out []string
	seenPre := false
	emitPre := func() {
		if !seenPre && c.Prefork > 0 {
			out = append(out, fmt.Sprintf("prefork %d", c.Prefork))
			seenPre = true
		}
	}
	for _, o := range f.order {
		switch x := o.(type) {
		case []string:
			isPre := len(x) > 0 && strings.HasPrefix(strings.TrimSpace(x[len(x)-1]), "prefork")
			if isPre {
				emitPre()
				continue
			}
			out = append(out, x...)
			out = append(out, "")
		case *httpBlock:
			// wird über die Formularliste ausgegeben (Reihenfolge der Liste)
			_ = x
		}
	}
	emitPre()
	if len(out) > 0 && out[len(out)-1] != "" {
		out = append(out, "")
	}
	for i := range c.Servers {
		s := &c.Servers[i]
		if s.ID > 0 {
			b := byID[s.ID]
			if b == nil {
				return nil, invalid("Webseite %d gibt es nicht mehr – bitte neu laden", s.ID)
			}
			old := b.server(s.ID)
			out = append(out, b.leading...)
			if old.kept != nil && (b.oneLine || strings.Contains(b.header, "match")) {
				out = append(out, b.header)
				for _, k := range old.kept {
					out = append(out, k)
				}
				out = append(out, "")
				continue
			}
			s.kept = old.kept
		}
		out = append(out, s.lines()...)
		out = append(out, "")
	}
	content := strings.TrimRight(strings.Join(out, "\n"), "\n") + "\n"
	res := &ConfResult{Warnings: []string{}}
	pf, _ := h.readFile(pfConfPath)
	ports := map[string]bool{}
	for _, s := range c.Servers {
		for _, l := range s.Listen {
			ports[l.Port] = true
		}
	}
	var pl []string
	for p := range ports {
		pl = append(pl, p)
	}
	sort.Strings(pl)
	for _, p := range pl {
		name := map[string]string{"80": "http", "443": "https"}[p]
		if !strings.Contains(pf, "port "+p) && !strings.Contains(pf, name) && !strings.Contains(pf, p) {
			res.Warnings = append(res.Warnings, "Port "+p+" kommt in /etc/pf.conf nicht vor – der Paketfilter blockiert ihn vermutlich.")
		}
	}
	// gleicher Name und gleicher Port zweimal
	seen := map[string]bool{}
	for _, s := range c.Servers {
		for _, l := range s.Listen {
			k := s.Name + "|" + l.Addr + "|" + l.Port
			if seen[k] {
				res.Warnings = append(res.Warnings, "Die Webseite „"+s.Name+"“ lauscht doppelt auf "+l.Addr+":"+l.Port+".")
			}
			seen[k] = true
		}
	}
	if err := h.putConf(ctx, httpdPath, content, preview, res); err != nil {
		return nil, err
	}
	if preview {
		return res, nil
	}
	enabled, running := h.rcActive(ctx, "httpd")
	switch {
	case running:
		err = h.run(ctx, "rcctl", "reload", "httpd")
		res.Applied = "httpd neu geladen."
	case activate && !enabled:
		if err = h.run(ctx, "rcctl", "enable", "httpd"); err == nil {
			err = h.run(ctx, "rcctl", "start", "httpd")
		}
		res.Applied = "httpd aktiviert und gestartet."
	case activate:
		err = h.run(ctx, "rcctl", "start", "httpd")
		res.Applied = "httpd gestartet."
	default:
		res.Applied = "Gespeichert; httpd läuft nicht und wurde nicht gestartet."
	}
	if err != nil {
		return nil, fmt.Errorf("Datei gespeichert, aber httpd ließ sich nicht (neu) laden: %w", err)
	}
	return res, nil
}

var _ = strconv.Itoa
