package server

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/thorin-eifel/maar/internal/metrics"
	"github.com/thorin-eifel/maar/internal/obsd"
)

// Mehrere Hosts: Jeder Host betreibt sein eigenes maard. Dieser maard fragt die
// Peers lesend über API-Token ab (Zusammenfassung, Messwerte) und bündelt sie.
// Verwaltet wird jeder Host weiterhin lokal über seine eigene Oberfläche.

const peersPath = "/var/maar/peers.json"

var (
	reTokenName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,40}$`)
	reFinger    = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type Peer struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	Token       string `json:"token"`
	Fingerprint string `json:"fingerprint,omitempty"` // SHA-256 des Serverzertifikats (https)
}

type Peers struct {
	mu    sync.Mutex
	host  *obsd.Host
	list  []Peer
	cache map[string]cached
}

type cached struct {
	at   time.Time
	view HostView
}

func NewPeers(h *obsd.Host) *Peers {
	p := &Peers{host: h, list: []Peer{}, cache: map[string]cached{}}
	if b, err := os.ReadFile(h.P(peersPath)); err == nil {
		json.Unmarshal(b, &p.list)
	}
	return p
}

func (p *Peers) save() error {
	b, _ := json.MarshalIndent(p.list, "", "  ")
	f := p.host.P(peersPath)
	os.MkdirAll(filepath.Dir(f), 0o700)
	if err := os.WriteFile(f+".tmp", b, 0o600); err != nil {
		return err
	}
	return os.Rename(f+".tmp", f)
}

func (p *Peers) all() []Peer {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Peer{}, p.list...)
}

func (p *Peers) get(id string) (Peer, bool) {
	for _, x := range p.all() {
		if x.ID == id {
			return x, true
		}
	}
	return Peer{}, false
}

// ---- Zusammenfassung dieses Hosts ----

type SummaryVM struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	VCPUs  int    `json:"vcpus"`
	MaxMem string `json:"maxmem"`
}

type SummaryAlert struct {
	Name     string `json:"name"`
	Subject  string `json:"subject"`
	Severity string `json:"severity"`
	Since    int64  `json:"since"`
}

type Summary struct {
	Time      int64          `json:"time"`
	Hostname  string         `json:"hostname"`
	Version   string         `json:"version"`
	OS        string         `json:"os"`
	Release   string         `json:"release"`
	Machine   string         `json:"machine"`
	NCPU      int            `json:"ncpu"`
	Uptime    int64          `json:"uptime"`
	CPU       float64        `json:"cpu"`
	MemTotal  int64          `json:"memTotal"`
	MemActive int64          `json:"memActive"`
	VMs       []SummaryVM    `json:"vms"`
	Alerts    []SummaryAlert `json:"alerts"`
	Failed    []string       `json:"failed"`
	Events    []AuditEvent   `json:"events"`
}

func (s *Server) localSummary(ctx context.Context) (*Summary, error) {
	o, err := s.Host.Overview(ctx)
	if err != nil {
		return nil, err
	}
	sum := &Summary{Time: time.Now().Unix(), Hostname: o.Hostname, Version: s.Version, OS: o.OS, Release: o.Release, Machine: o.Machine,
		NCPU: o.NCPU, Uptime: o.Uptime, CPU: o.CPUBusy, MemTotal: o.MemTotal, MemActive: o.MemActive,
		VMs: []SummaryVM{}, Alerts: []SummaryAlert{}, Failed: []string{}}
	if ov, err := s.Host.VMs(ctx); err == nil {
		for _, v := range ov.VMs {
			sum.VMs = append(sum.VMs, SummaryVM{Name: v.Name, State: v.State, VCPUs: v.VCPUs, MaxMem: v.MaxMem})
		}
	}
	if svc, err := s.Host.Services(ctx); err == nil {
		for _, x := range svc {
			if x.Failed {
				sum.Failed = append(sum.Failed, x.Name)
			}
		}
	}
	if s.Metrics != nil {
		act, _ := s.Metrics.Alerts.Snapshot()
		for _, a := range act {
			sum.Alerts = append(sum.Alerts, SummaryAlert{Name: a.Name, Subject: a.Subject, Severity: a.Severity, Since: a.Since})
		}
	}
	sum.Events = s.Events.Query("", "", "", "", 30)
	return sum, nil
}

func (s *Server) federationSummary(w http.ResponseWriter, r *http.Request) {
	sum, err := s.localSummary(r.Context())
	respond(w, sum, err)
}

// ---- Abruf bei Peers ----

type HostView struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Local   bool     `json:"local"`
	URL     string   `json:"url,omitempty"`
	Status  string   `json:"status"` // ok, offline, auth, cert
	Error   string   `json:"error,omitempty"`
	Summary *Summary `json:"summary,omitempty"`
}

type peerError struct{ status, msg string }

func (e *peerError) Error() string { return e.msg }

func (s *Server) client(p Peer) *http.Client {
	tr := &http.Transport{
		DialContext: (&net.Dialer{Timeout: 4 * time.Second}).DialContext,
		TLSClientConfig: &tls.Config{
			// Statt der Zertifikatskette wird der gespeicherte Fingerabdruck geprüft (selbst signierte Zertifikate).
			InsecureSkipVerify: true,
			VerifyConnection: func(cs tls.ConnectionState) error {
				if len(cs.PeerCertificates) == 0 {
					return errors.New("kein Zertifikat")
				}
				sum := sha256.Sum256(cs.PeerCertificates[0].Raw)
				if hex.EncodeToString(sum[:]) != p.Fingerprint {
					return errors.New("Zertifikat stimmt nicht mit dem gespeicherten Fingerabdruck überein")
				}
				return nil
			},
		},
		DisableKeepAlives: true,
	}
	return &http.Client{Transport: tr, Timeout: 6 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (s *Server) peerGet(ctx context.Context, p Peer, path string) ([]byte, error) {
	if s.Host.Demo && strings.HasPrefix(p.URL, "demo://") {
		return demoPeerGet(p, path)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(p.URL, "/")+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.Token)
	res, err := s.client(p).Do(req)
	if err != nil {
		st := "offline"
		if strings.Contains(err.Error(), "Fingerabdruck") {
			st = "cert"
		}
		return nil, &peerError{st, err.Error()}
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	switch {
	case res.StatusCode == 401 || res.StatusCode == 403:
		return nil, &peerError{"auth", "Token wurde abgelehnt – neu anlegen oder prüfen"}
	case res.StatusCode != 200:
		return nil, &peerError{"offline", fmt.Sprintf("HTTP %d", res.StatusCode)}
	}
	return body, nil
}

func (s *Server) fetchPeer(ctx context.Context, p Peer) HostView {
	v := HostView{ID: p.ID, Name: p.Name, URL: p.URL}
	if strings.HasPrefix(p.URL, "demo://") {
		v.URL = ""
	}
	b, err := s.peerGet(ctx, p, "/api/federation/summary")
	if err != nil {
		var pe *peerError
		v.Status, v.Error = "offline", err.Error()
		if errors.As(err, &pe) {
			v.Status = pe.status
		}
		return v
	}
	var sum Summary
	if err := json.Unmarshal(b, &sum); err != nil {
		v.Status, v.Error = "offline", "unerwartete Antwort"
		return v
	}
	v.Status, v.Summary = "ok", &sum
	return v
}

func (s *Server) hosts(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	peers := s.Peers.all()
	views := make([]HostView, len(peers))
	var wg sync.WaitGroup
	for i, p := range peers {
		s.Peers.mu.Lock()
		c, ok := s.Peers.cache[p.ID]
		s.Peers.mu.Unlock()
		if ok && time.Since(c.at) < 10*time.Second {
			views[i] = c.view
			continue
		}
		wg.Add(1)
		go func(i int, p Peer) {
			defer wg.Done()
			views[i] = s.fetchPeer(ctx, p)
			s.Peers.mu.Lock()
			s.Peers.cache[p.ID] = cached{time.Now(), views[i]}
			s.Peers.mu.Unlock()
		}(i, p)
	}
	local := HostView{ID: "local", Local: true, Status: "ok"}
	sum, err := s.localSummary(r.Context())
	if err != nil {
		local.Status, local.Error = "offline", err.Error()
	} else {
		local.Summary, local.Name = sum, sum.Hostname
	}
	wg.Wait()
	writeJSON(w, map[string]any{"hosts": append([]HostView{local}, views...), "now": time.Now().Unix()})
}

func (s *Server) hostMetrics(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "local" {
		s.metricsQuery(w, r)
		return
	}
	p, ok := s.Peers.get(id)
	if !ok {
		jsonErr(w, http.StatusNotFound, "Host unbekannt")
		return
	}
	q := url.Values{"range": {r.URL.Query().Get("range")}, "keys": {r.URL.Query().Get("keys")}}
	b, err := s.peerGet(r.Context(), p, "/api/metrics?"+q.Encode())
	if err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Write(b)
}

// ---- Peers verwalten (nur Administratoren, durch die Rollenprüfung für nicht-GET) ----

func (s *Server) allowedURL(raw string) (*url.URL, error) {
	if s.Host.Demo && strings.HasPrefix(raw, "demo://") {
		return url.Parse(raw)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
		return nil, fmt.Errorf("%w: Adresse %q (erwartet: https://host:9443)", obsd.ErrInvalid, raw)
	}
	loop := false
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		loop = ip.IsLoopback()
	} else {
		loop = u.Hostname() == "localhost"
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && loop) {
		return nil, fmt.Errorf("%w: Peers nur über https (http nur auf Loopback)", obsd.ErrInvalid)
	}
	return u, nil
}

// peerProbe liest den Zertifikat-Fingerabdruck eines Hosts (TOFU: der Administrator vergleicht ihn).
func (s *Server) peerProbe(w http.ResponseWriter, r *http.Request) {
	var req struct{ URL string }
	if !decode(w, r, &req) {
		return
	}
	u, err := s.allowedURL(req.URL)
	if err != nil {
		fail(w, err)
		return
	}
	if u.Scheme != "https" {
		writeJSON(w, map[string]string{"fingerprint": ""})
		return
	}
	host := u.Host
	if u.Port() == "" {
		host = net.JoinHostPort(u.Hostname(), "443")
	}
	d := &net.Dialer{Timeout: 4 * time.Second}
	conn, err := tls.DialWithDialer(d, "tcp", host, &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		jsonErr(w, http.StatusBadGateway, "Keine TLS-Verbindung: "+err.Error())
		return
	}
	defer conn.Close()
	cs := conn.ConnectionState()
	if len(cs.PeerCertificates) == 0 {
		jsonErr(w, http.StatusBadGateway, "Der Host liefert kein Zertifikat")
		return
	}
	sum := sha256.Sum256(cs.PeerCertificates[0].Raw)
	writeJSON(w, map[string]string{"fingerprint": hex.EncodeToString(sum[:]), "subject": cs.PeerCertificates[0].Subject.String()})
}

func (s *Server) peerAdd(w http.ResponseWriter, r *http.Request) {
	var req Peer
	if !decode(w, r, &req) {
		return
	}
	err := s.addPeer(r.Context(), &req)
	s.audit(r, err, "Peer hinzugefügt: %s (%s)", req.Name, req.URL)
	respond(w, msg{Message: "Host " + req.Name + " verbunden."}, err)
}

func (s *Server) addPeer(ctx context.Context, p *Peer) error {
	p.Name = strings.TrimSpace(p.Name)
	if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._ -]{0,40}$`).MatchString(p.Name) {
		return fmt.Errorf("%w: Name des Hosts", obsd.ErrInvalid)
	}
	u, err := s.allowedURL(p.URL)
	if err != nil {
		return err
	}
	p.URL = strings.TrimRight(u.String(), "/")
	if u.Scheme == "https" && !reFinger.MatchString(strings.ToLower(p.Fingerprint)) {
		return fmt.Errorf("%w: Zertifikat-Fingerabdruck fehlt – zuerst „Fingerabdruck holen“", obsd.ErrInvalid)
	}
	p.Fingerprint = strings.ToLower(p.Fingerprint)
	if p.Token == "" || len(p.Token) > 200 || strings.ContainsAny(p.Token, " \r\n") {
		return fmt.Errorf("%w: API-Token", obsd.ErrInvalid)
	}
	// Verbindung und Token gleich prüfen, damit Tippfehler sofort auffallen.
	tctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	if _, err := s.peerGet(tctx, *p, "/api/federation/summary"); err != nil {
		return fmt.Errorf("%w: %v", obsd.ErrInvalid, err)
	}
	s.Peers.mu.Lock()
	defer s.Peers.mu.Unlock()
	if len(s.Peers.list) >= 100 {
		return fmt.Errorf("%w: höchstens 100 Hosts", obsd.ErrInvalid)
	}
	base := hostSlug(p.Name)
	id, n := base, 1
	for {
		clash := id == "local"
		for _, x := range s.Peers.list {
			clash = clash || x.ID == id || x.URL == p.URL || x.Name == p.Name
		}
		if !clash {
			break
		}
		for _, x := range s.Peers.list {
			if x.URL == p.URL || x.Name == p.Name {
				return fmt.Errorf("%w: Host %q ist schon verbunden", obsd.ErrInvalid, x.Name)
			}
		}
		n++
		id = fmt.Sprintf("%s-%d", base, n)
	}
	p.ID = id
	s.Peers.list = append(s.Peers.list, *p)
	sort.Slice(s.Peers.list, func(i, j int) bool { return s.Peers.list[i].Name < s.Peers.list[j].Name })
	return s.Peers.save()
}

func hostSlug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteByte('-')
		}
	}
	if out := strings.Trim(b.String(), "-"); out != "" {
		return out
	}
	return "host"
}

func (s *Server) peerDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.Peers.mu.Lock()
	found := false
	for i, x := range s.Peers.list {
		if x.ID == id {
			s.Peers.list = append(s.Peers.list[:i], s.Peers.list[i+1:]...)
			delete(s.Peers.cache, id)
			found = true
			break
		}
	}
	var err error
	if found {
		err = s.Peers.save()
	} else {
		err = fmt.Errorf("%w: Host %q gibt es nicht", obsd.ErrInvalid, id)
	}
	s.Peers.mu.Unlock()
	s.audit(r, err, "Peer entfernt: %s", id)
	respond(w, msg{Message: "Host entfernt."}, err)
}

// ---- Demo-Peers (ohne Netzwerk) ----

var (
	demoStoreOnce sync.Once
	demoStore     *metrics.Store
)

func demoPeerGet(p Peer, path string) ([]byte, error) {
	if p.URL == "demo://wetterwarte" {
		return nil, &peerError{"offline", "Verbindung abgelehnt (Demo: Host ist ausgeschaltet)"}
	}
	if p.Token != "demo" {
		return nil, &peerError{"auth", "Token wurde abgelehnt – neu anlegen oder prüfen"}
	}
	now := time.Now()
	switch {
	case path == "/api/federation/summary":
		t := float64(now.Unix())
		sum := Summary{Time: now.Unix(), Hostname: "kuestenwache.ctw.lan", Version: "1.0.0", OS: "OpenBSD", Release: "8.0", Machine: "arm64", NCPU: 8,
			Uptime: 3*86400 + 7200, CPU: math.Round((38+14*math.Sin(t/45))*10) / 10, MemTotal: 32 << 30, MemActive: int64(11.5 * (1 << 30)),
			VMs:    []SummaryVM{{"gitea", "running", 2, "4.0G"}, {"runner01", "running", 4, "8.0G"}, {"runner02", "stopped", 4, "8.0G"}, {"staging", "running", 2, "2.0G"}},
			Alerts: []SummaryAlert{{Name: "VM dauerhaft unter Volllast", Subject: "vm.runner01.cpu", Severity: "warning", Since: now.Add(-40 * time.Minute).Unix()}},
			Failed: []string{},
			Events: []AuditEvent{
				{Time: now.Add(-9 * time.Minute).Unix(), User: "anna", Addr: "192.168.50.31", Kind: "aktion", Result: "ok", Text: "vm start runner01"},
				{Time: now.Add(-2 * time.Hour).Unix(), User: "thorin", Addr: "192.168.50.20", Kind: "auftrag", Result: "fehler", Text: "Auftrag j7 beendet: Sicherung nachts", Error: "sftp: Verbindung abgelehnt"},
				{Time: now.Add(-5 * time.Hour).Unix(), User: "anna", Addr: "192.168.50.31", Kind: "anmeldung", Result: "ok", Text: "Anmeldung"},
			}}
		return json.Marshal(sum)
	case strings.HasPrefix(path, "/api/metrics?"):
		demoStoreOnce.Do(func() { demoStore = metrics.NewStore(); metrics.Backfill(demoStore, time.Now()) })
		q, _ := url.ParseQuery(strings.TrimPrefix(path, "/api/metrics?"))
		return json.Marshal(queryMetrics(demoStore, q))
	}
	return nil, &peerError{"offline", "HTTP 404"}
}
