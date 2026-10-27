package obsd

import (
	"context"
	"strings"
	"testing"
)

func TestNTPForm(t *testing.T) {
	h := newDemoHost(t)
	ctx := context.Background()
	v, err := h.NTP(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Conf.Servers) != 1 || !v.Conf.Servers[0].Pool || len(v.Conf.Sensors) != 1 || len(v.Conf.Constraints) != 2 || !v.Running || !strings.Contains(v.Status, "clock synced") {
		t.Fatalf("NTP: %+v %q", v.Conf, v.Status)
	}
	if len(v.Sensors) != 2 || v.Sensors[0] != "udcf0" {
		t.Errorf("Sensoren: %v", v.Sensors)
	}
	// Unbekannte Zeilen und Kommentare bleiben.
	h.WriteConfig(ctx, ntpPath, "# Zeitquellen\nquery from 192.168.50.10\nserver 192.168.50.1 weight 2\nsensor udcf0 correction 5000 stratum 2\nlisten on 127.0.0.1\n", 0)
	v, _ = h.NTP(ctx)
	if len(v.Preserved) != 2 || len(v.Conf.Servers) != 1 || v.Conf.Servers[0].Weight != 2 || v.Conf.Listen[0] != "127.0.0.1" {
		t.Fatalf("Parser: %+v %v", v.Conf, v.Preserved)
	}
	c := v.Conf
	c.Servers = append(c.Servers, NTPServer{Address: "pool.ntp.org", Pool: true, Weight: 5, Trusted: true})
	c.Constraints = []NTPConstraint{{URL: "https://www.example.org/", Pool: true}}
	res, err := h.SaveNTP(ctx, &c, false, false)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := h.readFile(ntpPath)
	for _, want := range []string{"# Zeitquellen", "query from 192.168.50.10", "sensor udcf0 correction 5000 stratum 2", "listen on 127.0.0.1", "server 192.168.50.1 weight 2", "servers pool.ntp.org trusted weight 5", `constraints from "https://www.example.org/"`} {
		if !strings.Contains(got, want) {
			t.Errorf("%q fehlt:\n%s", want, got)
		}
	}
	if res.Applied != "ntpd neu gestartet." {
		t.Errorf("Anwenden: %+v", res)
	}
	// Vorschau schreibt nichts.
	c.Servers = c.Servers[:1]
	before, _ := h.readFile(ntpPath)
	if p, err := h.SaveNTP(ctx, &c, false, true); err != nil || p.Content == before {
		t.Errorf("Vorschau: %v", err)
	}
	if after, _ := h.readFile(ntpPath); after != before {
		t.Error("Vorschau hat geschrieben")
	}
	for i, bad := range []NTPConf{
		{},
		{Servers: []NTPServer{{Address: "a b"}}},
		{Servers: []NTPServer{{Address: "x;y"}}},
		{Servers: []NTPServer{{Address: "pool.ntp.org", Weight: 11}}},
		{Servers: []NTPServer{{Address: "pool.ntp.org"}, {Address: "pool.ntp.org"}}},
		{Servers: []NTPServer{{Address: "pool.ntp.org"}}, Sensors: []NTPSensor{{Device: "gibtsnicht"}}},
		{Servers: []NTPServer{{Address: "pool.ntp.org"}}, Constraints: []NTPConstraint{{URL: "http://x.de"}}},
		{Servers: []NTPServer{{Address: "pool.ntp.org"}}, Constraints: []NTPConstraint{{URL: "https://x.de\"\nserver evil"}}},
		{Servers: []NTPServer{{Address: "pool.ntp.org"}}, Listen: []string{"1.2.3.4"}},
	} {
		if _, err := h.SaveNTP(ctx, &bad, false, false); err == nil {
			t.Errorf("Fall %d akzeptiert", i)
		}
	}
	if after, _ := h.readFile(ntpPath); after != before {
		t.Error("abgelehnte Eingaben haben die Datei verändert")
	}
}

func TestResolvForm(t *testing.T) {
	h := newDemoHost(t)
	ctx := context.Background()
	h.WriteConfig(ctx, resolvPath, "# vom Admin\nnameserver 127.0.0.1\nnameserver 9.9.9.9\nlookup file bind\nsearch ctw.lan\nsortlist 10.0.0.0/255.0.0.0\noptions insecure1 ndots:2\n", 0)
	v, err := h.Resolv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Conf.Nameservers) != 2 || v.Conf.NDots != 2 || len(v.OtherOpts) != 1 || len(v.Preserved) != 1 || v.Suggestions[0].Address != "127.0.0.1" {
		t.Fatalf("Resolv: %+v other=%v pres=%v sug=%v", v.Conf, v.OtherOpts, v.Preserved, v.Suggestions)
	}
	c := v.Conf
	c.Nameservers = []string{"192.168.50.1", "2620:fe::fe"}
	c.Search = []string{"ctw.lan", "vm.ctw.lan"}
	c.EDNS0, c.TrustAD, c.NDots = true, true, 0
	res, err := h.SaveResolv(ctx, &c, false)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := h.readFile(resolvPath)
	for _, want := range []string{"# vom Admin", "sortlist 10.0.0.0/255.0.0.0", "lookup file bind", "search ctw.lan vm.ctw.lan", "nameserver 192.168.50.1", "nameserver 2620:fe::fe", "options edns0 trust-ad insecure1"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q fehlt:\n%s", want, got)
		}
	}
	if strings.Contains(got, "ndots") || strings.Contains(got, "127.0.0.1") || strings.Count(got, "options") != 1 {
		t.Errorf("Rest:\n%s", got)
	}
	if len(res.Warnings) == 0 { // resolvd ist in der Demo aktiv
		t.Error("keine Warnung zu resolvd")
	}
	for i, bad := range []ResolvConf{
		{Lookup: []string{"bind"}, Nameservers: []string{"nicht-ip"}},
		{Lookup: []string{"bind"}, Nameservers: []string{"1.1.1.1", "1.1.1.1"}},
		{Lookup: []string{"bind"}, Nameservers: []string{"1.1.1.1", "1.0.0.1", "8.8.8.8", "8.8.4.4", "9.9.9.9", "9.9.9.10"}},
		{Lookup: []string{"bind"}, Search: []string{"a.de", "b.de", "c.de", "d.de", "e.de", "f.de", "g.de"}},
		{Lookup: []string{"bind"}, Search: []string{"a b.de"}},
		{Lookup: []string{"bind"}, Domain: "x\nnameserver 6.6.6.6"},
		{Lookup: []string{}},
		{Lookup: []string{"yp"}},
		{Lookup: []string{"bind"}, Family: []string{"inet5"}},
		{Lookup: []string{"bind"}, NDots: 99},
	} {
		if _, err := h.SaveResolv(ctx, &bad, false); err == nil {
			t.Errorf("Fall %d akzeptiert", i)
		}
	}
}

func TestHostsForm(t *testing.T) {
	h := newDemoHost(t)
	ctx := context.Background()
	v, err := h.Hosts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Entries) != 5 || v.Entries[0].IP != "127.0.0.1" {
		t.Fatalf("Hosts: %+v", v.Entries)
	}
	h.WriteConfig(ctx, hostsPath, "# Zonen\n127.0.0.1\tlocalhost\n::1\t\tlocalhost\n192.168.50.10\tnordlicht.ctw.lan nordlicht # Host\n\n# VMs\n10.0.0.11\tweb01\n", 0)
	v, _ = h.Hosts(ctx)
	es := v.Entries
	es[2].Names = []string{"nordlicht.ctw.lan", "nordlicht", "nl"} // ändern
	es = append(es[:3], es[3:]...)
	es = append(es, HostsEntry{IP: "10.0.0.20", Names: []string{"gitea.vm.ctw.lan", "gitea"}, Comment: "neu"})
	if _, err := h.SaveHosts(ctx, es, false); err != nil {
		t.Fatal(err)
	}
	got, _ := h.readFile(hostsPath)
	for _, want := range []string{"# Zonen", "# VMs", "192.168.50.10\tnordlicht.ctw.lan nordlicht nl\t# Host", "10.0.0.11\tweb01", "10.0.0.20\tgitea.vm.ctw.lan gitea\t# neu"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q fehlt:\n%s", want, got)
		}
	}
	// Löschen: web01 weglassen.
	v, _ = h.Hosts(ctx)
	var keep []HostsEntry
	for _, e := range v.Entries {
		if !contains(e.Names, "web01") {
			keep = append(keep, e)
		}
	}
	if _, err := h.SaveHosts(ctx, keep, false); err != nil {
		t.Fatal(err)
	}
	if got, _ = h.readFile(hostsPath); strings.Contains(got, "web01") || !strings.Contains(got, "# VMs") {
		t.Errorf("nach Löschen:\n%s", got)
	}
	// Schutz: localhost muss bleiben, Eingaben werden geprüft.
	v, _ = h.Hosts(ctx)
	noLocal := v.Entries[1:]
	if _, err := h.SaveHosts(ctx, noLocal, false); err == nil {
		t.Error("localhost ohne 127.0.0.1 akzeptiert")
	}
	for i, bad := range []HostsEntry{
		{IP: "kein-ip", Names: []string{"x"}},
		{IP: "10.0.0.1", Names: []string{}},
		{IP: "10.0.0.1", Names: []string{"a b"}},
		{IP: "10.0.0.1", Names: []string{"x\n10.9.9.9 evil"}},
		{IP: "10.0.0.1", Names: []string{"x"}, Comment: "a\nb"},
		{ID: 99, IP: "10.0.0.1", Names: []string{"x"}},
	} {
		if _, err := h.SaveHosts(ctx, append(append([]HostsEntry{}, v.Entries...), bad), false); err == nil {
			t.Errorf("Fall %d akzeptiert", i)
		}
	}
}

func TestSSHForm(t *testing.T) {
	h := newDemoHost(t)
	ctx := context.Background()
	h.WriteConfig(ctx, sshdPath, "#\tOpenBSD sshd_config\nPermitRootLogin no\n#PasswordAuthentication yes\nPasswordAuthentication no\nSubsystem\tsftp\t/usr/libexec/sftp-server\n\nMatch Address 10.0.0.0/24\n\tPasswordAuthentication yes\n", 0)
	v, err := h.SSH(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v.Conf.PermitRootLogin != "no" || v.Conf.PasswordAuth != "no" || v.Matches != 1 || len(v.Preserved) != 1 || v.Keys["thorin"] != 1 || v.Keys["mara"] != 0 {
		t.Fatalf("SSH: %+v keys=%v pres=%v", v.Conf, v.Keys, v.Preserved)
	}
	c := v.Conf
	c.MaxAuthTries, c.X11Forwarding, c.AllowUsers, c.LogLevel, c.Ports = 3, "no", []string{"thorin"}, "VERBOSE", []int{22, 2222}
	res, err := h.SaveSSH(ctx, "thorin", &c, false)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := h.readFile(sshdPath)
	i := strings.Index(got, "Match Address")
	head, tail := got[:i], got[i:]
	for _, want := range []string{"#PasswordAuthentication yes", "Subsystem\tsftp", "Port 22", "Port 2222", "PermitRootLogin no", "PasswordAuthentication no", "MaxAuthTries 3", "AllowUsers thorin", "LogLevel VERBOSE", "X11Forwarding no"} {
		if !strings.Contains(head, want) {
			t.Errorf("%q fehlt im globalen Teil:\n%s", want, got)
		}
	}
	if strings.Count(head, "PasswordAuthentication no") != 1 || !strings.Contains(tail, "PasswordAuthentication yes") || strings.Contains(tail, "Port") {
		t.Errorf("Match-Block verändert:\n%s", got)
	}
	if !strings.Contains(strings.Join(res.Warnings, "|"), "2222") || !strings.Contains(strings.Join(res.Warnings, "|"), "Match") || !strings.Contains(res.Applied, "neu geladen") {
		t.Errorf("Hinweise: %+v", res)
	}
	// Aussperr-Schutz
	base := v.Conf
	cases := []struct {
		name string
		mod  func(c *SSHConf)
		who  string
	}{
		{"alle Methoden aus", func(c *SSHConf) { c.PasswordAuth, c.KbdInteractive, c.PubkeyAuth = "no", "no", "no" }, "thorin"},
		{"Passwort aus ohne Schlüssel", func(c *SSHConf) { c.PasswordAuth, c.KbdInteractive = "no", "no" }, "mara"},
		{"AllowUsers ohne mich", func(c *SSHConf) { c.AllowUsers = []string{"mara"} }, "thorin"},
		{"AllowGroups ohne meine Gruppe", func(c *SSHConf) { c.AllowGroups = []string{"wheel"} }, "mara"},
		{"unbekannter Benutzer", func(c *SSHConf) { c.AllowUsers = []string{"thorin", "gibtsnicht"} }, "thorin"},
		{"unbekannte Gruppe", func(c *SSHConf) { c.AllowGroups = []string{"gibtsnicht"} }, "thorin"},
		{"Root-Login ungültig", func(c *SSHConf) { c.PermitRootLogin = "vielleicht" }, "thorin"},
		{"Port 0", func(c *SSHConf) { c.Ports = []int{0} }, "thorin"},
		{"doppelter Port", func(c *SSHConf) { c.Ports = []int{22, 22} }, "thorin"},
		{"MaxAuthTries 99", func(c *SSHConf) { c.MaxAuthTries = 99 }, "thorin"},
		{"fremde Adresse", func(c *SSHConf) { c.ListenAddress = []string{"1.2.3.4"} }, "thorin"},
		{"LogLevel", func(c *SSHConf) { c.LogLevel = "ALL\nPermitEmptyPasswords yes" }, "thorin"},
	}
	before, _ := h.readFile(sshdPath)
	for _, tc := range cases {
		c := base
		tc.mod(&c)
		if _, err := h.SaveSSH(ctx, tc.who, &c, false); err == nil {
			t.Errorf("%s: akzeptiert", tc.name)
		}
	}
	if after, _ := h.readFile(sshdPath); after != before {
		t.Error("abgelehnte Eingaben haben die Datei verändert")
	}
	// Mit hinterlegtem Schlüssel darf thorin das Passwort abschalten.
	c = base
	c.PasswordAuth, c.KbdInteractive = "no", "no"
	if _, err := h.SaveSSH(ctx, "thorin", &c, false); err != nil {
		t.Errorf("Schlüssel vorhanden, trotzdem abgelehnt: %v", err)
	}
}
