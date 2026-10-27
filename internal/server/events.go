package server

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/thorin-eifel/maar/internal/obsd"
)

// Ereignisprotokoll: jede Anmeldung und jede verändernde oder verweigerte
// Aktion mit Benutzer, Quelle und Ergebnis. Die letzten 2000 Einträge bleiben
// in /var/maar/events.jsonl erhalten (zusätzlich zum Syslog).

const (
	eventsPath = "/var/maar/events.jsonl"
	eventsKeep = 2000
)

type AuditEvent struct {
	Time   int64  `json:"time"`
	User   string `json:"user"`
	Addr   string `json:"addr"`
	Kind   string `json:"kind"`   // anmeldung, aktion, verweigert
	Result string `json:"result"` // ok, fehler
	Text   string `json:"text"`
	Error  string `json:"error,omitempty"`
}

type EventLog struct {
	mu   sync.Mutex
	path string
	list []AuditEvent
	n    int // seit dem Laden angehängt
}

func NewEventLog(h *obsd.Host) *EventLog {
	e := &EventLog{path: h.P(eventsPath)}
	if f, err := os.Open(e.path); err == nil {
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			var ev AuditEvent
			if json.Unmarshal(sc.Bytes(), &ev) == nil {
				e.list = append(e.list, ev)
			}
		}
		f.Close()
		if len(e.list) > eventsKeep {
			e.list = e.list[len(e.list)-eventsKeep:]
		}
		e.rewrite()
	}
	return e
}

func (e *EventLog) rewrite() {
	var b strings.Builder
	for _, ev := range e.list {
		j, _ := json.Marshal(ev)
		b.Write(j)
		b.WriteByte('\n')
	}
	os.MkdirAll(filepath.Dir(e.path), 0o700)
	os.WriteFile(e.path, []byte(b.String()), 0o600)
}

func (e *EventLog) Add(ev AuditEvent) {
	if ev.Time == 0 {
		ev.Time = time.Now().Unix()
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.list = append(e.list, ev)
	e.n++
	if len(e.list) > eventsKeep+200 {
		e.list = e.list[len(e.list)-eventsKeep:]
		e.rewrite()
		return
	}
	os.MkdirAll(filepath.Dir(e.path), 0o700)
	f, err := os.OpenFile(e.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	j, _ := json.Marshal(ev)
	f.Write(append(j, '\n'))
	f.Close()
}

// Query liefert die neuesten Einträge zuerst, gefiltert.
func (e *EventLog) Query(user, kind, result, q string, limit int) []AuditEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	q = strings.ToLower(q)
	out := []AuditEvent{}
	for i := len(e.list) - 1; i >= 0 && len(out) < limit; i-- {
		ev := e.list[i]
		if user != "" && ev.User != user || kind != "" && ev.Kind != kind || result != "" && ev.Result != result {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(ev.Text+" "+ev.Error+" "+ev.User+" "+ev.Addr), q) {
			continue
		}
		out = append(out, ev)
	}
	return out
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit < 1 || limit > eventsKeep {
		limit = 200
	}
	writeJSON(w, map[string]any{"events": s.Events.Query(q.Get("user"), q.Get("kind"), q.Get("result"), q.Get("q"), limit)})
}
