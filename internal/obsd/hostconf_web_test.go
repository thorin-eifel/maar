package obsd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestHTTPDForm(t *testing.T) {
	h := newDemoHost(t)
	ctx := context.Background()
	v, err := h.HTTPD(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Conf.Servers) != 2 || v.Conf.Servers[0].Locations != 2 || v.Conf.Servers[1].TLSCert != "/etc/ssl/nordlicht.ctw.lan.fullchain.pem" || v.Conf.Servers[1].Root != "/htdocs/nordlicht" || !v.Conf.Servers[1].Listen[0].TLS {
		t.Fatalf("Parser: %+v", v.Conf.Servers)
	}
	if len(v.Roots) < 3 || len(v.Certs) < 2 || len(v.Keys) < 2 {
		t.Errorf("Auswahl: roots=%v certs=%v keys=%v", v.Roots, v.Certs, v.Keys)
	}
	// Unverändert zurückschreiben: location-Blöcke bleiben wörtlich erhalten.
	if _, err := h.SaveHTTPD(ctx, &v.Conf, false, false); err != nil {
		t.Fatal(err)
	}
	got, _ := h.readFile(httpdPath)
	for _, w := range []string{`location "/.well-known/acme-challenge/*" {`, "request strip 2", `location * {`, `block return 302 "https://$HTTP_HOST$REQUEST_URI"`, `certificate "/etc/ssl/nordlicht.ctw.lan.fullchain.pem"`, "# httpd.conf"} {
		if !strings.Contains(got, w) {
			t.Errorf("%q fehlt:\n%s", w, got)
		}
	}
	// Ändern: Alias, Verzeichnisliste, Protokollformat, HSTS, neue Weiterleitungs-Seite.
	v, _ = h.HTTPD(ctx)
	s := &v.Conf.Servers[1]
	s.Aliases = []string{"www.ctw.lan"}
	s.AutoIndex, s.IndexFile, s.LogStyle, s.HSTS, s.HSTSSub = "yes", "start.html", "combined", 31536000, true
	v.Conf.Servers = append(v.Conf.Servers, HTTPServer{Name: "alt.ctw.lan", Listen: []HTTPListen{{Addr: "*", Port: "80"}}, RedirCode: 301, RedirURL: "https://nordlicht.ctw.lan$REQUEST_URI"})
	v.Conf.Prefork = 2
	res, err := h.SaveHTTPD(ctx, &v.Conf, false, false)
	if err != nil {
		t.Fatal(err)
	}
	got, _ = h.readFile(httpdPath)
	for _, w := range []string{"prefork 2", `alias "www.ctw.lan"`, "auto index", `index "start.html"`, "style combined", "hsts max-age 31536000 subdomains", `server "alt.ctw.lan" {`, `block return 301 "https://nordlicht.ctw.lan$REQUEST_URI"`, `location "/.well-known/acme-challenge/*"`} {
		if !strings.Contains(got, w) {
			t.Errorf("%q fehlt:\n%s", w, got)
		}
	}
	if !strings.Contains(res.Applied, "geladen") {
		t.Errorf("Anwenden: %+v", res)
	}
	// Zurücklesen ergibt dieselben Werte.
	back, _ := h.HTTPD(ctx)
	b := back.Conf.Servers[1]
	if back.Conf.Prefork != 2 || b.IndexFile != "start.html" || b.LogStyle != "combined" || b.HSTS != 31536000 || !b.HSTSSub || b.AutoIndex != "yes" || len(back.Conf.Servers) != 3 || back.Conf.Servers[2].RedirCode != 301 {
		t.Errorf("Rücklesen: %+v", back.Conf)
	}
	// Löschen der ersten Webseite.
	back.Conf.Servers = back.Conf.Servers[1:]
	if _, err := h.SaveHTTPD(ctx, &back.Conf, false, false); err != nil {
		t.Fatal(err)
	}
	if got, _ = h.readFile(httpdPath); strings.Contains(got, "acme-challenge") || !strings.Contains(got, "alt.ctw.lan") {
		t.Errorf("nach Löschen:\n%s", got)
	}
	// Ablehnungen
	before, _ := h.readFile(httpdPath)
	base := func() HTTPServer {
		return HTTPServer{Name: "x.ctw.lan", Listen: []HTTPListen{{Addr: "*", Port: "80"}}}
	}
	for i, mod := range []func(s *HTTPServer){
		func(s *HTTPServer) { s.Name = `a" { }` },
		func(s *HTTPServer) { s.Aliases = []string{"a b"} },
		func(s *HTTPServer) { s.Listen = nil },
		func(s *HTTPServer) { s.Listen = []HTTPListen{{Addr: "1.2.3.4", Port: "80"}} },
		func(s *HTTPServer) { s.Listen = []HTTPListen{{Addr: "*", Port: "99999"}} },
		func(s *HTTPServer) { s.Listen = []HTTPListen{{Addr: "*", Port: "80 tls"}} },
		func(s *HTTPServer) { s.Root = "/htdocs/../../etc" },
		func(s *HTTPServer) { s.Root = "/htdocs/gibtsnicht" },
		func(s *HTTPServer) { s.TLSCert = "/etc/passwd"; s.TLSKey = "/etc/ssl/private/server.key" },
		func(s *HTTPServer) {
			s.Listen = []HTTPListen{{Addr: "*", Port: "443", TLS: true}}
			s.TLSCert = "/etc/ssl/server.crt"
		},
		func(s *HTTPServer) { s.IndexFile = "a b" },
		func(s *HTTPServer) { s.LogStyle = "bunt" },
		func(s *HTTPServer) { s.HSTS = -1 },
		func(s *HTTPServer) { s.RedirCode, s.RedirURL = 200, "https://x.de" },
		func(s *HTTPServer) { s.RedirCode, s.RedirURL = 301, "https://x.de\"\n}" },
	} {
		s := base()
		mod(&s)
		if _, err := h.SaveHTTPD(ctx, &HTTPConf{Servers: []HTTPServer{s}}, false, false); err == nil {
			t.Errorf("Fall %d akzeptiert: %+v", i, s)
		}
	}
	if after, _ := h.readFile(httpdPath); after != before {
		t.Error("abgelehnte Eingaben haben httpd.conf verändert")
	}
}

func TestSMTPDForm(t *testing.T) {
	h := newDemoHost(t)
	ctx := context.Background()
	v, err := h.SMTPD(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c := v.Conf
	if len(c.Tables) != 1 || c.Tables[0].Name != "aliases" || c.Tables[0].Type != "file" || c.Tables[0].Path != "/etc/mail/aliases" || len(c.Listens) != 2 || c.Listens[0].On != "socket" || len(c.Actions) != 2 || c.Actions[0].Alias != "aliases" || c.Actions[1].Method != "relay" || len(c.Matches) != 2 || c.Matches[1].For != "any" {
		t.Fatalf("Parser: %+v", c)
	}
	if !contains(v.Interfaces, "egress") || !contains(v.Interfaces, "lo0") || !contains(v.TableFiles, "/etc/mail/aliases") {
		t.Errorf("Auswahl: %v %v", v.Interfaces, v.TableFiles)
	}
	// Rundlauf: gleiche Datei (bis auf Formatierung) zurück
	if _, err := h.SaveSMTPD(ctx, &c, false, false); err != nil {
		t.Fatal(err)
	}
	got, _ := h.readFile(smtpdPath)
	for _, w := range []string{"table aliases file:/etc/mail/aliases", "listen on socket", "listen on lo0", `action "local_mail" mbox alias <aliases>`, `action "outbound" relay`, `match from local for local action "local_mail"`, `match from local for any action "outbound"`} {
		if !strings.Contains(got, w) {
			t.Errorf("%q fehlt:\n%s", w, got)
		}
	}
	// Nachrichtenzustellung ändern: Submission mit TLS und Anmeldung, Relay über Smarthost, Domain-Regel
	v, _ = h.SMTPD(ctx)
	c = v.Conf
	c.PKIs = []SMTPPKI{{Name: "mx", Cert: "/etc/ssl/nordlicht.ctw.lan.fullchain.pem", Key: "/etc/ssl/private/nordlicht.ctw.lan.key"}}
	c.Tables = append(c.Tables, SMTPTable{Name: "creds", Type: "file", Path: "/etc/mail/aliases"})
	c.Listens = append(c.Listens, SMTPListen{On: "egress", Port: 587, TLS: "tls-require", PKI: "mx", Auth: "auth", Comment: "Einreichung"})
	c.Actions[1] = SMTPAction{Name: "outbound", Method: "relay", Host: "smtp+tls://relay.example.org:587", TLS: "tls", Auth: "creds", Helo: "nordlicht.ctw.lan"}
	c.Actions = append(c.Actions, SMTPAction{Name: "virt", Method: "maildir", Path: "~/Maildir", Junk: true, Virtual: "aliases"})
	c.Matches = append([]SMTPMatch{{From: "any", For: "domain", ForArg: "ctw.lan", Action: "virt", Comment: "eigene Domain"}}, c.Matches...)
	c.Matches = append(c.Matches, SMTPMatch{From: "auth", For: "any", Action: "outbound"})
	res, err := h.SaveSMTPD(ctx, &c, false, false)
	if err != nil {
		t.Fatal(err)
	}
	got, _ = h.readFile(smtpdPath)
	for _, w := range []string{"pki mx cert \"/etc/ssl/nordlicht.ctw.lan.fullchain.pem\"", "# Einreichung\nlisten on", "listen on egress port 587 tls-require pki mx auth", `action "outbound" relay host "smtp+tls://relay.example.org:587" tls auth <creds> helo "nordlicht.ctw.lan"`, `action "virt" maildir "~/Maildir" junk virtual <aliases>`, "# eigene Domain\nmatch from any for domain ctw.lan action \"virt\"", `match from auth for any action "outbound"`} {
		if w == "# Einreichung\nlisten on" {
			w = "# Einreichung\nlisten on egress"
		}
		if !strings.Contains(got, w) {
			t.Errorf("%q fehlt:\n%s", w, got)
		}
	}
	if !strings.Contains(res.Applied, "neu gestartet") || len(res.Warnings) == 0 {
		t.Errorf("Anwenden: %+v", res)
	}
	back, _ := h.SMTPD(ctx)
	if len(back.Conf.Matches) != 4 || back.Conf.Matches[0].ForArg != "ctw.lan" || back.Conf.Actions[1].Auth != "creds" || back.Conf.Listens[2].PKI != "mx" {
		t.Errorf("Rücklesen: %+v", back.Conf)
	}
	// Zeilen, die das Formular nicht versteht, bleiben in ihrem Abschnitt und an ihrer Stelle.
	h.WriteConfig(ctx, smtpdPath, "queue ttl 2d\ntable aliases file:/etc/mail/aliases\nlisten on lo0\nlisten on lo0 port 10025 tag intern\naction \"l\" mbox alias <aliases>\nmatch auth from any for any action \"l\"\nmatch from local for local action \"l\"\n", 0)
	v, _ = h.SMTPD(ctx)
	if len(v.Conf.Listens) != 2 || v.Conf.Listens[1].Raw == "" || v.Conf.Matches[0].Raw == "" || len(v.Conf.General) != 1 {
		t.Fatalf("Rohzeilen: %+v", v.Conf)
	}
	if _, err := h.SaveSMTPD(ctx, &v.Conf, false, false); err != nil {
		t.Fatal(err)
	}
	got, _ = h.readFile(smtpdPath)
	i1, i2 := strings.Index(got, `match auth from any for any action "l"`), strings.Index(got, `match from local for local action "l"`)
	if !strings.HasPrefix(got, "queue ttl 2d") || !strings.Contains(got, "listen on lo0 port 10025 tag intern") || i1 < 0 || i2 < i1 {
		t.Errorf("Rohzeilen verschoben:\n%s", got)
	}
	// Ablehnungen: offenes Relay, Anmeldung ohne TLS, unbekannte Verweise, Einschleusungen, neue Rohzeilen
	before, _ := h.readFile(smtpdPath)
	base := func() SMTPConf {
		return SMTPConf{Tables: []SMTPTable{{Name: "aliases", Type: "file", Path: "/etc/mail/aliases"}}, Listens: []SMTPListen{{On: "lo0"}}, Actions: []SMTPAction{{Name: "out", Method: "relay"}, {Name: "l", Method: "mbox", Alias: "aliases"}}, Matches: []SMTPMatch{{From: "local", For: "any", Action: "out"}}}
	}
	for i, mod := range []func(c *SMTPConf){
		func(c *SMTPConf) { c.Matches[0] = SMTPMatch{From: "any", For: "any", Action: "out"} },
		func(c *SMTPConf) {
			c.Matches[0] = SMTPMatch{From: "src", FromArg: "0.0.0.0/0", For: "any", Action: "out"}
		},
		func(c *SMTPConf) { c.Listens[0] = SMTPListen{On: "lo0", Auth: "auth"} },
		func(c *SMTPConf) { c.Listens[0] = SMTPListen{On: "lo0", TLS: "tls", PKI: "gibtsnicht"} },
		func(c *SMTPConf) { c.Listens[0] = SMTPListen{On: "eth99"} },
		func(c *SMTPConf) { c.Listens[0] = SMTPListen{On: "lo0", Port: 70000} },
		func(c *SMTPConf) { c.Listens[0] = SMTPListen{On: "socket", Port: 25} },
		func(c *SMTPConf) { c.Matches[0].Action = "gibtsnicht" },
		func(c *SMTPConf) { c.Actions[1].Alias = "gibtsnicht" },
		func(c *SMTPConf) { c.Actions[0].Host = "x; rm" },
		func(c *SMTPConf) { c.Actions[0].Auth = "aliases" },
		func(c *SMTPConf) { c.Actions[1].Host = "relay.example.org" },
		func(c *SMTPConf) { c.Actions[0].Name = "a b" },
		func(c *SMTPConf) { c.Tables[0].Path = "/etc/passwd" },
		func(c *SMTPConf) { c.Matches[0].ForArg, c.Matches[0].For = "x y", "domain" },
		func(c *SMTPConf) {
			c.Matches = append(c.Matches, SMTPMatch{Raw: "match from any for any action \"out\""})
		},
	} {
		cc := base()
		mod(&cc)
		if _, err := h.SaveSMTPD(ctx, &cc, false, false); err == nil {
			t.Errorf("Fall %d akzeptiert", i)
		}
	}
	if after, _ := h.readFile(smtpdPath); after != before {
		t.Error("abgelehnte Eingaben haben smtpd.conf verändert")
	}
}

func TestMailTablesForm(t *testing.T) {
	h := newDemoHost(t)
	ctx := context.Background()
	// virtuelle Benutzer über das smtpd-Formular anlegen
	v, _ := h.SMTPD(ctx)
	c := v.Conf
	c.Tables = append(c.Tables, SMTPTable{Name: "vusers", Type: "file", Path: "/etc/mail/virtual"}, SMTPTable{Name: "creds", Type: "file", Path: "/etc/mail/secrets"})
	c.Actions = append(c.Actions, SMTPAction{Name: "virt", Method: "mbox", Virtual: "vusers"}, SMTPAction{Name: "sm", Method: "relay", Host: "smtp+tls://m.example.org", TLS: "tls", Auth: "creds"})
	h.writeAtomic("/etc/mail/virtual", []byte("# virtuelle Adressen\ninfo@ctw.lan\tthorin\n@ctw.lan\tmara, root\n"), 0o644)
	h.writeAtomic("/etc/mail/secrets", []byte("mailuser $2b$09$geheimerhash\n"), 0o600)
	if _, err := h.SaveSMTPD(ctx, &c, false, false); err != nil {
		t.Fatal(err)
	}
	m, err := h.MailTables(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]MailTable{}
	for _, tb := range m.Tables {
		byName[tb.Name] = tb
	}
	if len(m.Tables) != 2 || byName["aliases"].Kind != "aliases" || byName["vusers"].Kind != "virtual" || len(byName["aliases"].Entries) != 3 || byName["vusers"].Entries[1].Key != "@ctw.lan" || len(byName["vusers"].Entries[1].Targets) != 2 {
		t.Fatalf("Tabellen: %+v", m.Tables)
	}
	if len(m.Hidden) != 1 || m.Hidden[0] != "creds" {
		t.Errorf("Anmeldetabelle nicht verborgen: %v", m.Hidden)
	}
	if b, _ := json.Marshal(m); strings.Contains(string(b), "geheimerhash") {
		t.Error("Passwort-Hash im Ergebnis")
	}
	if _, err := h.SaveMailTable(ctx, "creds", nil, false); err == nil {
		t.Error("Anmeldetabelle bearbeitbar")
	}
	// Alias ändern, neuen anlegen; Kommentare bleiben
	al := byName["aliases"].Entries
	al[2].Targets = []string{"thorin", "mara@example.org"}
	al = append(al, MailEntry{Key: "abuse", Targets: []string{"postmaster"}})
	res, err := h.SaveMailTable(ctx, "aliases", al, false)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := h.readFile("/etc/mail/aliases")
	for _, w := range []string{"# Aliases", "MAILER-DAEMON: postmaster", "root: thorin, mara@example.org", "abuse: postmaster"} {
		if !strings.Contains(got, w) {
			t.Errorf("%q fehlt:\n%s", w, got)
		}
	}
	if !strings.Contains(res.Applied, "neu eingelesen") {
		t.Errorf("Anwenden: %+v", res)
	}
	// Ablehnungen und Schutz
	before, _ := h.readFile("/etc/mail/aliases")
	cur, _ := h.MailTables(ctx)
	var base []MailEntry
	for _, tb := range cur.Tables {
		if tb.Name == "aliases" {
			base = tb.Entries
		}
	}
	mk := func(mod func(e []MailEntry) []MailEntry) []MailEntry {
		return mod(append([]MailEntry{}, base...))
	}
	for i, es := range [][]MailEntry{
		mk(func(e []MailEntry) []MailEntry { // root entfernen
			var out []MailEntry
			for _, x := range e {
				if x.Key != "root" {
					out = append(out, x)
				}
			}
			return out
		}),
		mk(func(e []MailEntry) []MailEntry {
			return append(e, MailEntry{Key: "a", Targets: []string{"b"}}, MailEntry{Key: "b", Targets: []string{"a"}})
		}),
		mk(func(e []MailEntry) []MailEntry { return append(e, MailEntry{Key: "x", Targets: []string{"|/bin/sh"}}) }),
		mk(func(e []MailEntry) []MailEntry {
			return append(e, MailEntry{Key: "x", Targets: []string{"/etc/passwd"}})
		}),
		mk(func(e []MailEntry) []MailEntry {
			return append(e, MailEntry{Key: "x", Targets: []string{"gibtsnicht"}})
		}),
		mk(func(e []MailEntry) []MailEntry { return append(e, MailEntry{Key: "x y", Targets: []string{"thorin"}}) }),
		mk(func(e []MailEntry) []MailEntry { return append(e, MailEntry{Key: "root", Targets: []string{"thorin"}}) }),
		mk(func(e []MailEntry) []MailEntry { return append(e, MailEntry{Key: "x", Targets: nil}) }),
		mk(func(e []MailEntry) []MailEntry { return append(e, MailEntry{Raw: "evil: |/bin/sh"}) }),
		mk(func(e []MailEntry) []MailEntry {
			return append(e, MailEntry{Key: "x", Targets: []string{"thorin\nevil: |sh"}})
		}),
	} {
		if _, err := h.SaveMailTable(ctx, "aliases", es, false); err == nil {
			t.Errorf("Fall %d akzeptiert", i)
		}
	}
	if after, _ := h.readFile("/etc/mail/aliases"); after != before {
		t.Error("abgelehnte Eingaben haben die Datei verändert")
	}
	// virtuelle Tabelle: Adressformen
	vt := byName["vusers"].Entries
	vt = append(vt, MailEntry{Key: "sales@ctw.lan", Targets: []string{"mara"}})
	if _, err := h.SaveMailTable(ctx, "vusers", vt, false); err != nil {
		t.Fatal(err)
	}
	if g, _ := h.readFile("/etc/mail/virtual"); !strings.Contains(g, "sales@ctw.lan\tmara") || !strings.Contains(g, "# virtuelle Adressen") {
		t.Errorf("virtual:\n%s", g)
	}
	if _, err := h.SaveMailTable(ctx, "vusers", append(vt, MailEntry{Key: "ohne-at", Targets: []string{"mara"}}), false); err == nil {
		t.Error("virtuelle Adresse ohne @ akzeptiert")
	}
}

func TestSysctlConfForm(t *testing.T) {
	h := newDemoHost(t)
	ctx := context.Background()
	v, err := h.SysctlConf(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Entries) != 3 || v.Entries[0].Key != "net.inet.ip.forwarding" || v.Entries[2].Value != "7030" || v.Current["kern.maxfiles"] != "7030" || len(v.All) < 15 || len(v.Catalog) < 10 {
		t.Fatalf("Ansicht: %+v catalog=%d all=%d", v.Entries, len(v.Catalog), len(v.All))
	}
	for _, c := range v.Catalog {
		if c.Current == "" && c.Key != "vm.malloc_conf" {
			t.Errorf("Katalogeintrag ohne aktuellen Wert: %s", c.Key)
		}
	}
	// Ändern, hinzufügen, sofort anwenden
	es := v.Entries
	es[2].Value = "20000"
	es = append(es, SysctlEntry{Key: "kern.somaxconn", Value: "1024"}, SysctlEntry{Key: "net.inet.tcp.sack", Value: "0"})
	res, err := h.SaveSysctlConf(ctx, es, true, false)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := h.readFile(sysctlConfPath)
	for _, w := range []string{"net.inet.ip.forwarding=1", "hw.smt=0", "kern.maxfiles=20000", "kern.somaxconn=1024", "net.inet.tcp.sack=0"} {
		if !strings.Contains(got, w) {
			t.Errorf("%q fehlt:\n%s", w, got)
		}
	}
	if !strings.Contains(res.Applied, "3 Wert") {
		t.Errorf("Anwenden: %+v", res)
	}
	out, _ := h.out(ctx, "sysctl", "-n", "kern.somaxconn")
	if strings.TrimSpace(out) != "1024" {
		t.Errorf("laufender Wert: %q", out)
	}
	// Entfernen: Datei ohne Eintrag, Hinweis auf den laufenden Wert
	v, _ = h.SysctlConf(ctx)
	var keep []SysctlEntry
	for _, e := range v.Entries {
		if e.Key != "hw.smt" {
			keep = append(keep, e)
		}
	}
	r2, err := h.SaveSysctlConf(ctx, keep, false, false)
	if err != nil || !strings.Contains(strings.Join(r2.Warnings, "|"), "bis zum Neustart") || !strings.Contains(r2.Applied, "nächsten Start") {
		t.Fatalf("Entfernen: %v %+v", err, r2)
	}
	// Risiko-Hinweis (mit frisch gelesenen Zeilennummern)
	v, _ = h.SysctlConf(ctx)
	keep = v.Entries
	r3, err := h.SaveSysctlConf(ctx, append(append([]SysctlEntry{}, keep...), SysctlEntry{Key: "kern.allowkmem", Value: "1"}), false, true)
	if err != nil || !strings.Contains(strings.Join(r3.Warnings, "|"), "Sicherheit") {
		t.Errorf("Risiko-Hinweis: %v %+v", err, r3)
	}
	// Ablehnungen; die Datei bleibt unverändert und es wird nichts gesetzt
	before, _ := h.readFile(sysctlConfPath)
	for i, bad := range []SysctlEntry{
		{Key: "gibt.es.nicht", Value: "1"},
		{Key: "kern.maxfiles", Value: "1"},                     // doppelt
		{Key: "net.inet.tcp.sack", Value: "2"},                 // bool
		{Key: "kern.somaxconn", Value: "abc"},                  // Zahl
		{Key: "kern.bufcachepercent", Value: "99"},             // Bereich
		{Key: "machdep.allowaperture", Value: "7"},             // enum
		{Key: "kern.hostname", Value: "a b"},                   // Leerzeichen
		{Key: "kern.hostname", Value: "x\nkern.securelevel=2"}, // Einschleusung
		{Key: "kern.ostype", Value: "Linux"},                   // nur lesbar
		{Key: "hw.machine", Value: "arm"},                      // nur lesbar
		{Key: "kern.maxproc", Value: ""},                       // leer
		{Raw: "kern.securelevel=2"},                            // neue Rohzeile
		{Key: "kern.securelevel+", Value: "1"},
	} {
		list := append(append([]SysctlEntry{}, keep...), bad)
		if i == 1 {
			list = append(keep, bad)
		}
		if _, err := h.SaveSysctlConf(ctx, list, true, false); err == nil {
			t.Errorf("Fall %d akzeptiert: %+v", i, bad)
		}
	}
	if after, _ := h.readFile(sysctlConfPath); after != before {
		t.Error("abgelehnte Eingaben haben sysctl.conf verändert")
	}
	if out, _ := h.out(ctx, "sysctl", "-n", "kern.maxproc"); strings.TrimSpace(out) != "1310" {
		t.Errorf("abgelehnte Eingabe änderte den laufenden Wert: %q", out)
	}
}

func TestRelaydForm(t *testing.T) {
	h := newDemoHost(t)
	ctx := context.Background()
	v, err := h.Relayd(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c := v.Conf
	if c.Interval != 5 || c.Timeout != 1000 || !c.LogConn || len(c.Tables) != 1 || c.Tables[0].Name != "web" || len(c.Tables[0].Hosts) != 2 || c.Tables[0].Hosts[0].Retry != 2 || len(c.Services) != 2 {
		t.Fatalf("Parser: %+v", c)
	}
	rl, rd := c.Services[0], c.Services[1]
	if rl.Kind != "relay" || rl.Protocol != "httpfwd" || rl.Table != "web" || rl.Port != 80 || rl.Mode != "roundrobin" || rl.Check.Kind != "http" || rl.Check.Path != "/" || rl.Check.Code != 200 || rd.Kind != "redirect" || rd.Check.Kind != "tcp" || rd.Port != 22 || rd.Listen[0].Port != "2222" {
		t.Fatalf("Dienste: %+v / %+v", rl, rd)
	}
	if len(v.Protocols) != 1 || v.Protocols[0] != "httpfwd" || len(v.VMHosts) < 2 || v.VMHosts[0].Addr == "" || !contains(v.Keypairs, "server") {
		t.Errorf("Auswahl: %v %v %v", v.Protocols, v.VMHosts, v.Keypairs)
	}
	// Rundlauf: Protokoll, Kommentar und Reihenfolge bleiben
	if _, err := h.SaveRelayd(ctx, &c, false, false); err != nil {
		t.Fatal(err)
	}
	got, _ := h.readFile(relaydPath)
	for _, w := range []string{"# relayd.conf", "interval 5", "table <web> { 10.0.0.11 retry 2, 10.0.0.12 }", `http protocol "httpfwd" {`, `match header set "X-Forwarded-For" value "$REMOTE_ADDR"`, `relay "www" {`, "listen on 192.168.50.10 port 80", `forward to <web> port 80 mode roundrobin check http "/" code 200`, `redirect "ssh" {`, "forward to <web> port 22 check tcp"} {
		if !strings.Contains(got, w) {
			t.Errorf("%q fehlt:\n%s", w, got)
		}
	}
	// Ändern: weiterer Host, Verteilverfahren, TLS-Relay mit neuem Protokoll, Sticky
	v, _ = h.Relayd(ctx)
	c = v.Conf
	c.Tables[0].Hosts = append(c.Tables[0].Hosts, RelaydHost{Addr: "10.0.0.20", Retry: 1})
	c.Services[0].Mode = "loadbalance"
	c.Services[0].Check = RelaydCheck{Kind: "http", Path: "/health", Code: 204}
	c.Services[1].Sticky = true
	c.Protocols = []RelaydProtocolNew{{Name: "tlsfwd", Preset: "https", Keypair: "server"}}
	c.Services = append(c.Services, RelaydService{Kind: "relay", Name: "ssl", Listen: []RelaydListen{{Addr: "192.168.50.10", Port: "443", TLS: true}}, Protocol: "tlsfwd", Table: "web", Port: 80, Mode: "source-hash", Check: RelaydCheck{Kind: "tcp"}, Timeout: 300})
	res, err := h.SaveRelayd(ctx, &c, false, false)
	if err != nil {
		t.Fatal(err)
	}
	got, _ = h.readFile(relaydPath)
	for _, w := range []string{"10.0.0.20 retry 1", "mode loadbalance check http \"/health\" code 204", "sticky-address", `http protocol "tlsfwd" {`, `tls { keypair "server" }`, "listen on 192.168.50.10 port 443 tls", "mode source-hash check tcp", "session timeout 300", `http protocol "httpfwd"`} {
		if !strings.Contains(got, w) {
			t.Errorf("%q fehlt:\n%s", w, got)
		}
	}
	if !strings.Contains(strings.Join(res.Warnings, "|"), "anchor") || !strings.Contains(res.Applied, "gestartet") && !strings.Contains(res.Applied, "geladen") {
		t.Errorf("Hinweise: %+v", res)
	}
	// Zeilen mit Unbekanntem bleiben
	h.WriteConfig(ctx, relaydPath, "table <a> { 10.0.0.1 }\nrelay \"x\" {\n\tlisten on 192.168.50.10 port 8080\n\tforward to <a> port 80 check script \"/usr/local/bin/chk\"\n\ttransparent forward to 10.0.0.9 port 1\n}\nredirect \"y\" {\n\tlisten on 192.168.50.10 port 53 pflog\n\tforward to <a>\n}\n", 0)
	v, _ = h.Relayd(ctx)
	if v.Conf.Services[0].Notes == nil || v.Conf.Services[1].Raw == "" {
		t.Fatalf("Notizen/Raw: %+v", v.Conf.Services)
	}
	if _, err := h.SaveRelayd(ctx, &v.Conf, false, false); err != nil {
		t.Fatal(err)
	}
	got, _ = h.readFile(relaydPath)
	for _, w := range []string{`check script "/usr/local/bin/chk"`, "transparent forward to 10.0.0.9 port 1", "port 53 pflog"} {
		if !strings.Contains(got, w) {
			t.Errorf("%q verloren:\n%s", w, got)
		}
	}
	// Ablehnungen
	before, _ := h.readFile(relaydPath)
	base := func() RelaydConf {
		return RelaydConf{Tables: []RelaydTable{{Name: "t", Hosts: []RelaydHost{{Addr: "10.0.0.1"}}}}, Services: []RelaydService{{Kind: "relay", Name: "r", Listen: []RelaydListen{{Addr: "192.168.50.10", Port: "80"}}, Table: "t", Port: 80}}}
	}
	for i, mod := range []func(c *RelaydConf){
		func(c *RelaydConf) { c.Interval, c.Timeout = 5, 6000 },
		func(c *RelaydConf) { c.Tables[0].Hosts = nil },
		func(c *RelaydConf) { c.Tables[0].Hosts[0].Addr = "a b" },
		func(c *RelaydConf) { c.Tables[0].Hosts[0].Addr = "10.0.0.1 }\nrelay \"evil\" {" },
		func(c *RelaydConf) { c.Tables[0].Hosts[0].Retry = 99 },
		func(c *RelaydConf) { c.Tables[0].Name = "a>b" },
		func(c *RelaydConf) { c.Services[0].Table = "gibtsnicht" },
		func(c *RelaydConf) { c.Services[0].Mode = "least-states" },
		func(c *RelaydConf) { c.Services[0].Kind = "redirect"; c.Services[0].Mode = "hash" },
		func(c *RelaydConf) { c.Services[0].Listen[0].Addr = "1.2.3.4" },
		func(c *RelaydConf) { c.Services[0].Listen[0].Port = "99999" },
		func(c *RelaydConf) { c.Services[0].Listen[0].Port = "80:90" },
		func(c *RelaydConf) { c.Services[0].Protocol = "gibtsnicht" },
		func(c *RelaydConf) { c.Services[0].Check = RelaydCheck{Kind: "http", Path: "x", Code: 200} },
		func(c *RelaydConf) { c.Services[0].Check = RelaydCheck{Kind: "http", Path: "/\"; evil", Code: 200} },
		func(c *RelaydConf) { c.Services[0].Check = RelaydCheck{Kind: "script", Path: "/bin/sh"} },
		func(c *RelaydConf) { c.Services[0].Name = "a\"b" },
		func(c *RelaydConf) {
			c.Protocols = []RelaydProtocolNew{{Name: "p", Preset: "https", Keypair: "gibtsnicht"}}
		},
		func(c *RelaydConf) { c.Protocols = []RelaydProtocolNew{{Name: "p", Preset: "evil"}} },
		func(c *RelaydConf) { c.Services[0].Timeout = -1 },
		func(c *RelaydConf) {
			c.Services = append(c.Services, RelaydService{Kind: "redirect", Name: "d", Listen: []RelaydListen{{Addr: "192.168.50.10", Port: "1"}}})
		},
		func(c *RelaydConf) { c.Services[0].ID = 99 },
	} {
		cc := base()
		mod(&cc)
		if _, err := h.SaveRelayd(ctx, &cc, false, false); err == nil {
			t.Errorf("Fall %d akzeptiert", i)
		}
	}
	if after, _ := h.readFile(relaydPath); after != before {
		t.Error("abgelehnte Eingaben haben relayd.conf verändert")
	}
}
