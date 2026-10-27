package server

import (
	"net/http"

	"github.com/thorin-eifel/maar/internal/obsd"
)

func (s *Server) san(w http.ResponseWriter, r *http.Request) {
	o, err := s.Host.SAN(r.Context())
	respond(w, o, err)
}

func (s *Server) saveISCSI(w http.ResponseWriter, r *http.Request) {
	var t obsd.ISCSITarget
	if !decode(w, r, &t) {
		return
	}
	task, err := s.Host.SaveISCSITarget(r.Context(), r.PathValue("name"), &t)
	s.startTask(w, r, task, err)
}

func (s *Server) deleteISCSI(w http.ResponseWriter, r *http.Request) {
	task, err := s.Host.DeleteISCSITarget(r.Context(), r.PathValue("name"))
	s.startTask(w, r, task, err)
}

func (s *Server) initDatastore(w http.ResponseWriter, r *http.Request) {
	var req struct{ Disk, Name string }
	if !decode(w, r, &req) {
		return
	}
	task, err := s.Host.InitDatastore(r.Context(), req.Disk, req.Name)
	s.startTask(w, r, task, err)
}

func (s *Server) datastoreAction(w http.ResponseWriter, r *http.Request) {
	name, a := r.PathValue("name"), r.PathValue("action")
	var err error
	switch a {
	case "mount":
		err = s.Host.MountDatastore(r.Context(), name)
	case "unmount":
		err = s.Host.UnmountDatastore(r.Context(), name)
	default:
		jsonErr(w, http.StatusBadRequest, "Aktion "+a)
		return
	}
	s.audit(r, err, "datastore %s %s", a, name)
	respond(w, msg{Message: "Datastore " + name + ": " + a + " ausgeführt."}, err)
}

func (s *Server) removeDatastore(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	err := s.Host.RemoveDatastore(r.Context(), name)
	s.audit(r, err, "datastore remove %s", name)
	respond(w, msg{Message: "Datastore " + name + " entfernt. Die Daten auf der Disk sind unberührt."}, err)
}

func (s *Server) datastoreDetail(w http.ResponseWriter, r *http.Request) {
	d, err := s.Host.DatastoreDetail(r.Context(), r.PathValue("name"), s.scope(r))
	respond(w, d, err)
}

func (s *Server) portGroupDetail(w http.ResponseWriter, r *http.Request) {
	d, err := s.Host.PortGroupDetail(r.Context(), r.PathValue("name"), s.scope(r))
	respond(w, d, err)
}
