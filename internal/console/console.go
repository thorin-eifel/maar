// Package console stellt Terminalsitzungen bereit: geteilte serielle
// Konsolen der VMs (direkt über deren TTY, wie "vmctl console") und
// Host-Shells in einem eigenen Pseudoterminal.
package console

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"

	"github.com/creack/pty"
)

// Term ist eine Terminalsitzung aus Sicht eines einzelnen Zuschauers.
type Term interface {
	Write(p []byte) (int, error)
	Resize(cols, rows int)
	Output() <-chan []byte
	Done() <-chan struct{}
	Reason() string
	Close()
}

const scrollback = 64 << 10

// Hub teilt eine Konsole zwischen mehreren Zuschauern. Neue Zuschauer
// erhalten die letzten 64 KiB Ausgabe, damit sie den Kontext sehen.
type Hub struct {
	mu   sync.Mutex
	cons map[string]*shared
}

func NewHub() *Hub { return &Hub{cons: map[string]*shared{}} }

type shared struct {
	hub     *Hub
	key     string
	dev     io.ReadWriteCloser
	mu      sync.Mutex
	wmu     sync.Mutex
	clients map[*client]struct{}
	buf     []byte
	closed  bool
	reason  string
}

type client struct {
	s      *shared
	out    chan []byte
	done   chan struct{}
	once   sync.Once
	reason string
}

// Attach verbindet mit einer bestehenden Konsole oder öffnet sie über open.
func (h *Hub) Attach(key string, open func() (io.ReadWriteCloser, error)) (Term, error) {
	h.mu.Lock()
	s := h.cons[key]
	if s == nil {
		dev, err := open()
		if err != nil {
			h.mu.Unlock()
			return nil, err
		}
		s = &shared{hub: h, key: key, dev: dev, clients: map[*client]struct{}{}}
		h.cons[key] = s
		go s.pump()
	}
	h.mu.Unlock()

	c := &client{s: s, out: make(chan []byte, 256), done: make(chan struct{})}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, errors.New("Konsole wurde gerade geschlossen")
	}
	if len(s.buf) > 0 {
		c.out <- append([]byte(nil), s.buf...)
	}
	s.clients[c] = struct{}{}
	s.mu.Unlock()
	return c, nil
}

// Viewers liefert die Anzahl der Zuschauer je Konsole.
func (h *Hub) Viewers() map[string]int {
	h.mu.Lock()
	defer h.mu.Unlock()
	m := map[string]int{}
	for k, s := range h.cons {
		s.mu.Lock()
		m[k] = len(s.clients)
		s.mu.Unlock()
	}
	return m
}

func (s *shared) pump() {
	b := make([]byte, 8192)
	for {
		n, err := s.dev.Read(b)
		if n > 0 {
			chunk := append([]byte(nil), b[:n]...)
			s.mu.Lock()
			s.buf = append(s.buf, chunk...)
			if len(s.buf) > scrollback {
				s.buf = s.buf[len(s.buf)-scrollback:]
			}
			for c := range s.clients {
				select {
				case c.out <- chunk:
				default:
					// Zuschauer kommt nicht hinterher: trennen statt blockieren.
					c.finish("Verbindung zu langsam")
					delete(s.clients, c)
				}
			}
			s.mu.Unlock()
		}
		if err != nil {
			reason := "Konsole getrennt"
			if errors.Is(err, io.EOF) || errors.Is(err, os.ErrClosed) {
				reason = "Konsole beendet (VM gestoppt?)"
			}
			s.shutdown(reason)
			return
		}
	}
}

func (s *shared) shutdown(reason string) {
	s.hub.mu.Lock()
	if s.hub.cons[s.key] == s {
		delete(s.hub.cons, s.key)
	}
	s.hub.mu.Unlock()
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		for c := range s.clients {
			c.finish(reason)
		}
		s.clients = nil
	}
	s.mu.Unlock()
	s.dev.Close()
}

func (c *client) finish(reason string) {
	c.once.Do(func() {
		c.reason = reason
		close(c.done)
	})
}

func (c *client) Write(p []byte) (int, error) {
	c.s.wmu.Lock()
	defer c.s.wmu.Unlock()
	return c.s.dev.Write(p)
}

func (c *client) Resize(int, int)       {} // serielle Konsole: keine Fenstergröße
func (c *client) Output() <-chan []byte { return c.out }
func (c *client) Done() <-chan struct{} { return c.done }
func (c *client) Reason() string        { return c.reason }

// Close trennt diesen Zuschauer; der letzte schließt das Gerät.
func (c *client) Close() {
	s := c.s
	s.mu.Lock()
	delete(s.clients, c)
	last := len(s.clients) == 0 && !s.closed
	s.mu.Unlock()
	c.finish("geschlossen")
	if last {
		s.shutdown("geschlossen")
	}
}

// ---- Host-Shell im PTY ----

type ptyTerm struct {
	cmd    *exec.Cmd
	f      *os.File
	out    chan []byte
	done   chan struct{}
	once   sync.Once
	reason string
}

// StartShell startet cmd in einem neuen Pseudoterminal.
func StartShell(cmd *exec.Cmd, cols, rows int) (Term, error) {
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, err
	}
	t := &ptyTerm{cmd: cmd, f: f, out: make(chan []byte, 256), done: make(chan struct{})}
	go func() {
		b := make([]byte, 8192)
		for {
			n, err := f.Read(b)
			if n > 0 {
				select {
				case t.out <- append([]byte(nil), b[:n]...):
				case <-t.done:
					return
				}
			}
			if err != nil {
				t.finish("Shell beendet")
				return
			}
		}
	}()
	go func() {
		cmd.Wait()
		t.finish("Shell beendet")
	}()
	return t, nil
}

func (t *ptyTerm) finish(r string) {
	t.once.Do(func() {
		t.reason = r
		close(t.done)
	})
}

func (t *ptyTerm) Write(p []byte) (int, error) { return t.f.Write(p) }
func (t *ptyTerm) Resize(cols, rows int) {
	if cols > 0 && rows > 0 && cols < 1000 && rows < 1000 {
		pty.Setsize(t.f, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	}
}
func (t *ptyTerm) Output() <-chan []byte { return t.out }
func (t *ptyTerm) Done() <-chan struct{} { return t.done }
func (t *ptyTerm) Reason() string        { return t.reason }
func (t *ptyTerm) Close() {
	t.finish("geschlossen")
	if t.cmd.Process != nil {
		t.cmd.Process.Signal(os.Interrupt)
		t.cmd.Process.Kill()
	}
	t.f.Close()
}
