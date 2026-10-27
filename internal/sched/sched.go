// Package sched startet Sicherungsjobs nach ihrem Zeitplan und führt den Verlauf.
package sched

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/thorin-eifel/maar/internal/jobs"
	"github.com/thorin-eifel/maar/internal/obsd"
)

type Scheduler struct {
	Host *obsd.Host
	Jobs *jobs.Manager
	Log  *log.Logger

	mu      sync.Mutex
	running map[string]string // Backup-Job → Task-ID
}

func New(h *obsd.Host, j *jobs.Manager, l *log.Logger) *Scheduler {
	return &Scheduler{Host: h, Jobs: j, Log: l, running: map[string]string{}}
}

// Start prüft alle 30 Sekunden, ob ein Job fällig ist. Verpasste Termine
// (Dienst war aus) werden einmal nachgeholt.
func (s *Scheduler) Start(ctx context.Context) {
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			s.tick(time.Now())
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}

func (s *Scheduler) tick(now time.Time) {
	c, err := s.Host.BackupConfig()
	if err != nil {
		s.Log.Printf("backup: %v", err)
		return
	}
	for _, j := range c.Jobs {
		if !j.Enabled || j.Schedule.Kind == "manual" {
			continue
		}
		next := j.Schedule.NextRun(time.Unix(c.LastRun[j.ID], 0))
		if !next.IsZero() && !now.Before(next) {
			if _, err := s.Run(j.ID, "Zeitplan", "maar"); err != nil {
				s.Log.Printf("backup %s: %v", j.ID, err)
			}
		}
	}
}

// Running liefert die Task-IDs laufender Sicherungsjobs.
func (s *Scheduler) Running() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := map[string]string{}
	for k, v := range s.running {
		m[k] = v
	}
	return m
}

// Run startet einen Sicherungsjob sofort.
func (s *Scheduler) Run(jobID, trigger, user string) (string, error) {
	s.mu.Lock()
	if id, ok := s.running[jobID]; ok {
		s.mu.Unlock()
		return id, nil
	}
	s.mu.Unlock()
	req, name, err := s.Host.RequestForJob(jobID)
	if err != nil {
		return "", err
	}
	return s.start(req, name, trigger, user)
}

// RunAdhoc sichert einzelne VMs außerhalb eines Jobs.
func (s *Scheduler) RunAdhoc(req *obsd.BackupRequest, user string) (string, error) {
	req.Job = ""
	return s.start(req, "", "manuell", user)
}

func (s *Scheduler) start(req *obsd.BackupRequest, name, trigger, user string) (string, error) {
	task, err := s.Host.BackupTask(context.Background(), req, name)
	if err != nil {
		s.Host.Emit("backup_failed", name, err.Error())
		if req.Job != "" {
			s.Host.RecordRun(obsd.BackupRun{Job: req.Job, Name: name, Trigger: trigger, Started: time.Now().Unix(), Ended: time.Now().Unix(), State: "failed", Error: err.Error()})
		}
		return "", err
	}
	j := s.Jobs.StartFunc(user, task.Title, task.Command, task.Run)
	run := obsd.BackupRun{Job: req.Job, Name: task.Title, Trigger: trigger, TaskID: j.ID, Started: j.Started.Unix(), State: "running"}
	s.Host.RecordRun(run)
	s.mu.Lock()
	if req.Job != "" {
		s.running[req.Job] = j.ID
	}
	s.mu.Unlock()
	go func() {
		for {
			info := j.Snapshot()
			if info.State != jobs.Running {
				run.State, run.Error, run.Ended = string(info.State), info.Error, info.Finished.Unix()
				s.Host.RecordRun(run)
				if info.State == jobs.Failed {
					s.Host.Emit("backup_failed", run.Name, info.Error)
				}
				s.mu.Lock()
				if s.running[req.Job] == j.ID {
					delete(s.running, req.Job)
				}
				s.mu.Unlock()
				return
			}
			time.Sleep(2 * time.Second)
		}
	}()
	return j.ID, nil
}
