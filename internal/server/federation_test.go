package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

type hostsResp struct {
	Hosts []HostView `json:"hosts"`
}

func getHosts(t *testing.T, c *http.Client, base, csrf string) map[string]HostView {
	t.Helper()
	code, out := do(t, c, "GET", base+"/api/hosts", csrf, "")
	if code != 200 {
		t.Fatalf("hosts: %d %s", code, out)
	}
	var r hostsResp
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatal(err)
	}
	m := map[string]HostView{}
	for _, h := range r.Hosts {
		m[h.ID] = h
	}
	return m
}

func TestFederation(t *testing.T) {
	_, remote, rc := newTestServerS(t)
	rcsrf := login(t, remote, rc)
	central, cts, cc := newTestServerS(t)
	ccsrf := login(t, cts, cc)

	// Token auf dem Remote-Host anlegen; Klartext gibt es nur einmal.
	code, out := do(t, rc, "POST", remote.URL+"/api/tokens", rcsrf, `{"name":"zentrale"}`)
	if code != 200 {
		t.Fatalf("Token anlegen: %d %s", code, out)
	}
	var tk struct{ Token string }
	json.Unmarshal([]byte(out), &tk)
	if !strings.HasPrefix(tk.Token, "maar_") {
		t.Fatalf("Token: %q", tk.Token)
	}
	if _, out := do(t, rc, "GET", remote.URL+"/api/tokens", rcsrf, ""); strings.Contains(out, tk.Token) || strings.Contains(out, "hash") {
		t.Errorf("Token-Liste verrät Geheimnis: %s", out)
	}

	// Token dürfen nur die Zusammenfassung und Messwerte lesen.
	bearer := func(method, path, token string) int {
		req, _ := http.NewRequest(method, remote.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	for _, c := range []struct {
		method, path, token string
		want                int
	}{
		{"GET", "/api/federation/summary", tk.Token, 200},
		{"GET", "/api/metrics?keys=host.cpu", tk.Token, 200},
		{"GET", "/api/vms", tk.Token, 403},
		{"GET", "/api/file?path=/etc/pf.conf", tk.Token, 403},
		{"POST", "/api/vms/web01/stop", tk.Token, 403},
		{"GET", "/api/federation/summary", "maar_falsch", 401},
	} {
		if got := bearer(c.method, c.path, c.token); got != c.want {
			t.Errorf("%s %s: %d, erwartet %d", c.method, c.path, got, c.want)
		}
	}

	// Peers hinzufügen: ungültige Eingaben, falsches Token, dann richtig.
	add := func(body string) (int, string) { return do(t, cc, "POST", cts.URL+"/api/peers", ccsrf, body) }
	for _, bad := range []string{
		`{"name":"x","url":"http://example.com","token":"t"}`,               // http nicht auf Loopback
		`{"name":"x","url":"https://user:pw@example.com","token":"t"}`,      // Zugangsdaten in der URL
		`{"name":"x","url":"https://example.com","token":"t"}`,              // Fingerabdruck fehlt
		`{"name":"x","url":"file:///etc/passwd","token":"t"}`,               // anderes Schema
		`{"name":"","url":"` + remote.URL + `","token":"` + tk.Token + `"}`, // kein Name
		`{"name":"fern","url":"` + remote.URL + `","token":"maar_falsch"}`,  // falsches Token
	} {
		if code, out := add(bad); code != 400 {
			t.Errorf("akzeptiert: %s → %d %s", bad, code, out)
		}
	}
	if code, out := add(`{"name":"fern","url":"` + remote.URL + `","token":"` + tk.Token + `"}`); code != 200 {
		t.Fatalf("Peer hinzufügen: %d %s", code, out)
	}
	if code, _ := add(`{"name":"fern2","url":"` + remote.URL + `","token":"` + tk.Token + `"}`); code != 400 {
		t.Error("doppelte URL akzeptiert")
	}

	hosts := getHosts(t, cc, cts.URL, ccsrf)
	if h := hosts["fern"]; h.Status != "ok" || h.Summary == nil || h.Summary.Hostname == "" || len(h.Summary.VMs) < 3 {
		t.Errorf("Remote-Host: %+v", h)
	}
	if hosts["local"].Summary == nil || !hosts["local"].Local {
		t.Errorf("lokaler Host fehlt: %+v", hosts["local"])
	}
	if h := hosts["kuestenwache"]; h.Status != "ok" || h.Summary.NCPU != 8 {
		t.Errorf("Demo-Peer: %+v", h)
	}
	if h := hosts["wetterwarte"]; h.Status != "offline" {
		t.Errorf("Demo-Peer offline: %+v", h)
	}
	if strings.Contains(func() string { _, o := do(t, cc, "GET", cts.URL+"/api/hosts", ccsrf, ""); return o }(), tk.Token) {
		t.Error("Hostliste enthält das Token")
	}

	// Messwerte des Peers durch die Zentrale.
	code, out = do(t, cc, "GET", cts.URL+"/api/hosts/fern/metrics?range=1h&keys=host.cpu", ccsrf, "")
	if code != 200 || !strings.Contains(out, `"series"`) {
		t.Errorf("Peer-Messwerte: %d %.100s", code, out)
	}
	if code, _ := do(t, cc, "GET", cts.URL+"/api/hosts/gibtsnicht/metrics", ccsrf, ""); code != 404 {
		t.Errorf("unbekannter Host: %d", code)
	}

	// Token widerrufen: der Peer meldet „auth“ (Cache der Zentrale umgehen).
	var id struct{ ID string }
	_, out = do(t, rc, "GET", remote.URL+"/api/tokens", rcsrf, "")
	var list []struct{ ID string }
	json.Unmarshal([]byte(out), &list)
	id.ID = list[0].ID
	if code, _ := do(t, rc, "DELETE", remote.URL+"/api/tokens/"+id.ID, rcsrf, ""); code != 200 {
		t.Fatalf("Token löschen: %d", code)
	}
	central.Peers.mu.Lock()
	central.Peers.cache = map[string]cached{}
	central.Peers.mu.Unlock()
	if h := getHosts(t, cc, cts.URL, ccsrf)["fern"]; h.Status != "auth" {
		t.Errorf("nach Widerruf: %+v", h)
	}

	// Entfernen; Betrachter dürfen keine Peers verwalten.
	central.Access.mu.Lock()
	central.Access.cfg.Users = []Grant{{User: "demo", Role: "viewer"}}
	central.Access.mu.Unlock()
	if code, _ := do(t, cc, "DELETE", cts.URL+"/api/peers/fern", ccsrf, ""); code != 403 {
		t.Errorf("viewer entfernt Peer: %d", code)
	}
	if code, _ := do(t, cc, "GET", cts.URL+"/api/tokens", ccsrf, ""); code != 403 {
		t.Errorf("viewer liest Token: %d", code)
	}
	central.Access.mu.Lock()
	central.Access.cfg.Users = nil
	central.Access.mu.Unlock()
	if code, _ := do(t, cc, "DELETE", cts.URL+"/api/peers/fern", ccsrf, ""); code != 200 {
		t.Errorf("Peer entfernen: %d", code)
	}
}
