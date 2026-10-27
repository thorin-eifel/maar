package sched

import (
	"io"
	"log"
	"os"
	"testing"
	"time"

	"github.com/thorin-eifel/maar/internal/jobs"
	"github.com/thorin-eifel/maar/internal/obsd"
)

func TestTickRunsDueJobOnce(t *testing.T) {
	d, root, err := obsd.NewDemo()
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	h := obsd.NewHost(d, root, true)
	s := New(h, jobs.NewManager(d), log.New(io.Discard, "", 0))

	// Der Demo-Job „naechtlich“ läuft täglich 02:15; zwei Tage später ist er fällig.
	future := time.Now().AddDate(0, 0, 2)
	s.tick(future)
	id := s.Running()["naechtlich"]
	if id == "" {
		t.Fatal("fälliger Job wurde nicht gestartet")
	}
	s.tick(future) // läuft bereits: kein zweiter Start
	if s.Running()["naechtlich"] != id {
		t.Error("Job doppelt gestartet")
	}
	deadline := time.Now().Add(60 * time.Second)
	for s.Running()["naechtlich"] != "" && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
	}
	c, _ := h.BackupConfig()
	last := c.History[len(c.History)-1]
	if last.TaskID != id || last.State != "done" {
		t.Fatalf("Verlauf: %+v", last)
	}
	// Direkt danach ist der Job nicht erneut fällig.
	s.tick(time.Now())
	if s.Running()["naechtlich"] != "" {
		t.Error("Job lief direkt erneut")
	}
}
