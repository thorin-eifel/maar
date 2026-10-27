package server

import "net/http"

func (s *Server) snapshots(w http.ResponseWriter, r *http.Request) {
	t, err := s.Host.Snapshots(r.Context(), r.PathValue("name"))
	respond(w, t, err)
}

type snapReq struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	SaveAs      string `json:"saveAs"`
}

func (s *Server) createSnapshot(w http.ResponseWriter, r *http.Request) {
	var req snapReq
	if !decode(w, r, &req) {
		return
	}
	vm := r.PathValue("name")
	snap, err := s.Host.CreateSnapshot(r.Context(), vm, req.Name, req.Description)
	s.audit(r, err, "snapshot create %s %q", vm, req.Name)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, map[string]any{"message": "Snapshot „" + snap.Name + "“ angelegt.", "snapshot": snap})
}

func (s *Server) renameSnapshot(w http.ResponseWriter, r *http.Request) {
	var req snapReq
	if !decode(w, r, &req) {
		return
	}
	vm, id := r.PathValue("name"), r.PathValue("id")
	err := s.Host.RenameSnapshot(r.Context(), vm, id, req.Name, req.Description)
	s.audit(r, err, "snapshot rename %s %s", vm, id)
	respond(w, msg{Message: "Snapshot gespeichert."}, err)
}

func (s *Server) revertSnapshot(w http.ResponseWriter, r *http.Request) {
	var req snapReq
	if r.ContentLength > 0 && !decode(w, r, &req) {
		return
	}
	vm, id := r.PathValue("name"), r.PathValue("id")
	err := s.Host.RevertSnapshot(r.Context(), vm, id, req.SaveAs)
	s.audit(r, err, "snapshot revert %s %s save=%q", vm, id, req.SaveAs)
	respond(w, msg{Message: vm + " auf den Snapshot zurückgesetzt."}, err)
}

func (s *Server) deleteSnapshot(w http.ResponseWriter, r *http.Request) {
	t, err := s.Host.DeleteSnapshot(r.Context(), r.PathValue("name"), r.PathValue("id"))
	s.startTask(w, r, t, err)
}

func (s *Server) consolidateSnapshots(w http.ResponseWriter, r *http.Request) {
	t, err := s.Host.ConsolidateSnapshots(r.Context(), r.PathValue("name"))
	s.startTask(w, r, t, err)
}
