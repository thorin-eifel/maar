// Package auth prüft Anmeldungen über BSD Auth und verwaltet Sitzungen.
package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sync"
	"syscall"
	"time"
)

// Verifier prüft Benutzername und Passwort.
type Verifier interface {
	Verify(ctx context.Context, user, pass string) error
}

var ErrDenied = errors.New("Anmeldung abgelehnt")

var reUser = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,30}$`)

// BSDAuth nutzt das login_passwd-Skript im "response"-Modus. Challenge und
// Antwort werden über den Rückkanal (fd 3) übergeben, das Ergebnis kommt
// als "authorize" bzw. "reject" zurück – wie bei auth_userokay(3).
type BSDAuth struct {
	Style string // Standard: passwd
}

func (b BSDAuth) Verify(ctx context.Context, user, pass string) error {
	if !reUser.MatchString(user) || len(pass) == 0 || len(pass) > 1024 {
		return ErrDenied
	}
	style := b.Style
	if style == "" {
		style = "passwd"
	}
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return err
	}
	parent := os.NewFile(uintptr(fds[0]), "auth-parent")
	child := os.NewFile(uintptr(fds[1]), "auth-child")
	defer parent.Close()

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/libexec/auth/login_"+style, "-s", "response", "--", user)
	cmd.ExtraFiles = []*os.File{child} // wird im Kind zu fd 3
	cmd.Env = []string{"PATH=/bin:/usr/bin:/sbin:/usr/sbin"}
	if err := cmd.Start(); err != nil {
		child.Close()
		return err
	}
	child.Close()
	var msg bytes.Buffer
	msg.WriteString("\x00") // leere Challenge
	msg.WriteString(pass)
	msg.WriteByte(0)
	parent.Write(msg.Bytes())
	reply, _ := io.ReadAll(io.LimitReader(parent, 4096))
	waitErr := cmd.Wait()
	if waitErr != nil {
		return ErrDenied
	}
	for _, l := range bytes.Split(reply, []byte("\n")) {
		if bytes.HasPrefix(bytes.TrimSpace(l), []byte("reject")) {
			return ErrDenied
		}
		if bytes.HasPrefix(bytes.TrimSpace(l), []byte("authorize")) {
			return nil
		}
	}
	return ErrDenied
}

// DemoAuth akzeptiert ein festes Konto für den Demo-Modus.
type DemoAuth struct{ User, Pass string }

func (d DemoAuth) Verify(_ context.Context, user, pass string) error {
	if subtle.ConstantTimeCompare([]byte(user), []byte(d.User)) == 1 &&
		subtle.ConstantTimeCompare([]byte(pass), []byte(d.Pass)) == 1 {
		return nil
	}
	return ErrDenied
}

// ---- Sitzungen ----

type Session struct {
	ID       string
	User     string
	CSRF     string
	Created  time.Time
	LastSeen time.Time
	Addr     string
}

type Store struct {
	mu       sync.Mutex
	sessions map[string]*Session
	Idle     time.Duration
	Max      time.Duration
	fails    map[string][]time.Time
}

func NewStore() *Store {
	s := &Store{sessions: map[string]*Session{}, fails: map[string][]time.Time{}, Idle: 30 * time.Minute, Max: 12 * time.Hour}
	go s.gc()
	return s
}

func token() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func (s *Store) Create(user, addr string) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	sess := &Session{ID: token(), User: user, CSRF: token(), Created: now, LastSeen: now, Addr: addr}
	s.sessions[sess.ID] = sess
	return sess
}

func (s *Store) Get(id string) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.sessions[id]
	if sess == nil {
		return nil
	}
	now := time.Now()
	if now.Sub(sess.LastSeen) > s.Idle || now.Sub(sess.Created) > s.Max {
		delete(s.sessions, id)
		return nil
	}
	sess.LastSeen = now
	return sess
}

func (s *Store) Delete(id string) {
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
}

// Throttled meldet, ob eine Quelle zu viele Fehlversuche hatte (5 in 5 Minuten).
func (s *Store) Throttled(addr string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	cut := time.Now().Add(-5 * time.Minute)
	var keep []time.Time
	for _, t := range s.fails[addr] {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	s.fails[addr] = keep
	return len(keep) >= 5
}

func (s *Store) Fail(addr string) {
	s.mu.Lock()
	s.fails[addr] = append(s.fails[addr], time.Now())
	s.mu.Unlock()
}

func (s *Store) gc() {
	for range time.Tick(time.Minute) {
		s.mu.Lock()
		now := time.Now()
		for id, sess := range s.sessions {
			if now.Sub(sess.LastSeen) > s.Idle || now.Sub(sess.Created) > s.Max {
				delete(s.sessions, id)
			}
		}
		s.mu.Unlock()
	}
}

// CheckCSRF vergleicht das Token in konstanter Zeit.
func CheckCSRF(sess *Session, tok string) bool {
	return sess != nil && subtle.ConstantTimeCompare([]byte(sess.CSRF), []byte(tok)) == 1
}
