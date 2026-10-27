package server

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"
	"github.com/thorin-eifel/maar/internal/auth"
	"github.com/thorin-eifel/maar/internal/console"
	"github.com/thorin-eifel/maar/internal/jobs"
	"github.com/thorin-eifel/maar/internal/metrics"
	"github.com/thorin-eifel/maar/internal/obsd"
	"github.com/thorin-eifel/maar/internal/sched"
)

func newTestServer(t *testing.T) (*httptest.Server, *http.Client) {
	t.Helper()
	_, ts, c := newTestServerS(t)
	return ts, c
}

func newTestServerS(t *testing.T) (*Server, *httptest.Server, *http.Client) {
	t.Helper()
	d, root, err := obsd.NewDemo()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	host, jm := obsd.NewHost(d, root, true), jobs.NewManager(d)
	s := &Server{
		Host: host, Auth: auth.DemoAuth{User: "demo", Pass: "demo"}, Sessions: auth.NewStore(),
		Jobs: jm, Hub: console.NewHub(), Sched: sched.New(host, jm, log.New(io.Discard, "", 0)), Metrics: metrics.NewCollector(host, log.New(io.Discard, "", 0)), Static: fstest.MapFS{"index.html": {Data: []byte("ok")}},
		Audit: log.New(io.Discard, "", 0),
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	jar, _ := cookiejar.New(nil)
	return s, ts, &http.Client{Jar: jar}
}

func login(t *testing.T, ts *httptest.Server, c *http.Client) string {
	t.Helper()
	res, err := c.Post(ts.URL+"/api/login", "application/json", strings.NewReader(`{"user":"demo","password":"demo"}`))
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("login: %v %v", err, res.Status)
	}
	var s struct{ CSRF string }
	json.NewDecoder(res.Body).Decode(&s)
	return s.CSRF
}

func TestAuthAndCSRF(t *testing.T) {
	ts, c := newTestServer(t)
	if res, _ := c.Get(ts.URL + "/api/overview"); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("ohne Anmeldung: %d", res.StatusCode)
	}
	res, _ := c.Post(ts.URL+"/api/login", "application/json", strings.NewReader(`{"user":"demo","password":"falsch"}`))
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("falsches Passwort: %d", res.StatusCode)
	}
	csrf := login(t, ts, c)
	if res, _ := c.Get(ts.URL + "/api/overview"); res.StatusCode != 200 {
		t.Errorf("Übersicht: %d", res.StatusCode)
	}
	req, _ := http.NewRequest("POST", ts.URL+"/api/vms/web01/pause", nil)
	if res, _ := c.Do(req); res.StatusCode != http.StatusForbidden {
		t.Errorf("ohne CSRF: %d", res.StatusCode)
	}
	req.Header.Set("X-CSRF-Token", csrf)
	if res, _ := c.Do(req); res.StatusCode != 200 {
		t.Errorf("mit CSRF: %d", res.StatusCode)
	}
	// Ungültige Eingaben ergeben 400, nicht 500.
	req, _ = http.NewRequest("POST", ts.URL+"/api/services/a%3Bb/start", nil)
	req.Header.Set("X-CSRF-Token", csrf)
	if res, _ := c.Do(req); res.StatusCode != http.StatusBadRequest {
		t.Errorf("ungültiger Dienstname: %d", res.StatusCode)
	}
}

func TestSharedVMConsole(t *testing.T) {
	ts, c := newTestServer(t)
	login(t, ts, c)
	u, _ := http.NewRequest("GET", ts.URL, nil)
	cookies := c.Jar.Cookies(u.URL)
	hdr := http.Header{}
	for _, ck := range cookies {
		hdr.Add("Cookie", ck.Name+"="+ck.Value)
	}
	dial := func(origin string) (*websocket.Conn, error) {
		h := hdr.Clone()
		h.Set("Origin", origin)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		conn, _, err := websocket.Dial(ctx, strings.Replace(ts.URL, "http", "ws", 1)+"/ws/console/vm/web01", &websocket.DialOptions{HTTPHeader: h})
		return conn, err
	}
	if _, err := dial("https://evil.example"); err == nil {
		t.Error("fremder Origin wurde akzeptiert")
	}
	a, err := dial(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer a.CloseNow()
	readUntil := func(conn *websocket.Conn, want string) bool {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		var buf strings.Builder
		for {
			_, b, err := conn.Read(ctx)
			if err != nil {
				return false
			}
			buf.Write(b)
			if strings.Contains(buf.String(), want) {
				return true
			}
		}
	}
	if !readUntil(a, "login: ") {
		t.Fatal("kein Login-Prompt von der Demo-VM")
	}
	// Zweiter Zuschauer sieht den Verlauf und die Eingaben des ersten.
	b, err := dial(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer b.CloseNow()
	if !readUntil(b, "login: ") {
		t.Fatal("zweiter Zuschauer bekam keinen Verlauf")
	}
	a.Write(context.Background(), websocket.MessageBinary, []byte("root\r"))
	if !readUntil(b, "Password:") {
		t.Error("Eingabe von A erschien nicht bei B")
	}
}
