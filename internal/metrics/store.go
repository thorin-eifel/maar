// Package metrics sammelt Messwerte, hält sie als Zeitreihen in zwei
// Auflösungen vor und wertet Alarmregeln aus.
package metrics

import (
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	RawStep = 10 * time.Second // Rohwerte: 2 Stunden
	RawLen  = 720
	AggStep = 5 * time.Minute // Mittelwerte: 7 Tage
	AggLen  = 2016
)

type Point struct {
	T int64   `json:"t"`
	V float64 `json:"v"`
}

type ring struct {
	P    []Point `json:"p"`
	Head int     `json:"h"`
	Max  int     `json:"m"`
}

func (r *ring) add(p Point) {
	if len(r.P) < r.Max {
		r.P = append(r.P, p)
		return
	}
	r.P[r.Head] = p
	r.Head = (r.Head + 1) % r.Max
}

func (r *ring) since(from int64) []Point {
	out := make([]Point, 0, len(r.P))
	n := len(r.P)
	for i := 0; i < n; i++ {
		p := r.P[(r.Head+i)%n]
		if p.T >= from {
			out = append(out, p)
		}
	}
	return out
}

type series struct {
	Raw     ring    `json:"raw"`
	Agg     ring    `json:"agg"`
	Bucket  int64   `json:"bucket"`
	Sum     float64 `json:"sum"`
	N       int     `json:"n"`
	Updated int64   `json:"updated"`
}

type Store struct {
	mu sync.Mutex
	s  map[string]*series
}

func NewStore() *Store { return &Store{s: map[string]*series{}} }

func (st *Store) Add(key string, t time.Time, v float64) {
	st.mu.Lock()
	defer st.mu.Unlock()
	se := st.s[key]
	if se == nil {
		se = &series{Raw: ring{Max: RawLen}, Agg: ring{Max: AggLen}}
		st.s[key] = se
	}
	ts := t.Unix()
	se.Raw.add(Point{ts, v})
	se.Updated = ts
	bucket := t.Truncate(AggStep).Unix()
	if se.Bucket != 0 && bucket != se.Bucket && se.N > 0 {
		se.Agg.add(Point{se.Bucket, se.Sum / float64(se.N)})
		se.Sum, se.N = 0, 0
	}
	se.Bucket = bucket
	se.Sum += v
	se.N++
}

// Query liefert die Werte ab from: Rohwerte, solange sie reichen, sonst
// 5-Minuten-Mittel plus den laufenden Abschnitt.
func (st *Store) Query(key string, from time.Time) []Point {
	st.mu.Lock()
	defer st.mu.Unlock()
	se := st.s[key]
	if se == nil {
		return []Point{}
	}
	f := from.Unix()
	if time.Since(from) <= time.Duration(RawLen)*RawStep {
		return se.Raw.since(f)
	}
	out := se.Agg.since(f)
	if se.N > 0 {
		out = append(out, Point{se.Bucket, se.Sum / float64(se.N)})
	}
	return out
}

// Last liefert den jüngsten Wert, sofern er höchstens maxAge alt ist.
func (st *Store) Last(key string, maxAge time.Duration) (float64, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	se := st.s[key]
	if se == nil || len(se.Raw.P) == 0 || time.Since(time.Unix(se.Updated, 0)) > maxAge {
		return 0, false
	}
	n := len(se.Raw.P)
	return se.Raw.P[(se.Raw.Head+n-1)%n].V, true
}

func (st *Store) Keys(prefix string) []string {
	st.mu.Lock()
	defer st.mu.Unlock()
	var k []string
	for key := range st.s {
		if strings.HasPrefix(key, prefix) {
			k = append(k, key)
		}
	}
	sort.Strings(k)
	return k
}

// Prune entfernt Reihen, die seit max nicht mehr geschrieben wurden (gelöschte VMs).
func (st *Store) Prune(max time.Duration) {
	st.mu.Lock()
	defer st.mu.Unlock()
	cut := time.Now().Add(-max).Unix()
	for k, se := range st.s {
		if se.Updated < cut {
			delete(st.s, k)
		}
	}
}

func (st *Store) Save(path string) error {
	st.mu.Lock()
	b, err := json.Marshal(st.s)
	st.mu.Unlock()
	if err != nil {
		return err
	}
	os.MkdirAll(filepath.Dir(path), 0o700)
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	zw := gzip.NewWriter(f)
	zw.Write(b)
	zw.Close()
	f.Close()
	return os.Rename(tmp, path)
}

func (st *Store) Load(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	m := map[string]*series{}
	if err := json.NewDecoder(zr).Decode(&m); err != nil {
		return err
	}
	for _, se := range m {
		se.Raw.Max, se.Agg.Max = RawLen, AggLen
	}
	st.mu.Lock()
	st.s = m
	st.mu.Unlock()
	return nil
}
