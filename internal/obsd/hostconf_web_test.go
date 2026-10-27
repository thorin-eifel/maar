package obsd

import (
	"context"
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
