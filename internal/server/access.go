package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/thorin-eifel/maar/internal/obsd"
)

// Rollen: admin darf alles, operator bedient seine VMs (Start/Stopp, Konsole,
// Snapshots anlegen und zurücksetzen), viewer liest nur. Konten ohne Eintrag
// erhalten die Standardrolle (anfangs admin – wer sich anmelden darf, ist in wheel).

const accessPath = "/var/maar/access.json"

var roles = []string{"admin", "operator", "viewer"}

type Grant struct {
	User string   `json:"user"`
	Role string   `json:"role"`
	VMs  []string `json:"vms"` // leer = alle VMs; sonst nur diese (operator, viewer)
}

type AccessConfig struct {
	Default string  `json:"default"`
	Users   []Grant `json:"users"`
}

type Access struct {
	mu   sync.Mutex
	host *obsd.Host
	cfg  AccessConfig
}

var reGrantUser = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,30}$`)

func NewAccess(h *obsd.Host) *Access {
	a := &Access{host: h, cfg: AccessConfig{Default: "admin", Users: []Grant{}}}
	if b, err := os.ReadFile(h.P(accessPath)); err == nil {
		var c AccessConfig
		if json.Unmarshal(b, &c) == nil && validRole(c.Default) {
			if c.Users == nil {
				c.Users = []Grant{}
			}
			a.cfg = c
		}
	}
	return a
}

func validRole(r string) bool {
	for _, x := range roles {
		if x == r {
			return true
		}
	}
	return false
}

func (a *Access) Config() AccessConfig {
	a.mu.Lock()
	defer a.mu.Unlock()
	c := a.cfg
	c.Users = append([]Grant{}, c.Users...)
	return c
}

// For liefert Rolle und VM-Eingrenzung eines Kontos.
func (a *Access) For(user string) Grant {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, g := range a.cfg.Users {
		if g.User == user {
			return g
		}
	}
	return Grant{User: user, Role: a.cfg.Default}
}

// Set ersetzt die Konfiguration. actor darf sich nicht selbst aussperren.
func (a *Access) Set(c AccessConfig, actor string, vms []string) error {
	if !validRole(c.Default) {
		return fmt.Errorf("%w: Standardrolle %q", obsd.ErrInvalid, c.Default)
	}
	known := map[string]bool{}
	for _, v := range vms {
		known[v] = true
	}
	seen := map[string]bool{}
	out := []Grant{}
	for _, g := range c.Users {
		if !reGrantUser.MatchString(g.User) || !validRole(g.Role) || seen[g.User] {
			return fmt.Errorf("%w: Eintrag für %q", obsd.ErrInvalid, g.User)
		}
		seen[g.User] = true
		for _, v := range g.VMs {
			if !known[v] {
				return fmt.Errorf("%w: VM %q gibt es nicht", obsd.ErrInvalid, v)
			}
		}
		if g.Role == "admin" {
			g.VMs = nil
		}
		sort.Strings(g.VMs)
		if g.VMs == nil {
			g.VMs = []string{}
		}
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].User < out[j].User })
	c.Users = out
	role := c.Default
	for _, g := range out {
		if g.User == actor {
			role = g.Role
		}
	}
	if role != "admin" {
		return fmt.Errorf("%w: Du würdest dir selbst die Administratorrolle nehmen", obsd.ErrInvalid)
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	p := a.host.P(accessPath)
	os.MkdirAll(filepath.Dir(p), 0o700)
	if err := os.WriteFile(p+".tmp", b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(p+".tmp", p); err != nil {
		return err
	}
	a.mu.Lock()
	a.cfg = c
	a.mu.Unlock()
	return nil
}

var (
	operatorActions = map[string]bool{"start": true, "stop": true, "pause": true, "unpause": true}
	// Für Betrachter und Operatoren gesperrte Lesezugriffe (Konfigurationsdateien, Schlüssel).
	readDenied   = []string{"/api/file", "/api/backup/pubkey", "/api/access", "/api/tokens", "/ws/console/shell"}
	viewerDenied = []string{"/ws/console/"} // Konsolen sind interaktiv
	operatorOK   = map[string]bool{
		"POST /api/vms/{name}/snapshots":             true,
		"POST /api/vms/{name}/snapshots/{id}/revert": true,
	}
)

// allowed entscheidet, ob die Rolle diese Anfrage ausführen darf.
func (g Grant) allowed(r *http.Request) (bool, string) {
	if g.Role == "admin" {
		return true, ""
	}
	path := r.URL.Path
	for _, p := range readDenied {
		if path == p || strings.HasPrefix(path, p+"/") || (p == "/api/file" && path == "/api/files") {
			return false, "nur für Administratoren"
		}
	}
	if g.Role == "viewer" {
		for _, p := range viewerDenied {
			if strings.HasPrefix(path, p) {
				return false, "Konsolen sind interaktiv – nicht für Betrachter"
			}
		}
	}
	if name := r.PathValue("name"); name != "" && (strings.HasPrefix(path, "/api/vms/") || strings.HasPrefix(path, "/ws/console/vm/")) {
		if len(g.VMs) > 0 && !contains(g.VMs, name) {
			return false, fmt.Sprintf("VM %s gehört nicht zu deinem Zugriff", name)
		}
	}
	if r.Method == http.MethodGet {
		return true, ""
	}
	if g.Role == "operator" {
		if r.Pattern == "POST /api/vms/{name}/{action}" && operatorActions[r.PathValue("action")] {
			return true, ""
		}
		if operatorOK[r.Pattern] {
			return true, ""
		}
	}
	return false, "die Rolle " + g.Role + " darf nur lesen" + map[bool]string{true: " und ihre VMs bedienen", false: ""}[g.Role == "operator"]
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// ---- Handler ----

func (s *Server) accessGet(w http.ResponseWriter, r *http.Request) {
	ov, err := s.Host.VMs(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	vms := []string{}
	for _, v := range ov.VMs {
		vms = append(vms, v.Name)
	}
	members := []string{}
	if gs, err := s.Host.Groups(); err == nil {
		for _, g := range gs {
			if g.Name == s.AdminGroup {
				members = g.Members
			}
		}
	}
	writeJSON(w, map[string]any{"config": s.Access.Config(), "members": members, "vms": vms, "roles": roles, "group": s.AdminGroup})
}

func (s *Server) accessPut(w http.ResponseWriter, r *http.Request) {
	var c AccessConfig
	if !decode(w, r, &c) {
		return
	}
	ov, err := s.Host.VMs(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	var vms []string
	for _, v := range ov.VMs {
		vms = append(vms, v.Name)
	}
	err = s.Access.Set(c, sessionFrom(r).User, vms)
	s.audit(r, err, "Zugriff geändert: Standard %s, %d Einträge", c.Default, len(c.Users))
	respond(w, msg{Message: "Zugriffsrechte gespeichert."}, err)
}

// scope liefert die VM-Eingrenzung des angemeldeten Kontos (nil = uneingeschränkt).
func (s *Server) scope(r *http.Request) []string {
	g := s.Access.For(sessionFrom(r).User)
	if g.Role == "admin" || len(g.VMs) == 0 {
		return nil
	}
	return g.VMs
}
