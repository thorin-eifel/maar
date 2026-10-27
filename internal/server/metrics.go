package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/thorin-eifel/maar/internal/metrics"
	"github.com/thorin-eifel/maar/internal/obsd"
)

var ranges = map[string]time.Duration{"1h": time.Hour, "6h": 6 * time.Hour, "24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour}

func (s *Server) metricsQuery(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	d, ok := ranges[q.Get("range")]
	if !ok {
		d = time.Hour
	}
	from := time.Now().Add(-d)
	out := map[string][]metrics.Point{}
	for _, k := range strings.Split(q.Get("keys"), ",") {
		k = strings.TrimSpace(k)
		if k == "" || len(k) > 200 {
			continue
		}
		if strings.HasSuffix(k, "*") {
			for _, kk := range s.Metrics.Store.Keys(strings.TrimSuffix(k, "*")) {
				out[kk] = s.Metrics.Store.Query(kk, from)
			}
			continue
		}
		out[k] = s.Metrics.Store.Query(k, from)
	}
	writeJSON(w, map[string]any{"from": from.Unix(), "to": time.Now().Unix(), "series": out})
}

func (s *Server) metricsKeys(w http.ResponseWriter, r *http.Request) {
	k := s.Metrics.Store.Keys(r.URL.Query().Get("prefix"))
	if k == nil {
		k = []string{}
	}
	writeJSON(w, k)
}

func (s *Server) alerts(w http.ResponseWriter, r *http.Request) {
	active, hist := s.Metrics.Alerts.Snapshot()
	if len(hist) > 100 {
		hist = hist[len(hist)-100:]
	}
	writeJSON(w, map[string]any{"config": s.Metrics.Alerts.Config(), "active": active, "history": hist, "eventKinds": metrics.EventKinds})
}

func (s *Server) alertSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Recipients []string        `json:"recipients"`
		Events     map[string]bool `json:"events"`
	}
	if !decode(w, r, &req) {
		return
	}
	err := s.Metrics.Alerts.SetSettings(req.Recipients, req.Events)
	s.audit(r, err, "alerts settings %v", req.Recipients)
	respond(w, msg{Message: "Benachrichtigungen gespeichert."}, err)
}

func (s *Server) saveAlertRule(w http.ResponseWriter, r *http.Request) {
	var rule metrics.Rule
	if !decode(w, r, &rule) {
		return
	}
	err := s.Metrics.Alerts.SaveRule(&rule)
	s.audit(r, err, "alert rule save %s %s%s%v", rule.Name, rule.Metric, rule.Op, rule.Threshold)
	respond(w, msg{Message: "Regel „" + rule.Name + "“ gespeichert."}, err)
}

func (s *Server) deleteAlertRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.Metrics.Alerts.DeleteRule(id)
	s.audit(r, err, "alert rule delete %s", id)
	respond(w, msg{Message: "Regel gelöscht."}, err)
}

func (s *Server) testAlertMail(w http.ResponseWriter, r *http.Request) {
	err := s.Metrics.Alerts.Test()
	s.audit(r, err, "alert test mail")
	if err != nil && !strings.Contains(err.Error(), obsd.ErrInvalid.Error()) {
		fail(w, err)
		return
	}
	respond(w, msg{Message: "Testnachricht verschickt. Kommt sie nicht an: smtpd und /var/log/maillog prüfen."}, err)
}
