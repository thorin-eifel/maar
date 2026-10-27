package server

import (
	"net/http"

	"github.com/thorin-eifel/maar/internal/obsd"
)

func (s *Server) startTask(w http.ResponseWriter, r *http.Request, t *obsd.Task, err error) {
	if err != nil {
		s.audit(r, err, "task")
		fail(w, err)
		return
	}
	j := s.Jobs.StartFunc(sessionFrom(r).User, t.Title, t.Command, t.Run)
	s.audit(r, nil, "job %s: %s", j.ID, t.Title)
	writeJSON(w, msg{Job: j.ID, Message: t.Title + " gestartet."})
}

func (s *Server) templates(w http.ResponseWriter, r *http.Request) {
	t, err := s.Host.Templates()
	respond(w, map[string]any{"templates": t, "vmDir": s.Host.VMDir}, err)
}

func (s *Server) importTemplate(w http.ResponseWriter, r *http.Request) {
	var spec obsd.TemplateSpec
	if !decode(w, r, &spec) {
		return
	}
	t, err := s.Host.ImportTemplate(r.Context(), &spec)
	s.startTask(w, r, t, err)
}

func (s *Server) vmToTemplate(w http.ResponseWriter, r *http.Request) {
	var spec obsd.TemplateSpec
	if !decode(w, r, &spec) {
		return
	}
	t, err := s.Host.TemplateFromVM(r.Context(), r.PathValue("name"), &spec)
	s.startTask(w, r, t, err)
}

func (s *Server) updateTemplate(w http.ResponseWriter, r *http.Request) {
	var spec obsd.TemplateSpec
	if !decode(w, r, &spec) {
		return
	}
	name := r.PathValue("name")
	err := s.Host.UpdateTemplate(name, &spec)
	s.audit(r, err, "template update %s", name)
	respond(w, msg{Message: "Template " + name + " gespeichert."}, err)
}

func (s *Server) deleteTemplate(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	err := s.Host.DeleteTemplate(name)
	s.audit(r, err, "template delete %s", name)
	respond(w, msg{Message: "Template " + name + " gelöscht."}, err)
}

func (s *Server) deploy(source string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var spec obsd.DeploySpec
		if !decode(w, r, &spec) {
			return
		}
		t, err := s.Host.Deploy(r.Context(), source, r.PathValue("name"), &spec)
		s.startTask(w, r, t, err)
	}
}
