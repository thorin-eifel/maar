package server

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func do(t *testing.T, c *http.Client, method, url, csrf, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("X-CSRF-Token", csrf)
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := res.Body.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return res.StatusCode, b.String()
}

func TestRolesAndEvents(t *testing.T) {
	s, ts, c := newTestServerS(t)
	csrf := login(t, ts, c)
	vm := ts.URL + "/api/vms/"

	// Standard: wheel-Konten sind Administratoren.
	if code, _ := do(t, c, "POST", vm+"web01/pause", csrf, ""); code != 200 {
		t.Fatalf("admin pause: %d", code)
	}
	// Selbstaussperren wird abgelehnt.
	if code, out := do(t, c, "PUT", ts.URL+"/api/access", csrf, `{"default":"admin","users":[{"user":"demo","role":"viewer","vms":[]}]}`); code != 400 || !strings.Contains(out, "selbst") {
		t.Errorf("Selbstaussperrung: %d %s", code, out)
	}
	if code, _ := do(t, c, "PUT", ts.URL+"/api/access", csrf, `{"default":"admin","users":[{"user":"x y","role":"viewer","vms":[]}]}`); code != 400 {
		t.Errorf("ungültiger Benutzer: %d", code)
	}
	if code, _ := do(t, c, "PUT", ts.URL+"/api/access", csrf, `{"default":"admin","users":[{"user":"anna","role":"operator","vms":["gibtsnicht"]}]}`); code != 400 {
		t.Errorf("unbekannte VM: %d", code)
	}
	if code, _ := do(t, c, "PUT", ts.URL+"/api/access", csrf, `{"default":"admin","users":[{"user":"anna","role":"operator","vms":["web01"]}]}`); code != 200 {
		t.Fatalf("Rolle speichern: %d", code)
	}

	setGrant := func(g Grant) {
		s.Access.mu.Lock()
		s.Access.cfg.Users = []Grant{g}
		s.Access.mu.Unlock()
	}

	// Operator, auf web01 begrenzt: erlaubt (nicht 403) und verboten (403).
	setGrant(Grant{User: "demo", Role: "operator", VMs: []string{"web01"}})
	for _, tc := range []struct {
		method, path, body string
		denied             bool
	}{
		{"POST", vm + "web01/unpause", "", false},
		{"POST", vm + "web01/snapshots", `{"name":"vor-update"}`, false},
		{"GET", vm + "web01/snapshots", "", false},
		{"GET", vm + "web01/detail", "", false},
		{"GET", vm + "db01/detail", "", true},
		{"GET", ts.URL + "/api/san/datastores/vm-pool", "", false},
		{"GET", ts.URL + "/api/vnet/portgroups/intern", "", false},
		{"GET", ts.URL + "/api/overview", "", false},
		{"POST", vm + "web01/kill", "", true},
		{"POST", vm + "db01/stop", "", true},
		{"DELETE", vm + "web01", "", true},
		{"POST", ts.URL + "/api/power/reboot", "", true},
		{"GET", ts.URL + "/api/file?path=/etc/pf.conf", "", true},
		{"GET", ts.URL + "/api/access", "", true},
		{"GET", vm + "db01/snapshots", "", true},
	} {
		code, out := do(t, c, tc.method, tc.path, csrf, tc.body)
		if (code == http.StatusForbidden && strings.Contains(out, "Berechtigung")) != tc.denied {
			t.Errorf("operator %s %s: %d %s", tc.method, tc.path, code, out)
		}
	}
	if _, out := do(t, c, "GET", vm, csrf, ""); strings.Contains(out, `"db01"`) {
		t.Errorf("VM-Liste zeigt db01 trotz Eingrenzung")
	}
	if _, out := do(t, c, "GET", ts.URL+"/api/vnet/portgroups/intern", csrf, ""); strings.Contains(out, `"db01"`) || !strings.Contains(out, `"web01"`) {
		t.Errorf("Portgruppe ignoriert die Eingrenzung: %.300s", out)
	}
	if _, out := do(t, c, "GET", ts.URL+"/api/hosts", csrf, ""); strings.Contains(out, `"db01"`) || !strings.Contains(out, `"web01"`) {
		t.Errorf("Hostliste ignoriert die Eingrenzung: %.200s", out)
	}

	// Betrachter: nur lesen.
	setGrant(Grant{User: "demo", Role: "viewer"})
	if code, _ := do(t, c, "POST", vm+"web01/unpause", csrf, ""); code != http.StatusForbidden {
		t.Errorf("viewer start: %d", code)
	}
	if code, _ := do(t, c, "GET", ts.URL+"/api/services", csrf, ""); code != 200 {
		t.Errorf("viewer lesen: %d", code)
	}
	if code, _ := do(t, c, "GET", ts.URL+"/api/conf/ssh", csrf, ""); code != http.StatusForbidden {
		t.Errorf("viewer liest sshd-Formular: %d", code)
	}
	if code, _ := do(t, c, "GET", ts.URL+"/ws/console/vm/web01", csrf, ""); code != http.StatusForbidden {
		t.Errorf("viewer Konsole: %d", code)
	}
	if code, _ := do(t, c, "GET", vm+"web01/detail", csrf, ""); code != 200 {
		t.Errorf("viewer Detailseite: %d", code)
	}
	if res, _ := c.Get(ts.URL + "/api/session"); res.StatusCode != 200 {
		t.Errorf("Sitzung: %d", res.StatusCode)
	}

	// Ereignisprotokoll: Aktion, Ablehnung und gefilterte Abfrage.
	setGrant(Grant{User: "demo", Role: "admin"})
	code, out := do(t, c, "GET", ts.URL+"/api/events?kind=verweigert", csrf, "")
	if code != 200 || !strings.Contains(out, "db01/stop") || strings.Contains(out, `"kind":"aktion"`) {
		t.Errorf("Ereignisse (verweigert): %d %s", code, out)
	}
	if _, out := do(t, c, "GET", ts.URL+"/api/events?q=vm+pause", csrf, ""); !strings.Contains(out, "vm pause web01") {
		t.Errorf("Ereignis der Aktion fehlt: %s", out)
	}
	// Formulare für Systemdateien: Administrator liest, Vorschau schreibt nichts, Speichern prüft den Aussperrschutz.
	if code, out := do(t, c, "GET", ts.URL+"/api/conf/ntp", csrf, ""); code != 200 || !strings.Contains(out, "pool.ntp.org") {
		t.Errorf("ntp lesen: %d %.100s", code, out)
	}
	if code, out := do(t, c, "PUT", ts.URL+"/api/conf/resolv?preview=1", csrf, `{"nameservers":["192.168.50.1"],"lookup":["file","bind"],"search":["ctw.lan"]}`); code != 200 || !strings.Contains(out, "nameserver 192.168.50.1") {
		t.Errorf("resolv Vorschau: %d %.200s", code, out)
	}
	if code, out := do(t, c, "PUT", ts.URL+"/api/conf/ssh", csrf, `{"allowUsers":["thorin"]}`); code != 400 || !strings.Contains(out, "aussperren") {
		t.Errorf("ssh Aussperrschutz (demo nicht in AllowUsers): %d %.200s", code, out)
	}
	if code, _ := do(t, c, "GET", ts.URL+"/api/conf/gibtsnicht", csrf, ""); code != 404 {
		t.Errorf("unbekannte Konfiguration: %d", code)
	}
	// Auftragsende und Alarm bei verweigertem Zugriff.
	if code, out := do(t, c, "POST", ts.URL+"/api/packages/update", csrf, `{"names":[]}`); code != 200 {
		t.Fatalf("Auftrag starten: %d %s", code, out)
	}
	var jobs string
	for i := 0; i < 40 && !strings.Contains(jobs, "beendet"); i++ {
		time.Sleep(250 * time.Millisecond)
		_, jobs = do(t, c, "GET", ts.URL+"/api/events?kind=auftrag", csrf, "")
	}
	if !strings.Contains(jobs, "beendet") {
		t.Errorf("Auftragsende fehlt im Protokoll: %s", jobs)
	}
	s.Metrics.Alerts.SetSettings([]string{"root"}, map[string]bool{"access_denied": true})
	setGrant(Grant{User: "demo", Role: "viewer"})
	do(t, c, "POST", vm+"web01/pause", csrf, "")
	_, hist := s.Metrics.Alerts.Snapshot()
	if len(hist) == 0 || hist[len(hist)-1].Rule != "access_denied" {
		t.Errorf("kein Alarm bei verweigertem Zugriff: %+v", hist)
	}
	setGrant(Grant{User: "demo", Role: "admin"})
	// Neustart des Servers behält das Protokoll.
	if n := len(NewEventLog(s.Host).Query("", "", "", "", 10)); n == 0 {
		t.Error("Protokoll nicht gespeichert")
	}
}
