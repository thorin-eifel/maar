package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/thorin-eifel/maar/internal/obsd"
)

// API-Token erlauben einem zentralen maard den lesenden Zugriff auf diesen Host
// (Zusammenfassung und Messwerte). Gespeichert wird nur der SHA-256-Wert.

const tokensPath = "/var/maar/tokens.json"

type apiToken struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Hash    string `json:"hash"`
	Created int64  `json:"created"`
}

type Tokens struct {
	mu   sync.Mutex
	host *obsd.Host
	list []apiToken
	used map[string]int64
}

func NewTokens(h *obsd.Host) *Tokens {
	t := &Tokens{host: h, list: []apiToken{}, used: map[string]int64{}}
	if b, err := os.ReadFile(h.P(tokensPath)); err == nil {
		json.Unmarshal(b, &t.list)
	}
	return t
}

func (t *Tokens) save() error {
	b, _ := json.MarshalIndent(t.list, "", "  ")
	p := t.host.P(tokensPath)
	os.MkdirAll(filepath.Dir(p), 0o700)
	if err := os.WriteFile(p+".tmp", b, 0o600); err != nil {
		return err
	}
	return os.Rename(p+".tmp", p)
}

func hashToken(tok string) string {
	h := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(h[:])
}

// Create liefert das Klartext-Token genau einmal.
func (t *Tokens) Create(name string) (apiToken, string, error) {
	if !reTokenName.MatchString(name) {
		return apiToken{}, "", fmt.Errorf("%w: Name des Tokens (Buchstaben, Ziffern, . _ -)", obsd.ErrInvalid)
	}
	var raw [32]byte
	rand.Read(raw[:])
	tok := "maar_" + base64.RawURLEncoding.EncodeToString(raw[:])
	var id [4]byte
	rand.Read(id[:])
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.list) >= 50 {
		return apiToken{}, "", fmt.Errorf("%w: höchstens 50 Token", obsd.ErrInvalid)
	}
	for _, x := range t.list {
		if x.Name == name {
			return apiToken{}, "", fmt.Errorf("%w: Token %q existiert schon", obsd.ErrInvalid, name)
		}
	}
	a := apiToken{ID: hex.EncodeToString(id[:]), Name: name, Hash: hashToken(tok), Created: time.Now().Unix()}
	t.list = append(t.list, a)
	return a, tok, t.save()
}

func (t *Tokens) Delete(id string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i, x := range t.list {
		if x.ID == id {
			t.list = append(t.list[:i], t.list[i+1:]...)
			return t.save()
		}
	}
	return fmt.Errorf("%w: Token %q gibt es nicht", obsd.ErrInvalid, id)
}

// Lookup findet das Token zum Klartext; leerer Name = unbekannt.
func (t *Tokens) Lookup(tok string) string {
	h := hashToken(tok)
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, x := range t.list {
		if x.Hash == h {
			t.used[x.ID] = time.Now().Unix()
			return x.Name
		}
	}
	return ""
}

// Erlaubte Pfade für Token: lesend, nur Föderation und Messwerte.
func tokenAllowed(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	switch r.URL.Path {
	case "/api/federation/summary", "/api/metrics", "/api/metrics/keys":
		return true
	}
	return false
}

func (s *Server) tokenList(w http.ResponseWriter, r *http.Request) {
	s.Tokens.mu.Lock()
	defer s.Tokens.mu.Unlock()
	out := []map[string]any{}
	for _, x := range s.Tokens.list {
		out = append(out, map[string]any{"id": x.ID, "name": x.Name, "created": x.Created, "lastUsed": s.Tokens.used[x.ID]})
	}
	writeJSON(w, out)
}

func (s *Server) tokenCreate(w http.ResponseWriter, r *http.Request) {
	var req struct{ Name string }
	if !decode(w, r, &req) {
		return
	}
	a, tok, err := s.Tokens.Create(req.Name)
	s.audit(r, err, "API-Token angelegt: %s", req.Name)
	respond(w, map[string]any{"id": a.ID, "name": a.Name, "token": tok}, err)
}

func (s *Server) tokenDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.Tokens.Delete(id)
	s.audit(r, err, "API-Token gelöscht: %s", id)
	respond(w, msg{Message: "Token gelöscht."}, err)
}
