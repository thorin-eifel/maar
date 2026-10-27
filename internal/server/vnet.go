package server

import (
	"net/http"

	"github.com/thorin-eifel/maar/internal/obsd"
)

func (s *Server) vnet(w http.ResponseWriter, r *http.Request) {
	o, err := s.Host.VNet(r.Context())
	respond(w, o, err)
}

func (s *Server) vnetPreview(w http.ResponseWriter, r *http.Request) {
	p, err := s.Host.NetPreview(r.Context())
	respond(w, p, err)
}

func (s *Server) savePortGroup(w http.ResponseWriter, r *http.Request) {
	var pg obsd.PortGroup
	if !decode(w, r, &pg) {
		return
	}
	t, err := s.Host.SavePortGroup(r.Context(), r.PathValue("name"), &pg)
	s.startTask(w, r, t, err)
}

func (s *Server) deletePortGroup(w http.ResponseWriter, r *http.Request) {
	t, err := s.Host.DeletePortGroup(r.Context(), r.PathValue("name"))
	s.startTask(w, r, t, err)
}

func (s *Server) setVMNICs(w http.ResponseWriter, r *http.Request) {
	var req struct {
		NICs []obsd.NICSpec `json:"nics"`
	}
	if !decode(w, r, &req) {
		return
	}
	t, err := s.Host.SetVMNICs(r.Context(), r.PathValue("name"), req.NICs)
	s.startTask(w, r, t, err)
}

func (s *Server) setVMFirewall(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Policy obsd.VMPolicy  `json:"policy"`
		Rules  []*obsd.FWRule `json:"rules"`
	}
	if !decode(w, r, &req) {
		return
	}
	name := r.PathValue("name")
	out, err := s.Host.SetVMFirewall(r.Context(), name, req.Policy, req.Rules)
	s.audit(r, err, "vm firewall %s (%d Regeln, in=%s out=%s)", name, len(req.Rules), req.Policy.In, req.Policy.Out)
	respond(w, msg{Message: "Firewall von " + name + " gespeichert und geladen.", Output: out}, err)
}

func (s *Server) syncVNet(w http.ResponseWriter, r *http.Request) {
	s.startTask(w, r, s.Host.SyncNetwork(), nil)
}
