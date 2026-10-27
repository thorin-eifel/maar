package server

import (
	"net/http"

	"github.com/thorin-eifel/maar/internal/obsd"
)

// Strukturierte Konfiguration von ntpd, resolv.conf, hosts und sshd. Lesen und
// Schreiben nur für Administratoren (siehe readDenied); ?preview=1 schreibt nichts.

func (s *Server) confGet(w http.ResponseWriter, r *http.Request) {
	var v any
	var err error
	switch r.PathValue("kind") {
	case "ntp":
		v, err = s.Host.NTP(r.Context())
	case "resolv":
		v, err = s.Host.Resolv(r.Context())
	case "hosts":
		v, err = s.Host.Hosts(r.Context())
	case "ssh":
		v, err = s.Host.SSH(r.Context())
	default:
		jsonErr(w, http.StatusNotFound, "Unbekannte Konfiguration")
		return
	}
	respond(w, v, err)
}

func (s *Server) confPut(w http.ResponseWriter, r *http.Request) {
	kind, preview := r.PathValue("kind"), r.URL.Query().Get("preview") == "1"
	var res *obsd.ConfResult
	var err error
	switch kind {
	case "ntp":
		var req struct {
			Conf     obsd.NTPConf `json:"conf"`
			Activate bool         `json:"activate"`
		}
		if !decode(w, r, &req) {
			return
		}
		res, err = s.Host.SaveNTP(r.Context(), &req.Conf, req.Activate, preview)
	case "resolv":
		var c obsd.ResolvConf
		if !decode(w, r, &c) {
			return
		}
		res, err = s.Host.SaveResolv(r.Context(), &c, preview)
	case "hosts":
		var req struct {
			Entries []obsd.HostsEntry `json:"entries"`
		}
		if !decode(w, r, &req) {
			return
		}
		res, err = s.Host.SaveHosts(r.Context(), req.Entries, preview)
	case "ssh":
		var c obsd.SSHConf
		if !decode(w, r, &c) {
			return
		}
		res, err = s.Host.SaveSSH(r.Context(), sessionFrom(r).User, &c, preview)
	default:
		jsonErr(w, http.StatusNotFound, "Unbekannte Konfiguration")
		return
	}
	if !preview {
		s.audit(r, err, "Konfiguration %s gespeichert", kind)
	}
	respond(w, res, err)
}
