package server

import (
	"net/http"
	"time"

	"github.com/thorin-eifel/maar/internal/obsd"
)

type jobView struct {
	*obsd.BackupJob
	NextRun int64  `json:"nextRun"`
	LastRun int64  `json:"lastRun"`
	Running string `json:"running,omitempty"`
}

type targetView struct {
	*obsd.BackupTarget
	Location string `json:"location"`
}

func (s *Server) backupOverview(w http.ResponseWriter, r *http.Request) {
	c, err := s.Host.BackupConfig()
	if err != nil {
		fail(w, err)
		return
	}
	running := s.Sched.Running()
	jobs := []jobView{}
	for _, j := range c.Jobs {
		v := jobView{BackupJob: j, LastRun: c.LastRun[j.ID], Running: running[j.ID]}
		if j.Enabled {
			if n := j.Schedule.NextRun(time.Unix(c.LastRun[j.ID], 0)); !n.IsZero() {
				if n.Before(time.Now()) {
					n = time.Now()
				}
				v.NextRun = n.Unix()
			}
		}
		jobs = append(jobs, v)
	}
	targets := []targetView{}
	for _, t := range c.Targets {
		targets = append(targets, targetView{t, t.Location()})
	}
	hist := c.History
	if len(hist) > 30 {
		hist = hist[len(hist)-30:]
	}
	writeJSON(w, map[string]any{"targets": targets, "jobs": jobs, "history": hist})
}

func (s *Server) saveTarget(w http.ResponseWriter, r *http.Request) {
	var t obsd.BackupTarget
	if !decode(w, r, &t) {
		return
	}
	res, err := s.Host.SaveTarget(&t)
	s.audit(r, err, "backup target save %s (%s)", t.Name, t.Type)
	respond(w, map[string]any{"message": "Ziel „" + t.Name + "“ gespeichert.", "target": res}, err)
}

func (s *Server) deleteTarget(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.Host.DeleteTarget(id)
	s.audit(r, err, "backup target delete %s", id)
	respond(w, msg{Message: "Ziel entfernt. Vorhandene Sicherungen bleiben dort liegen."}, err)
}

func (s *Server) testTarget(w http.ResponseWriter, r *http.Request) {
	out, err := s.Host.TestTarget(r.Context(), r.PathValue("id"))
	respond(w, msg{Message: out}, err)
}

func (s *Server) backupPubKey(w http.ResponseWriter, r *http.Request) {
	k, err := s.Host.BackupPubKey(r.Context())
	respond(w, map[string]string{"key": k}, err)
}

func (s *Server) saveBackupJob(w http.ResponseWriter, r *http.Request) {
	var j obsd.BackupJob
	if !decode(w, r, &j) {
		return
	}
	res, err := s.Host.SaveJob(&j)
	s.audit(r, err, "backup job save %s", j.Name)
	respond(w, map[string]any{"message": "Job „" + j.Name + "“ gespeichert.", "job": res}, err)
}

func (s *Server) deleteBackupJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.Host.DeleteJob(id)
	s.audit(r, err, "backup job delete %s", id)
	respond(w, msg{Message: "Job gelöscht."}, err)
}

func (s *Server) runBackupJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	task, err := s.Sched.Run(id, "manuell", sessionFrom(r).User)
	s.audit(r, err, "backup job run %s", id)
	respond(w, msg{Job: task, Message: "Sicherung gestartet."}, err)
}

func (s *Server) backupNow(w http.ResponseWriter, r *http.Request) {
	var req obsd.BackupRequest
	if !decode(w, r, &req) {
		return
	}
	task, err := s.Sched.RunAdhoc(&req, sessionFrom(r).User)
	s.audit(r, err, "backup now %v → %s (%s)", req.VMs, req.Target, req.Mode)
	respond(w, msg{Job: task, Message: "Sicherung gestartet."}, err)
}

func (s *Server) listBackups(w http.ResponseWriter, r *http.Request) {
	l, err := s.Host.ListBackups(r.Context(), r.PathValue("id"))
	respond(w, l, err)
}

func (s *Server) deleteBackup(w http.ResponseWriter, r *http.Request) {
	id, vm, b := r.PathValue("id"), r.PathValue("vm"), r.PathValue("backup")
	err := s.Host.DeleteBackup(r.Context(), id, vm, b)
	s.audit(r, err, "backup delete %s %s/%s", id, vm, b)
	respond(w, msg{Message: "Sicherung " + vm + "/" + b + " gelöscht."}, err)
}

func (s *Server) restoreBackup(w http.ResponseWriter, r *http.Request) {
	var req obsd.RestoreRequest
	if !decode(w, r, &req) {
		return
	}
	t, err := s.Host.RestoreTask(r.Context(), r.PathValue("id"), &req)
	s.startTask(w, r, t, err)
}
