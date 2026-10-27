package server

import (
	"net/http"
	"strings"

	"github.com/thorin-eifel/maar/internal/obsd"
)

// vmDetail liefert alles für die Detailseite: Status, Hardware, Netz, Firewall,
// Sicherung, aktive Alarme und die letzten Ereignisse dieser VM.
func (s *Server) vmDetail(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	d, err := s.Host.VMDetail(r.Context(), name)
	if err != nil {
		fail(w, err)
		return
	}
	alerts := []map[string]any{}
	if s.Metrics != nil {
		act, _ := s.Metrics.Alerts.Snapshot()
		for _, a := range act {
			if strings.HasPrefix(a.Subject, "vm."+name+".") {
				alerts = append(alerts, map[string]any{"name": a.Name, "subject": a.Subject, "since": a.Since, "value": a.Value, "severity": a.Severity})
			}
		}
	}
	events := s.Events.Query("", "", "", name, 30)
	writeJSON(w, struct {
		*obsd.VMDetail
		Alerts  []map[string]any `json:"alerts"`
		Events  []AuditEvent     `json:"events"`
		Viewers int              `json:"viewers"`
	}{d, alerts, events, s.Hub.Viewers()["vm:"+name]})
}
