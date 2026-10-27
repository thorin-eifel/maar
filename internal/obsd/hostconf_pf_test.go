package obsd

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestPFRuleLines(t *testing.T) {
	for _, c := range []struct {
		r    PFRule
		want string
	}{
		{PFRule{Action: "pass", Dir: "in", Iface: "egress", Proto: "tcp", From: "<lan>", To: "self", Ports: "80,443", Quick: true, Log: true}, "pass in log quick on egress proto tcp from <lan> to self port { 80 443 }"},
		{PFRule{Action: "block", Dir: "in", From: "203.0.113.0/24"}, "block in from 203.0.113.0/24"},
		{PFRule{Action: "pass", Proto: "icmp"}, "pass proto icmp all"},
		{PFRule{Action: "pass", Dir: "out", Proto: "udp", Ports: "53"}, "pass out proto udp all port 53"},
	} {
		if got := c.r.line(); got != c.want {
			t.Errorf("%q, erwartet %q", got, c.want)
		}
	}
}

func TestPFMergeKeepsRestAndAnchorOrder(t *testing.T) {
	src := "set skip on lo\nblock log all\npass out\n\n# VM-Netze von maar: x\nanchor \"maar\"\nload anchor \"maar\" from \"/etc/pf.maar.conf\"\n"
	c := &PFManaged{Tables: []PFTable{{Name: "blocklist", Addrs: []string{"203.0.113.7", "198.51.100.0/24"}}}, Rules: []PFRule{{Action: "block", Dir: "in", From: "<blocklist>", Quick: true, Descr: "Sperrliste"}}}
	out := mergePF(src, c)
	i, j := strings.Index(out, pfBegin), strings.Index(out, "anchor \"maar\"")
	if i < 0 || j < i || !strings.HasPrefix(out, "set skip on lo\nblock log all\npass out\n") || !strings.Contains(out, "table <blocklist> persist { 203.0.113.7, 198.51.100.0/24 }") || !strings.Contains(out, "# Sperrliste\nblock in quick from <blocklist>") {
		t.Fatalf("Merge:\n%s", out)
	}
	// Ersetzen und Entfernen an Ort und Stelle
	c.Rules[0].Quick = false
	if again := mergePF(out, c); strings.Count(again, pfBegin) != 1 || strings.Contains(again, "block in quick") {
		t.Errorf("Ersetzen:\n%s", again)
	}
	if gone := mergePF(out, &PFManaged{}); gone != src {
		t.Errorf("Entfernen ergibt nicht das Original:\n%s", gone)
	}
}

func TestPFValidation(t *testing.T) {
	known := map[string]bool{"lan": true}
	ifs := map[string]bool{"egress": true, "em0": true}
	for i, bad := range []PFManaged{
		{Rules: []PFRule{{Action: "allow"}}},
		{Rules: []PFRule{{Action: "pass", Iface: "evil0"}}},
		{Rules: []PFRule{{Action: "pass", Iface: "em0; pass all"}}},
		{Rules: []PFRule{{Action: "pass", Proto: "tcp", From: "<gibtsnicht>"}}},
		{Rules: []PFRule{{Action: "pass", Proto: "tcp", To: "1.2.3.4 port 22"}}},
		{Rules: []PFRule{{Action: "pass", Proto: "tcp", To: "example.com"}}},
		{Rules: []PFRule{{Action: "pass", Proto: "any"}}},
		{Rules: []PFRule{{Action: "pass", Ports: "80"}}}, // Port ohne TCP/UDP
		{Rules: []PFRule{{Action: "pass", Proto: "tcp", Ports: "0"}}},
		{Rules: []PFRule{{Action: "pass", Proto: "tcp", Ports: "70000"}}},
		{Rules: []PFRule{{Action: "pass", Proto: "tcp", Ports: "90:80"}}},
		{Rules: []PFRule{{Action: "pass", Proto: "tcp", Ports: "80,{ 22 }"}}},
		{Rules: []PFRule{{Action: "pass", Descr: "a\nblock all"}}},
		{Tables: []PFTable{{Name: "lan"}}}, // gibt es schon
		{Tables: []PFTable{{Name: "x y"}}},
		{Tables: []PFTable{{Name: "t", Addrs: []string{"nicht-ip"}}}},
		{Tables: []PFTable{{Name: "t"}, {Name: "t"}}},
	} {
		if err := bad.validate(known, ifs); err == nil {
			t.Errorf("Fall %d akzeptiert: %+v", i, bad)
		}
	}
	good := PFManaged{Tables: []PFTable{{Name: "ext"}}, Rules: []PFRule{{Action: "pass", Dir: "in", Iface: "egress", Proto: "tcp", From: "<ext>", To: "self", Ports: "8000:8080"}, {Action: "block", From: "<lan>"}}}
	if err := good.validate(known, ifs); err != nil {
		t.Error(err)
	}
}

func TestPFApplyConfirmRevert(t *testing.T) {
	old := pfConfirmWait
	pfConfirmWait = 400 * time.Millisecond
	defer func() { pfConfirmWait = old }()
	h := newDemoHost(t)
	ctx := context.Background()
	v, err := h.PFForm(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Enabled || len(v.Tables) != 2 || len(v.Existing) == 0 || v.Interfaces[0] != "egress" && !containsStr(v.Interfaces, "egress") {
		t.Fatalf("Form: %+v", v)
	}
	orig, _ := h.readFile(pfConfPath)
	c := PFManaged{Tables: []PFTable{{Name: "admins", Addrs: []string{"192.168.50.5"}}}, Rules: []PFRule{
		{Action: "pass", Dir: "in", Iface: "egress", Proto: "tcp", From: "<admins>", To: "self", Ports: "8443", Quick: true, Descr: "Admin-Port"},
		{Action: "block", Dir: "in", Proto: "tcp", To: "self", Ports: "9443", Quick: true}}}
	guard := []PFGuard{{Port: 9443, Label: "diese Oberfläche"}, {Port: 22, Label: "SSH"}}

	// Vorschau schreibt nichts.
	p, err := h.SavePF(ctx, &c, guard, true)
	if err != nil || !strings.Contains(p.Content, "table <admins>") || len(p.Warnings) == 0 || !strings.Contains(p.Warnings[0], "9443") {
		t.Fatalf("Vorschau: %v %+v", err, p)
	}
	if now, _ := h.readFile(pfConfPath); now != orig {
		t.Fatal("Vorschau hat geschrieben")
	}

	// Anwenden → ausstehend; ein zweites Anwenden wird abgelehnt.
	res, err := h.SavePF(ctx, &c, guard, false)
	if err != nil || res.Pending == 0 {
		t.Fatalf("Anwenden: %v %+v", err, res)
	}
	if _, err := h.SavePF(ctx, &c, guard, false); err == nil {
		t.Error("zweite Änderung trotz ausstehender Bestätigung")
	}
	if fv, _ := h.PFForm(ctx); fv.Pending == nil {
		t.Error("ausstehende Änderung nicht sichtbar")
	}
	now, _ := h.readFile(pfConfPath)
	if !strings.Contains(now, "block in quick proto tcp to self port 9443") || !strings.Contains(now, "anchor \"maar\"") {
		t.Errorf("pf.conf:\n%s", now)
	}
	// Ohne Bestätigung: automatische Rücknahme.
	time.Sleep(900 * time.Millisecond)
	if back, _ := h.readFile(pfConfPath); back != orig {
		t.Errorf("nicht zurückgenommen:\n%s", back)
	}
	if err := h.PFConfirm(ctx); err == nil {
		t.Error("Bestätigung nach Ablauf akzeptiert")
	}
	if fv, _ := h.PFForm(ctx); fv.Pending != nil || len(fv.Conf.Rules) != 0 {
		t.Errorf("Zustand nach Rücknahme: %+v", fv)
	}

	// Anwenden und bestätigen: dauerhaft, Regeln gespeichert.
	pfConfirmWait = time.Minute
	if _, err := h.SavePF(ctx, &c, guard, false); err != nil {
		t.Fatal(err)
	}
	if err := h.PFConfirm(ctx); err != nil {
		t.Fatal(err)
	}
	fv, _ := h.PFForm(ctx)
	if len(fv.Conf.Rules) != 2 || fv.Pending != nil || containsStr(fv.Tables, "admins") {
		t.Errorf("nach Bestätigung: %+v", fv)
	}
	// Manuelle Rücknahme
	c.Rules = c.Rules[:1]
	if _, err := h.SavePF(ctx, &c, guard, false); err != nil {
		t.Fatal(err)
	}
	if err := h.PFRevert(ctx, false); err != nil {
		t.Fatal(err)
	}
	if after, _ := h.readFile(pfConfPath); !strings.Contains(after, "port 9443") || !strings.Contains(after, "block in quick") {
		t.Errorf("Rücknahme stellte den bestätigten Stand nicht her:\n%s", after)
	}
}

func TestPFRecoverAfterRestart(t *testing.T) {
	h := newDemoHost(t)
	ctx := context.Background()
	orig, _ := h.readFile(pfConfPath)
	pfConfirmWait = time.Hour
	defer func() { pfConfirmWait = 60 * time.Second }()
	c := PFManaged{Rules: []PFRule{{Action: "block", Dir: "in", To: "self", Proto: "tcp", Ports: "22", Quick: true}}}
	if _, err := h.SavePF(ctx, &c, nil, false); err != nil {
		t.Fatal(err)
	}
	// „Neustart“: neuer Host-Wert mit demselben Dateisystem.
	h2 := NewHost(h.R, h.Root, true)
	h2.RecoverPF(ctx)
	if back, _ := h2.readFile(pfConfPath); back != orig {
		t.Errorf("Wiederherstellung:\n%s", back)
	}
	if h2.exists(pfPendingPath) {
		t.Error("Markierung blieb liegen")
	}
}

func containsStr(l []string, s string) bool { return contains(l, s) }

func TestDoasForm(t *testing.T) {
	h := newDemoHost(t)
	ctx := context.Background()
	h.WriteConfig(ctx, doasPath, "# Rechte\n\n# Admins\npermit persist :wheel\n# Sicherungen\npermit nopass thorin as root cmd /usr/sbin/vmctl args status\npermit setenv { PATH } mara\npermit nolog keepenv mara as root cmd /usr/sbin/rcctl\n", 0)
	v, err := h.Doas(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rs := v.Conf.Rules
	if len(rs) != 4 || rs[0].Identity != ":wheel" || !rs[0].Persist || rs[0].Comment != "Admins" || rs[1].Cmd != "/usr/sbin/vmctl" || len(rs[1].Args) != 1 || rs[2].Raw == "" || !rs[3].KeepEnv || !rs[3].NoLog {
		t.Fatalf("Parser: %+v", rs)
	}
	if len(v.Header) == 0 || len(v.Users) == 0 || len(v.Commands) == 0 {
		t.Errorf("Auswahl: %+v", v)
	}
	// Unverändert zurückschreiben: gleiche Regeln in gleicher Reihenfolge.
	if _, err := h.SaveDoas(ctx, "thorin", &v.Conf, false); err != nil {
		t.Fatal(err)
	}
	got, _ := h.readFile(doasPath)
	for _, w := range []string{"# Admins\npermit persist :wheel", "permit nopass thorin as root cmd /usr/sbin/vmctl args status", "permit setenv { PATH } mara", "permit nolog keepenv mara as root cmd /usr/sbin/rcctl", "# Rechte"} {
		if !strings.Contains(got, w) {
			t.Errorf("%q fehlt:\n%s", w, got)
		}
	}
	// Neue Regel, geändert.
	v2, _ := h.Doas(ctx)
	v2.Conf.Rules = append(v2.Conf.Rules, DoasRule{Action: "permit", Persist: true, Identity: "mara", As: "root", Cmd: "/usr/sbin/pkg_add", Comment: "Pakete"})
	res, err := h.SaveDoas(ctx, "thorin", &v2.Conf, false)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ = h.readFile(doasPath); !strings.Contains(got, "# Pakete\npermit persist mara as root cmd /usr/sbin/pkg_add") {
		t.Errorf("neue Regel:\n%s", got)
	}
	_ = res
	// Warnung: Rechte des Benutzers entzogen / nopass ohne Befehl.
	w := DoasConf{Rules: []DoasRule{{Action: "deny", Identity: "thorin"}}}
	if r, err := h.SaveDoas(ctx, "thorin", &w, true); err != nil || len(r.Warnings) == 0 {
		t.Errorf("keine Warnung beim Entzug: %v %+v", err, r)
	}
	n := DoasConf{Rules: []DoasRule{{Action: "permit", NoPass: true, Identity: "mara"}}}
	if r, _ := h.SaveDoas(ctx, "thorin", &n, true); !strings.Contains(strings.Join(r.Warnings, "|"), "nopass") {
		t.Errorf("keine nopass-Warnung: %+v", r)
	}
	if r, err := h.SaveDoas(ctx, "thorin", &DoasConf{Rules: []DoasRule{{Action: "permit", NoPass: true, Identity: "thorin", As: "root", Cmd: "vmctl"}}}, true); err != nil || !strings.Contains(strings.Join(r.Warnings, "|"), "buchstäblich") {
		t.Errorf("relativer Befehl: %v %+v", err, r)
	}
	before, _ := h.readFile(doasPath)
	for i, bad := range []DoasRule{
		{Action: "allow", Identity: "thorin"},
		{Action: "permit", Identity: "gibtsnicht"},
		{Action: "permit", Identity: ":gibtsnicht"},
		{Action: "permit", Identity: "thorin", As: "gibtsnicht"},
		{Action: "permit", Identity: "thorin", Cmd: "rc ctl"},
		{Action: "permit", Identity: "thorin", Cmd: "../rcctl"},
		{Action: "permit", Identity: "thorin", Cmd: "/usr/sbin/rcctl; reboot"},
		{Action: "permit", Identity: "thorin", Cmd: "/usr/sbin/rcctl", Args: []string{"a b"}},
		{Action: "permit", Identity: "thorin", Args: []string{"x"}},
		{Action: "deny", Identity: "thorin", NoPass: true},
		{Action: "permit", Identity: "thorin", NoPass: true, Persist: true},
		{Action: "permit", Identity: "thorin", Comment: "a\npermit mara"},
		{Raw: "permit nopass evil as root"},
	} {
		if _, err := h.SaveDoas(ctx, "thorin", &DoasConf{Rules: []DoasRule{bad}}, false); err == nil {
			t.Errorf("Fall %d akzeptiert: %+v", i, bad)
		}
	}
	if after, _ := h.readFile(doasPath); after != before {
		t.Error("abgelehnte Eingaben haben doas.conf verändert")
	}
}
