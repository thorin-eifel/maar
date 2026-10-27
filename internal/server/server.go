// Package server stellt die HTTP-API, die WebSocket-Konsolen und die
// eingebettete Oberfläche bereit.
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/thorin-eifel/maar/internal/auth"
	"github.com/thorin-eifel/maar/internal/console"
	"github.com/thorin-eifel/maar/internal/jobs"
	"github.com/thorin-eifel/maar/internal/metrics"
	"github.com/thorin-eifel/maar/internal/obsd"
	"github.com/thorin-eifel/maar/internal/sched"
)

const cookieName = "maar_session"

type Server struct {
	Host       *obsd.Host
	Auth       auth.Verifier
	Sessions   *auth.Store
	Jobs       *jobs.Manager
	Hub        *console.Hub
	Sched      *sched.Scheduler
	Metrics    *metrics.Collector
	Static     fs.FS
	AdminGroup string
	Secure     bool // Cookies nur über TLS
	Audit      *log.Logger
	Access     *Access
	Events     *EventLog
	Version    string
}

type ctxKey struct{}

func (s *Server) Handler() http.Handler {
	if s.Access == nil {
		s.Access = NewAccess(s.Host)
	}
	if s.Events == nil {
		s.Events = NewEventLog(s.Host)
	}
	s.Jobs.SetOnDone(func(i jobs.Info) {
		ev := AuditEvent{User: i.User, Kind: "auftrag", Result: "ok", Text: "Auftrag " + i.ID + " beendet: " + i.Title}
		switch i.State {
		case jobs.Failed:
			ev.Result, ev.Error = "fehler", strings.ReplaceAll(i.Error, "\n", " ")
		case jobs.Canceled:
			ev.Result, ev.Error = "fehler", "abgebrochen"
		}
		s.Events.Add(ev)
	})
	mux := http.NewServeMux()
	api := func(pattern string, h http.HandlerFunc) { mux.Handle(pattern, s.protect(h)) }

	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/logout", s.logout)
	mux.HandleFunc("GET /api/session", s.session)

	api("GET /api/overview", s.overview)
	api("GET /api/storage", s.storage)
	api("GET /api/disklabel/{disk}", s.disklabel)
	api("GET /api/procs", s.procs)
	api("POST /api/procs/{pid}/kill", s.kill)
	api("GET /api/sysctl", s.sysctls)
	api("PUT /api/sysctl", s.setSysctl)
	api("POST /api/power/{action}", s.power)

	api("GET /api/vms", s.vms)
	api("POST /api/vms", s.createVM)
	api("PUT /api/vms/{name}/config", s.updateVM)
	api("DELETE /api/vms/{name}", s.deleteVM)
	api("POST /api/vms/{name}/{action}", s.vmAction)
	api("POST /api/vm-disks", s.createDisk)
	api("DELETE /api/vm-images", s.deleteImage)
	api("POST /api/vm-isos/download", s.downloadISO)
	api("PUT /api/vm-isos/upload", s.uploadISO)
	api("POST /api/vm-switches", s.addSwitch)
	api("POST /api/vms/{name}/to-template", s.vmToTemplate)
	api("POST /api/vms/{name}/clone", s.deploy("vm"))
	api("GET /api/vms/{name}/snapshots", s.snapshots)
	api("POST /api/vms/{name}/snapshots", s.createSnapshot)
	api("POST /api/vms/{name}/snapshots/consolidate", s.consolidateSnapshots)
	api("PUT /api/vms/{name}/snapshots/{id}", s.renameSnapshot)
	api("POST /api/vms/{name}/snapshots/{id}/revert", s.revertSnapshot)
	api("DELETE /api/vms/{name}/snapshots/{id}", s.deleteSnapshot)

	api("GET /api/metrics", s.metricsQuery)
	api("GET /api/metrics/keys", s.metricsKeys)
	api("GET /api/alerts", s.alerts)
	api("PUT /api/alerts/settings", s.alertSettings)
	api("POST /api/alerts/rules", s.saveAlertRule)
	api("DELETE /api/alerts/rules/{id}", s.deleteAlertRule)
	api("POST /api/alerts/test", s.testAlertMail)

	api("GET /api/backup", s.backupOverview)
	api("GET /api/backup/pubkey", s.backupPubKey)
	api("POST /api/backup/targets", s.saveTarget)
	api("DELETE /api/backup/targets/{id}", s.deleteTarget)
	api("POST /api/backup/targets/{id}/test", s.testTarget)
	api("GET /api/backup/targets/{id}/backups", s.listBackups)
	api("DELETE /api/backup/targets/{id}/backups/{vm}/{backup}", s.deleteBackup)
	api("POST /api/backup/targets/{id}/restore", s.restoreBackup)
	api("POST /api/backup/jobs", s.saveBackupJob)
	api("DELETE /api/backup/jobs/{id}", s.deleteBackupJob)
	api("POST /api/backup/jobs/{id}/run", s.runBackupJob)
	api("POST /api/backup/now", s.backupNow)

	api("GET /api/templates", s.templates)
	api("POST /api/templates/import", s.importTemplate)
	api("PUT /api/templates/{name}", s.updateTemplate)
	api("DELETE /api/templates/{name}", s.deleteTemplate)
	api("POST /api/templates/{name}/deploy", s.deploy("template"))

	api("GET /api/services", s.services)
	api("GET /api/services/{name}", s.service)
	api("PUT /api/services/{name}/flags", s.setFlags)
	api("POST /api/services/{name}/{action}", s.serviceAction)

	api("GET /api/network", s.network)
	api("POST /api/network/{iface}/{action}", s.ifaceAction)

	api("GET /api/vnet", s.vnet)
	api("GET /api/vnet/preview", s.vnetPreview)
	api("POST /api/vnet/sync", s.syncVNet)
	api("POST /api/vnet/portgroups", s.savePortGroup)
	api("PUT /api/vnet/portgroups/{name}", s.savePortGroup)
	api("DELETE /api/vnet/portgroups/{name}", s.deletePortGroup)
	api("PUT /api/vnet/vms/{name}/nics", s.setVMNICs)
	api("PUT /api/vnet/vms/{name}/firewall", s.setVMFirewall)

	api("GET /api/pf", s.pf)
	api("GET /api/pf/states", s.pfStates)
	api("GET /api/pf/tables/{table}", s.pfTable)
	api("POST /api/pf/tables/{table}", s.pfTableEdit)
	api("POST /api/pf/{action}", s.pfAction)

	api("GET /api/packages", s.packages)
	api("GET /api/packages/search", s.searchPackages)
	api("GET /api/patches", s.patches)
	api("POST /api/packages/{action}", s.pkgJob)

	api("GET /api/users", s.users)
	api("POST /api/users", s.createUser)
	api("PUT /api/users/{name}", s.updateUser)
	api("POST /api/users/{name}/password", s.setPassword)
	api("POST /api/users/{name}/{action}", s.userAction)
	api("POST /api/groups", s.createGroup)
	api("DELETE /api/groups/{name}", s.deleteGroup)
	api("GET /api/cron/{user}", s.crontab)
	api("PUT /api/cron/{user}", s.setCrontab)

	api("GET /api/files", s.files)
	api("GET /api/file", s.readFile)
	api("PUT /api/file", s.writeFile)
	api("GET /api/file/backups", s.backups)
	api("GET /api/file/backup", s.backup)
	api("POST /api/file/apply", s.applyFile)

	api("GET /api/logs", s.logs)
	api("GET /api/log", s.log)
	api("GET /api/log/follow", s.followLog)

	api("GET /api/events", s.events)
	api("GET /api/access", s.accessGet)
	api("PUT /api/access", s.accessPut)

	api("GET /api/jobs", s.jobList)
	api("GET /api/jobs/{id}/stream", s.jobStream)
	api("POST /api/jobs/{id}/cancel", s.jobCancel)

	api("GET /ws/console/vm/{name}", s.vmConsole)
	api("GET /ws/console/shell", s.shellConsole)

	mux.Handle("GET /", s.static())
	return s.headers(mux)
}

func (s *Server) headers(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("Referrer-Policy", "no-referrer")
		hd.Set("X-Frame-Options", "DENY")
		if s.Secure {
			hd.Set("Strict-Transport-Security", "max-age=31536000")
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			hd.Set("Cache-Control", "no-store")
		} else {
			// Nach einem Update sofort die neue Oberfläche ausliefern.
			hd.Set("Cache-Control", "no-cache")
		}
		h.ServeHTTP(w, r)
	})
}

// static liefert die eingebettete Oberfläche; unbekannte Pfade → index.html.
func (s *Server) static() http.Handler {
	files := http.FileServerFS(s.Static)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/ws/") {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path != "/" {
			if _, err := fs.Stat(s.Static, strings.TrimPrefix(r.URL.Path, "/")); err != nil {
				r.URL.Path = "/"
			}
		}
		files.ServeHTTP(w, r)
	})
}

// protect verlangt eine gültige Sitzung und bei schreibenden Zugriffen das CSRF-Token.
func (s *Server) protect(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookieName)
		var sess *auth.Session
		if err == nil {
			sess = s.Sessions.Get(c.Value)
		}
		if sess == nil {
			jsonErr(w, http.StatusUnauthorized, "Nicht angemeldet")
			return
		}
		if r.Method != http.MethodGet && !auth.CheckCSRF(sess, r.Header.Get("X-CSRF-Token")) {
			jsonErr(w, http.StatusForbidden, "CSRF-Token fehlt oder ist ungültig")
			return
		}
		if ok, why := s.Access.For(sess.User).allowed(r); !ok {
			s.Events.Add(AuditEvent{User: sess.User, Addr: clientAddr(r), Kind: "verweigert", Result: "fehler", Text: r.Method + " " + r.URL.Path, Error: why})
			s.alert("access_denied", sess.User, fmt.Sprintf("%s (%s) wurde verweigert: %s %s – %s", sess.User, clientAddr(r), r.Method, r.URL.Path, why))
			jsonErr(w, http.StatusForbidden, "Keine Berechtigung: "+why)
			return
		}
		h(w, r.WithContext(withSession(r.Context(), sess)))
	})
}

func clientAddr(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

type loginReq struct {
	User string `json:"user"`
	Pass string `json:"password"`
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	addr := clientAddr(r)
	if s.Sessions.Throttled(addr) {
		jsonErr(w, http.StatusTooManyRequests, "Zu viele Fehlversuche. Bitte in einigen Minuten erneut versuchen.")
		return
	}
	var req loginReq
	if !decode(w, r, &req) {
		return
	}
	err := s.Auth.Verify(r.Context(), req.User, req.Pass)
	if err == nil && s.AdminGroup != "" && !s.Host.InGroup(req.User, s.AdminGroup) {
		err = auth.ErrDenied
	}
	if err != nil {
		s.Sessions.Fail(addr)
		s.Audit.Printf("login fehlgeschlagen user=%q addr=%s", req.User, addr)
		s.Events.Add(AuditEvent{User: req.User, Addr: addr, Kind: "anmeldung", Result: "fehler", Text: "Anmeldung fehlgeschlagen"})
		s.alert("login_failed", addr, fmt.Sprintf("Anmeldung als %q von %s fehlgeschlagen.", req.User, addr))
		time.Sleep(700 * time.Millisecond)
		jsonErr(w, http.StatusUnauthorized, "Benutzername oder Passwort falsch – oder das Konto ist nicht in der Gruppe "+s.AdminGroup+".")
		return
	}
	sess := s.Sessions.Create(req.User, addr)
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: sess.ID, Path: "/", HttpOnly: true, Secure: s.Secure, SameSite: http.SameSiteStrictMode})
	s.Audit.Printf("login user=%s addr=%s", req.User, addr)
	s.Events.Add(AuditEvent{User: req.User, Addr: addr, Kind: "anmeldung", Result: "ok", Text: "Anmeldung"})
	s.writeSession(w, r, sess)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		if sess := s.Sessions.Get(c.Value); sess != nil {
			if !auth.CheckCSRF(sess, r.Header.Get("X-CSRF-Token")) {
				jsonErr(w, http.StatusForbidden, "CSRF-Token fehlt")
				return
			}
			s.Audit.Printf("logout user=%s", sess.User)
		}
		s.Sessions.Delete(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.Secure, SameSite: http.SameSiteStrictMode})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(cookieName)
	if err != nil || s.Sessions.Get(c.Value) == nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]any{"error": "Nicht angemeldet", "demo": s.Host.Demo})
		return
	}
	s.writeSession(w, r, s.Sessions.Get(c.Value))
}

func (s *Server) writeSession(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	host := s.Host.Hostname(r.Context())
	g := s.Access.For(sess.User)
	writeJSON(w, map[string]any{"user": sess.User, "csrf": sess.CSRF, "hostname": host, "demo": s.Host.Demo, "version": s.Version, "role": g.Role, "vms": g.VMs})
}

// ---- Hilfsfunktionen ----

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}

func jsonErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20))
	if err := dec.Decode(v); err != nil {
		jsonErr(w, http.StatusBadRequest, "Ungültige Anfrage: "+err.Error())
		return false
	}
	return true
}

// fail bildet Fehler auf HTTP-Statuscodes ab.
func fail(w http.ResponseWriter, err error) {
	var ce *obsd.CheckError
	var cmd *obsd.CmdError
	switch {
	case errors.As(err, &ce):
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]string{"error": "Syntaxprüfung fehlgeschlagen – Datei wurde nicht gespeichert.", "check": ce.Output})
	case errors.Is(err, obsd.ErrInvalid):
		jsonErr(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, obsd.ErrConflict):
		jsonErr(w, http.StatusConflict, err.Error())
	case errors.As(err, &cmd):
		jsonErr(w, http.StatusBadGateway, err.Error())
	default:
		jsonErr(w, http.StatusInternalServerError, err.Error())
	}
}

// respond schreibt v oder den Fehler.
func respond(w http.ResponseWriter, v any, err error) {
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, v)
}

// alert meldet ein Ereignis an die Mail-Benachrichtigung (falls aktiviert).
func (s *Server) alert(kind, subject, message string) {
	if s.Metrics != nil {
		s.Metrics.Alerts.Event(kind, subject, message)
	}
}
