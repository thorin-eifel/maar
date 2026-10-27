package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/thorin-eifel/maar/internal/auth"
	"github.com/thorin-eifel/maar/internal/obsd"
)

func withSession(ctx context.Context, s *auth.Session) context.Context {
	return context.WithValue(ctx, ctxKey{}, s)
}

func sessionFrom(r *http.Request) *auth.Session {
	s, _ := r.Context().Value(ctxKey{}).(*auth.Session)
	return s
}

// audit protokolliert jede verändernde Aktion mit Benutzer und Quelle.
func (s *Server) audit(r *http.Request, err error, format string, a ...any) {
	user := "?"
	if sess := sessionFrom(r); sess != nil {
		user = sess.User
	}
	status := "ok"
	if err != nil {
		status = "fehler: " + strings.ReplaceAll(err.Error(), "\n", " ")
	}
	text := fmt.Sprintf(format, a...)
	s.Audit.Printf("user=%s addr=%s %s → %s", user, clientAddr(r), text, status)
	ev := AuditEvent{User: user, Addr: clientAddr(r), Kind: "aktion", Result: "ok", Text: text}
	if err != nil {
		ev.Result, ev.Error = "fehler", strings.ReplaceAll(err.Error(), "\n", " ")
	}
	s.Events.Add(ev)
}

type msg struct {
	Message string `json:"message,omitempty"`
	Output  string `json:"output,omitempty"`
	Job     string `json:"job,omitempty"`
}

// ---- System ----

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	o, err := s.Host.Overview(ctx)
	if err != nil {
		fail(w, err)
		return
	}
	res := map[string]any{"system": o}
	if st, err := s.Host.Storage(ctx); err == nil {
		res["storage"] = st.Mounts
	}
	if vms, err := s.Host.VMs(ctx); err == nil {
		res["vms"] = vms.VMs
		res["vmdRunning"] = vms.VMDRun
	}
	if svc, err := s.Host.Services(ctx); err == nil {
		var failed []string
		running := 0
		for _, x := range svc {
			if x.Failed {
				failed = append(failed, x.Name)
			}
			if x.Running {
				running++
			}
		}
		res["servicesFailed"] = failed
		res["servicesRunning"] = running
	}
	if pf, err := s.Host.PF(ctx); err == nil {
		res["pf"] = map[string]any{"enabled": pf.Enabled, "states": pf.States, "rules": len(pf.Rules)}
	}
	if n, err := s.Host.Network(ctx); err == nil {
		res["interfaces"] = n.Interfaces
		res["gateway"] = n.Gateway
	}
	if s.Metrics != nil {
		active, _ := s.Metrics.Alerts.Snapshot()
		res["alerts"] = active
	}
	writeJSON(w, res)
}

func (s *Server) storage(w http.ResponseWriter, r *http.Request) {
	st, err := s.Host.Storage(r.Context())
	respond(w, st, err)
}

func (s *Server) disklabel(w http.ResponseWriter, r *http.Request) {
	out, err := s.Host.Disklabel(r.Context(), r.PathValue("disk"))
	respond(w, msg{Output: out}, err)
}

func (s *Server) procs(w http.ResponseWriter, r *http.Request) {
	p, err := s.Host.Procs(r.Context())
	respond(w, p, err)
}

func (s *Server) kill(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Signal string `json:"signal"`
	}
	if !decode(w, r, &req) {
		return
	}
	pid, _ := strconv.Atoi(r.PathValue("pid"))
	err := s.Host.Kill(r.Context(), pid, req.Signal)
	s.audit(r, err, "kill -%s %d", req.Signal, pid)
	respond(w, msg{Message: fmt.Sprintf("Signal %s an %d gesendet.", req.Signal, pid)}, err)
}

func (s *Server) sysctls(w http.ResponseWriter, r *http.Request) {
	l, err := s.Host.Sysctls(r.Context())
	respond(w, l, err)
}

func (s *Server) setSysctl(w http.ResponseWriter, r *http.Request) {
	var req obsd.Sysctl
	if !decode(w, r, &req) {
		return
	}
	err := s.Host.SetSysctl(r.Context(), req.Name, req.Value)
	s.audit(r, err, "sysctl %s=%s", req.Name, req.Value)
	respond(w, msg{Message: req.Name + " gesetzt (bis zum Neustart; dauerhaft über /etc/sysctl.conf)."}, err)
}

func (s *Server) power(w http.ResponseWriter, r *http.Request) {
	a := r.PathValue("action")
	err := s.Host.Power(r.Context(), a)
	s.audit(r, err, "power %s", a)
	texts := map[string]string{"reboot": "Neustart in einer Minute.", "halt": "Ausschalten in einer Minute.", "cancel": "Geplantes Herunterfahren abgebrochen."}
	respond(w, msg{Message: texts[a]}, err)
}

// ---- VMs ----

func (s *Server) vms(w http.ResponseWriter, r *http.Request) {
	v, err := s.Host.VMs(r.Context())
	if err == nil {
		if g := s.Access.For(sessionFrom(r).User); len(g.VMs) > 0 && g.Role != "admin" {
			keep := v.VMs[:0:0]
			for _, m := range v.VMs {
				if contains(g.VMs, m.Name) {
					keep = append(keep, m)
				}
			}
			v.VMs = keep
		}
		viewers := s.Hub.Viewers()
		res := map[string]any{"overview": v, "viewers": viewers}
		writeJSON(w, res)
		return
	}
	fail(w, err)
}

func (s *Server) createVM(w http.ResponseWriter, r *http.Request) {
	var spec obsd.VMSpec
	if !decode(w, r, &spec) {
		return
	}
	err := s.Host.CreateVM(r.Context(), &spec)
	s.audit(r, err, "vm create %s mem=%s disk=%s", spec.Name, spec.Memory, spec.DiskSize)
	respond(w, msg{Message: "VM " + spec.Name + " angelegt."}, err)
}

func (s *Server) updateVM(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Block string `json:"block"`
	}
	if !decode(w, r, &req) {
		return
	}
	name := r.PathValue("name")
	err := s.Host.UpdateVMBlock(r.Context(), name, req.Block)
	s.audit(r, err, "vm config %s", name)
	respond(w, msg{Message: "Konfiguration von " + name + " gespeichert und neu geladen. Änderungen greifen beim nächsten Start."}, err)
}

func (s *Server) deleteVM(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	disks := r.URL.Query().Get("disks") == "1"
	err := s.Host.DeleteVM(r.Context(), name, disks)
	s.audit(r, err, "vm delete %s disks=%v", name, disks)
	if err == nil {
		s.Host.ForgetVMNet(r.Context(), name)
	}
	respond(w, msg{Message: "VM " + name + " entfernt."}, err)
}

func (s *Server) vmAction(w http.ResponseWriter, r *http.Request) {
	var opt map[string]string
	if r.ContentLength > 0 && !decode(w, r, &opt) {
		return
	}
	name, a := r.PathValue("name"), r.PathValue("action")
	err := s.Host.VMAction(r.Context(), name, a, opt)
	s.audit(r, err, "vm %s %s", a, name)
	texts := map[string]string{"start": "gestartet", "stop": "wird heruntergefahren", "kill": "hart beendet", "pause": "pausiert", "unpause": "fortgesetzt"}
	respond(w, msg{Message: "VM " + name + " " + texts[a] + "."}, err)
}

func (s *Server) createDisk(w http.ResponseWriter, r *http.Request) {
	var req struct{ Name, Size, Format, Base, Store string }
	if !decode(w, r, &req) {
		return
	}
	err := s.Host.CreateDisk(r.Context(), req.Name, req.Size, req.Format, req.Base, req.Store)
	s.audit(r, err, "vm disk create %s size=%s base=%s store=%s", req.Name, req.Size, req.Base, req.Store)
	respond(w, msg{Message: "Disk-Image angelegt."}, err)
}

func (s *Server) deleteImage(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	err := s.Host.DeleteImage(r.Context(), p)
	s.audit(r, err, "vm image delete %s", p)
	respond(w, msg{Message: p + " gelöscht."}, err)
}

func (s *Server) downloadISO(w http.ResponseWriter, r *http.Request) {
	var req struct{ URL, Name string }
	if !decode(w, r, &req) {
		return
	}
	u, err := url.Parse(req.URL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || strings.ContainsAny(req.URL, " \n\"'") {
		fail(w, fmt.Errorf("%w: nur http(s)-Adressen", obsd.ErrInvalid))
		return
	}
	if req.Name == "" {
		req.Name = u.Path[strings.LastIndex(u.Path, "/")+1:]
	}
	dst, err := s.Host.ISOPath(req.Name)
	if err != nil {
		fail(w, err)
		return
	}
	j := s.Jobs.Start(sessionFrom(r).User, &obsd.JobCommand{Title: "ISO laden: " + req.Name, Name: "ftp", Args: []string{"-o", dst, req.URL}})
	s.audit(r, nil, "iso download %s → %s", req.URL, dst)
	writeJSON(w, msg{Job: j.ID, Message: "Download gestartet."})
}

func (s *Server) uploadISO(w http.ResponseWriter, r *http.Request) {
	dst, err := s.Host.ISOPath(r.URL.Query().Get("name"))
	if err != nil {
		fail(w, err)
		return
	}
	real := s.Host.P(dst)
	if _, err := os.Stat(real); err == nil {
		fail(w, fmt.Errorf("%w: %s existiert bereits", obsd.ErrInvalid, dst))
		return
	}
	tmp := real + ".upload"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		fail(w, err)
		return
	}
	n, err := io.Copy(f, r.Body)
	f.Close()
	if err == nil {
		err = os.Rename(tmp, real)
	}
	if err != nil {
		os.Remove(tmp)
	}
	s.audit(r, err, "iso upload %s (%d Bytes)", dst, n)
	respond(w, msg{Message: fmt.Sprintf("%s hochgeladen (%d MB).", dst, n>>20)}, err)
}

func (s *Server) addSwitch(w http.ResponseWriter, r *http.Request) {
	var req struct{ Name, Interface string }
	if !decode(w, r, &req) {
		return
	}
	err := s.Host.AddSwitch(r.Context(), req.Name, req.Interface)
	s.audit(r, err, "vm switch add %s %s", req.Name, req.Interface)
	respond(w, msg{Message: "Switch " + req.Name + " angelegt."}, err)
}

// ---- Dienste ----

func (s *Server) services(w http.ResponseWriter, r *http.Request) {
	l, err := s.Host.Services(r.Context())
	respond(w, l, err)
}

func (s *Server) service(w http.ResponseWriter, r *http.Request) {
	d, err := s.Host.Service(r.Context(), r.PathValue("name"))
	respond(w, d, err)
}

func (s *Server) serviceAction(w http.ResponseWriter, r *http.Request) {
	name, a := r.PathValue("name"), r.PathValue("action")
	out, err := s.Host.ServiceAction(r.Context(), name, a)
	s.audit(r, err, "rcctl %s %s", a, name)
	respond(w, msg{Message: "rcctl " + a + " " + name, Output: out}, err)
}

func (s *Server) setFlags(w http.ResponseWriter, r *http.Request) {
	var req struct{ Flags string }
	if !decode(w, r, &req) {
		return
	}
	name := r.PathValue("name")
	err := s.Host.SetServiceFlags(r.Context(), name, req.Flags)
	s.audit(r, err, "rcctl set %s flags %s", name, req.Flags)
	respond(w, msg{Message: "Flags für " + name + " gesetzt. Neustart des Dienstes übernimmt sie."}, err)
}

// ---- Netzwerk & pf ----

func (s *Server) network(w http.ResponseWriter, r *http.Request) {
	n, err := s.Host.Network(r.Context())
	respond(w, n, err)
}

func (s *Server) ifaceAction(w http.ResponseWriter, r *http.Request) {
	i, a := r.PathValue("iface"), r.PathValue("action")
	out, err := s.Host.IfaceAction(r.Context(), i, a)
	s.audit(r, err, "iface %s %s", i, a)
	respond(w, msg{Message: i + ": " + a, Output: out}, err)
}

func (s *Server) pf(w http.ResponseWriter, r *http.Request) {
	p, err := s.Host.PF(r.Context())
	respond(w, p, err)
}

func (s *Server) pfStates(w http.ResponseWriter, r *http.Request) {
	l, err := s.Host.PFStates(r.Context(), 2000)
	respond(w, l, err)
}

func (s *Server) pfTable(w http.ResponseWriter, r *http.Request) {
	l, err := s.Host.PFTable(r.Context(), r.PathValue("table"))
	if l == nil {
		l = []string{}
	}
	respond(w, l, err)
}

func (s *Server) pfTableEdit(w http.ResponseWriter, r *http.Request) {
	var req struct{ Op, Addr string }
	if !decode(w, r, &req) {
		return
	}
	t := r.PathValue("table")
	err := s.Host.PFTableEdit(r.Context(), t, req.Op, req.Addr)
	s.audit(r, err, "pfctl -t %s -T %s %s", t, req.Op, req.Addr)
	respond(w, msg{Message: "Tabelle <" + t + "> aktualisiert."}, err)
}

func (s *Server) pfAction(w http.ResponseWriter, r *http.Request) {
	a := r.PathValue("action")
	out, err := s.Host.PFAction(r.Context(), a)
	s.audit(r, err, "pf %s", a)
	respond(w, msg{Message: "pf: " + a, Output: out}, err)
}

// ---- Pakete ----

func (s *Server) packages(w http.ResponseWriter, r *http.Request) {
	l, err := s.Host.Packages(r.Context())
	respond(w, l, err)
}

func (s *Server) searchPackages(w http.ResponseWriter, r *http.Request) {
	l, err := s.Host.SearchPackages(r.Context(), r.URL.Query().Get("q"))
	respond(w, l, err)
}

func (s *Server) patches(w http.ResponseWriter, r *http.Request) {
	p, err := s.Host.Patches(r.Context())
	respond(w, p, err)
}

func (s *Server) pkgJob(w http.ResponseWriter, r *http.Request) {
	var req struct{ Names []string }
	if r.ContentLength > 0 && !decode(w, r, &req) {
		return
	}
	a := r.PathValue("action")
	c, err := s.Host.PkgJob(a, req.Names)
	if err != nil {
		fail(w, err)
		return
	}
	j := s.Jobs.Start(sessionFrom(r).User, c)
	s.audit(r, nil, "job %s: %s", j.ID, j.Command)
	writeJSON(w, msg{Job: j.ID, Message: c.Title + " gestartet."})
}

// ---- Benutzer ----

func (s *Server) users(w http.ResponseWriter, r *http.Request) {
	u, err := s.Host.Users()
	if err != nil {
		fail(w, err)
		return
	}
	g, err := s.Host.Groups()
	respond(w, map[string]any{"users": u, "groups": g}, err)
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var spec obsd.UserSpec
	if !decode(w, r, &spec) {
		return
	}
	err := s.Host.CreateUser(r.Context(), &spec)
	s.audit(r, err, "useradd %s groups=%v", spec.Name, spec.Groups)
	respond(w, msg{Message: "Benutzer " + spec.Name + " angelegt."}, err)
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	var spec obsd.UserSpec
	if !decode(w, r, &spec) {
		return
	}
	spec.Name = r.PathValue("name")
	err := s.Host.UpdateUser(r.Context(), &spec)
	s.audit(r, err, "usermod %s groups=%v shell=%s", spec.Name, spec.Groups, spec.Shell)
	respond(w, msg{Message: "Benutzer " + spec.Name + " aktualisiert."}, err)
}

func (s *Server) setPassword(w http.ResponseWriter, r *http.Request) {
	var req struct{ Password string }
	if !decode(w, r, &req) {
		return
	}
	name := r.PathValue("name")
	err := s.Host.SetPassword(r.Context(), name, req.Password)
	s.audit(r, err, "passwd %s", name)
	respond(w, msg{Message: "Passwort für " + name + " gesetzt."}, err)
}

func (s *Server) userAction(w http.ResponseWriter, r *http.Request) {
	name, a := r.PathValue("name"), r.PathValue("action")
	if sess := sessionFrom(r); sess != nil && sess.User == name && a != "unlock" {
		fail(w, fmt.Errorf("%w: das eigene Konto kann nicht gesperrt oder gelöscht werden", obsd.ErrInvalid))
		return
	}
	err := s.Host.UserAction(r.Context(), name, a)
	s.audit(r, err, "user %s %s", a, name)
	texts := map[string]string{"lock": "gesperrt", "unlock": "entsperrt", "delete": "gelöscht"}
	respond(w, msg{Message: "Benutzer " + name + " " + texts[a] + "."}, err)
}

func (s *Server) createGroup(w http.ResponseWriter, r *http.Request) {
	var req struct{ Name string }
	if !decode(w, r, &req) {
		return
	}
	err := s.Host.GroupAction(r.Context(), req.Name, "create")
	s.audit(r, err, "groupadd %s", req.Name)
	respond(w, msg{Message: "Gruppe " + req.Name + " angelegt."}, err)
}

func (s *Server) deleteGroup(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	err := s.Host.GroupAction(r.Context(), name, "delete")
	s.audit(r, err, "groupdel %s", name)
	respond(w, msg{Message: "Gruppe " + name + " gelöscht."}, err)
}

func (s *Server) crontab(w http.ResponseWriter, r *http.Request) {
	c, err := s.Host.Crontab(r.Context(), r.PathValue("user"))
	respond(w, map[string]string{"content": c}, err)
}

func (s *Server) setCrontab(w http.ResponseWriter, r *http.Request) {
	var req struct{ Content string }
	if !decode(w, r, &req) {
		return
	}
	u := r.PathValue("user")
	err := s.Host.SetCrontab(r.Context(), u, req.Content)
	s.audit(r, err, "crontab %s", u)
	respond(w, msg{Message: "crontab von " + u + " gespeichert."}, err)
}

// ---- Dateien ----

func (s *Server) files(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.Host.ConfigFiles())
}

func (s *Server) readFile(w http.ResponseWriter, r *http.Request) {
	f, err := s.Host.ReadConfig(r.URL.Query().Get("path"))
	respond(w, f, err)
}

func (s *Server) writeFile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
		MTime   int64  `json:"mtime,string"`
	}
	if !decode(w, r, &req) {
		return
	}
	res, err := s.Host.WriteConfig(r.Context(), req.Path, req.Content, req.MTime)
	s.audit(r, err, "file write %s", req.Path)
	respond(w, res, err)
}

func (s *Server) backups(w http.ResponseWriter, r *http.Request) {
	l, err := s.Host.Backups(r.URL.Query().Get("path"))
	respond(w, l, err)
}

func (s *Server) backup(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	c, err := s.Host.ReadBackup(q.Get("path"), q.Get("id"))
	respond(w, map[string]string{"content": c}, err)
}

func (s *Server) applyFile(w http.ResponseWriter, r *http.Request) {
	var req struct{ Path string }
	if !decode(w, r, &req) {
		return
	}
	out, err := s.Host.ApplyConfig(r.Context(), req.Path)
	s.audit(r, err, "file apply %s", req.Path)
	respond(w, msg{Message: "Übernommen.", Output: out}, err)
}

// ---- Logs ----

func (s *Server) logs(w http.ResponseWriter, r *http.Request) {
	l, err := s.Host.LogFiles()
	respond(w, l, err)
}

func (s *Server) log(w http.ResponseWriter, r *http.Request) {
	p, err := s.Host.LogPath(r.URL.Query().Get("name"))
	if err != nil {
		fail(w, err)
		return
	}
	n, _ := strconv.Atoi(r.URL.Query().Get("lines"))
	if n <= 0 || n > 20000 {
		n = 500
	}
	l, size, err := obsd.TailFile(p, n)
	respond(w, map[string]any{"lines": l, "offset": size}, err)
}

// followLog streamt neue Zeilen per Server-Sent Events.
func (s *Server) followLog(w http.ResponseWriter, r *http.Request) {
	p, err := s.Host.LogPath(r.URL.Query().Get("name"))
	if err != nil {
		fail(w, err)
		return
	}
	off, _ := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	fl, ok := w.(http.Flusher)
	if !ok {
		jsonErr(w, 500, "Streaming nicht unterstützt")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	var partial string
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
		}
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		st, _ := f.Stat()
		if st.Size() < off {
			off = 0 // rotiert
			fmt.Fprintf(w, "event: rotated\ndata: {}\n\n")
		}
		if st.Size() > off {
			b := make([]byte, min(st.Size()-off, 1<<20))
			n, _ := f.ReadAt(b, off)
			off += int64(n)
			text := partial + string(b[:n])
			cut := strings.LastIndexByte(text, '\n')
			if cut >= 0 {
				for _, l := range strings.Split(text[:cut], "\n") {
					fmt.Fprintf(w, "data: %s\n\n", strconv.Quote(l))
				}
				partial = text[cut+1:]
			} else {
				partial = text
			}
		} else {
			fmt.Fprint(w, ": ping\n\n")
		}
		f.Close()
		fl.Flush()
	}
}

// ---- Jobs ----

func (s *Server) jobList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.Jobs.List())
}

func (s *Server) jobStream(w http.ResponseWriter, r *http.Request) {
	j := s.Jobs.Get(r.PathValue("id"))
	if j == nil {
		jsonErr(w, http.StatusNotFound, "Job nicht gefunden")
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		jsonErr(w, 500, "Streaming nicht unterstützt")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	ch, unsub := j.Subscribe()
	defer unsub()
	off := 0
	for {
		out, n, running := j.Output(off)
		off = n
		if len(out) > 0 {
			fmt.Fprintf(w, "data: %s\n\n", strconv.Quote(string(out)))
		}
		if !running {
			x := j.Snapshot()
			fmt.Fprintf(w, "event: end\ndata: %s\n\n", strconv.Quote(string(x.State)+"|"+x.Error))
			fl.Flush()
			return
		}
		fl.Flush()
		select {
		case <-r.Context().Done():
			return
		case <-ch:
		case <-time.After(15 * time.Second):
			fmt.Fprint(w, ": ping\n\n")
		}
	}
}

func (s *Server) jobCancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ok := s.Jobs.Cancel(id)
	s.audit(r, nil, "job cancel %s", id)
	if !ok {
		jsonErr(w, http.StatusNotFound, "Job nicht gefunden")
		return
	}
	writeJSON(w, msg{Message: "Job wird abgebrochen."})
}
