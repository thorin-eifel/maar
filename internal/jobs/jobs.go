// Package jobs führt lang laufende Befehle (pkg_add, syspatch, Downloads …)
// im Hintergrund aus und verteilt ihre Ausgabe an beliebig viele Zuschauer.
package jobs

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/thorin-eifel/maar/internal/obsd"
)

type State string

const (
	Running  State = "running"
	Done     State = "done"
	Failed   State = "failed"
	Canceled State = "canceled"
)

// Info ist der veröffentlichte Zustand eines Jobs.
type Info struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Command  string    `json:"command"`
	User     string    `json:"user"`
	State    State     `json:"state"`
	Error    string    `json:"error,omitempty"`
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished,omitempty"`
}

type Job struct {
	Info
	mu     sync.Mutex
	out    []byte
	subs   map[chan struct{}]struct{}
	cancel context.CancelFunc
}

// Write sammelt Ausgabe und weckt alle Zuschauer.
func (j *Job) Write(p []byte) (int, error) {
	j.mu.Lock()
	j.out = append(j.out, p...)
	if len(j.out) > 2<<20 {
		j.out = j.out[len(j.out)-(1<<20):]
	}
	for c := range j.subs {
		select {
		case c <- struct{}{}:
		default:
		}
	}
	j.mu.Unlock()
	return len(p), nil
}

// Output liefert die Ausgabe ab Offset sowie den neuen Offset und ob der Job läuft.
func (j *Job) Output(off int) ([]byte, int, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if off > len(j.out) {
		off = 0
	}
	return append([]byte(nil), j.out[off:]...), len(j.out), j.State == Running
}

func (j *Job) Subscribe() (chan struct{}, func()) {
	c := make(chan struct{}, 1)
	j.mu.Lock()
	j.subs[c] = struct{}{}
	j.mu.Unlock()
	return c, func() {
		j.mu.Lock()
		delete(j.subs, c)
		j.mu.Unlock()
	}
}

// Snapshot liefert eine Kopie des aktuellen Zustands.
func (j *Job) Snapshot() Info {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.Info
}

type Manager struct {
	R    obsd.Runner
	mu   sync.Mutex
	jobs map[string]*Job
	seq  int
}

func NewManager(r obsd.Runner) *Manager {
	return &Manager{R: r, jobs: map[string]*Job{}}
}

// Start führt einen einzelnen Befehl als Job aus.
func (m *Manager) Start(user string, c *obsd.JobCommand) *Job {
	cmd := strings.Join(append([]string{c.Name}, c.Args...), " ")
	return m.StartFunc(user, c.Title, cmd, func(ctx context.Context, w io.Writer) error {
		fmt.Fprintf(w, "$ %s\n", cmd)
		return m.R.Stream(ctx, w, c.Name, c.Args...)
	})
}

// StartFunc führt eine mehrstufige Aufgabe aus; fn schreibt ihren Fortschritt nach w.
func (m *Manager) StartFunc(user, title, command string, fn func(ctx context.Context, w io.Writer) error) *Job {
	m.mu.Lock()
	m.seq++
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Hour)
	j := &Job{Info: Info{ID: fmt.Sprintf("j%d", m.seq), Title: title, Command: command, User: user, State: Running,
		Started: time.Now()}, subs: map[chan struct{}]struct{}{}, cancel: cancel}
	m.jobs[j.ID] = j
	m.prune()
	m.mu.Unlock()

	go func() {
		defer cancel()
		err := fn(ctx, j)
		j.mu.Lock()
		j.Finished = time.Now()
		switch {
		case ctx.Err() == context.Canceled:
			j.State = Canceled
		case err != nil:
			j.State, j.Error = Failed, err.Error()
		default:
			j.State = Done
		}
		j.mu.Unlock()
		j.Write([]byte{}) // Zuschauer über Ende informieren
	}()
	return j
}

// prune behält höchstens 50 abgeschlossene Jobs.
func (m *Manager) prune() {
	var done []*Job
	for _, j := range m.jobs {
		if j.Snapshot().State != Running {
			done = append(done, j)
		}
	}
	if len(done) <= 50 {
		return
	}
	sort.Slice(done, func(a, b int) bool { return done[a].Started.Before(done[b].Started) })
	for _, j := range done[:len(done)-50] {
		delete(m.jobs, j.ID)
	}
}

func (m *Manager) Get(id string) *Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.jobs[id]
}

func (m *Manager) List() []Info {
	m.mu.Lock()
	defer m.mu.Unlock()
	list := make([]Info, 0, len(m.jobs))
	for _, j := range m.jobs {
		list = append(list, j.Snapshot())
	}
	sort.Slice(list, func(a, b int) bool { return list[a].Started.After(list[b].Started) })
	return list
}

func (m *Manager) Cancel(id string) bool {
	j := m.Get(id)
	if j == nil {
		return false
	}
	j.cancel()
	return true
}
