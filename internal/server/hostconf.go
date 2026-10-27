package server

import (
	"fmt"
	"net"
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
	case "pf":
		v, err = s.Host.PFForm(r.Context())
	case "doas":
		v, err = s.Host.Doas(r.Context())
	case "httpd":
		v, err = s.Host.HTTPD(r.Context())
	case "smtpd":
		v, err = s.Host.SMTPD(r.Context())
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
	case "pf":
		var c obsd.PFManaged
		if !decode(w, r, &c) {
			return
		}
		res, err = s.Host.SavePF(r.Context(), &c, s.pfGuard(r), preview)
	case "httpd":
		var req struct {
			Conf     obsd.HTTPConf `json:"conf"`
			Activate bool          `json:"activate"`
		}
		if !decode(w, r, &req) {
			return
		}
		res, err = s.Host.SaveHTTPD(r.Context(), &req.Conf, req.Activate, preview)
	case "smtpd":
		var req struct {
			Conf     obsd.SMTPConf `json:"conf"`
			Activate bool          `json:"activate"`
		}
		if !decode(w, r, &req) {
			return
		}
		res, err = s.Host.SaveSMTPD(r.Context(), &req.Conf, req.Activate, preview)
	case "doas":
		var c obsd.DoasConf
		if !decode(w, r, &c) {
			return
		}
		res, err = s.Host.SaveDoas(r.Context(), sessionFrom(r).User, &c, preview)
	default:
		jsonErr(w, http.StatusNotFound, "Unbekannte Konfiguration")
		return
	}
	if !preview {
		s.audit(r, err, "Konfiguration %s gespeichert", kind)
	}
	respond(w, res, err)
}

// pfGuard nennt die Ports, über die diese Sitzung und SSH laufen – Regeln, die sie sperren könnten, bekommen eine Warnung.
func (s *Server) pfGuard(r *http.Request) []obsd.PFGuard {
	var g []obsd.PFGuard
	if a, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
		if _, p, err := net.SplitHostPort(a.String()); err == nil {
			var n int
			fmt.Sscan(p, &n)
			if n > 0 {
				g = append(g, obsd.PFGuard{Port: n, Label: "diese Oberfläche"})
			}
		}
	}
	if v, err := s.Host.SSH(r.Context()); err == nil {
		ports := v.Conf.Ports
		if len(ports) == 0 {
			ports = []int{22}
		}
		for _, p := range ports {
			g = append(g, obsd.PFGuard{Port: p, Label: "SSH"})
		}
	}
	return g
}

func (s *Server) pfConfirm(w http.ResponseWriter, r *http.Request) {
	err := s.Host.PFConfirm(r.Context())
	s.audit(r, err, "Paketfilter-Regeln bestätigt")
	respond(w, msg{Message: "Die neuen Regeln sind dauerhaft."}, err)
}

func (s *Server) pfRevert(w http.ResponseWriter, r *http.Request) {
	err := s.Host.PFRevert(r.Context(), false)
	s.audit(r, err, "Paketfilter-Änderung zurückgenommen")
	respond(w, msg{Message: "Der vorherige Regelsatz ist wieder geladen."}, err)
}
