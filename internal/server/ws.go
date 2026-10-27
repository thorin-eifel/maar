package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/coder/websocket"
	"github.com/thorin-eifel/maar/internal/console"
)

type ctrl struct {
	Type    string `json:"type"`
	Cols    int    `json:"cols,omitempty"`
	Rows    int    `json:"rows,omitempty"`
	Message string `json:"message,omitempty"`
}

// vmConsole verbindet den Browser mit der seriellen Konsole einer VM.
// Mehrere Zuschauer teilen sich dieselbe Konsole.
func (s *Server) vmConsole(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	tty, err := s.Host.VMTTY(r.Context(), name)
	if err != nil {
		fail(w, err)
		return
	}
	term, err := s.Hub.Attach("vm:"+name, func() (io.ReadWriteCloser, error) {
		if s.Host.Demo {
			return console.NewDemoSerial(name), nil
		}
		return console.OpenTTY(tty)
	})
	if err != nil {
		fail(w, err)
		return
	}
	s.audit(r, nil, "console vm %s (%s)", name, tty)
	s.pumpTerm(w, r, term, "Verbunden mit "+name+" über "+tty+" (115200 Baud). Konsole wird mit anderen Zuschauern geteilt.")
}

// shellConsole startet eine Login-Shell als angemeldeter Benutzer.
func (s *Server) shellConsole(w http.ResponseWriter, r *http.Request) {
	user := sessionFrom(r).User
	cols, _ := strconv.Atoi(r.URL.Query().Get("cols"))
	rows, _ := strconv.Atoi(r.URL.Query().Get("rows"))
	if cols <= 0 || rows <= 0 {
		cols, rows = 120, 32
	}
	var cmd *exec.Cmd
	if s.Host.Demo {
		sh := os.Getenv("SHELL")
		if sh == "" {
			sh = "/bin/sh"
		}
		cmd = exec.Command(sh, "-l")
		cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	} else {
		cmd = exec.Command("/usr/bin/su", "-l", user)
		cmd.Env = []string{"TERM=xterm-256color", "PATH=/bin:/usr/bin:/sbin:/usr/sbin"}
	}
	term, err := console.StartShell(cmd, cols, rows)
	if err != nil {
		fail(w, err)
		return
	}
	s.audit(r, nil, "console shell")
	s.pumpTerm(w, r, term, "")
}

func (s *Server) pumpTerm(w http.ResponseWriter, r *http.Request, term console.Term, banner string) {
	defer term.Close()
	// Accept prüft standardmäßig, dass Origin und Host übereinstimmen.
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	defer c.CloseNow()
	c.SetReadLimit(1 << 20)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	send := func(v ctrl) {
		b, _ := json.Marshal(v)
		wctx, wc := context.WithTimeout(ctx, 5*time.Second)
		c.Write(wctx, websocket.MessageText, b)
		wc()
	}
	if banner != "" {
		send(ctrl{Type: "info", Message: banner})
	}

	// Browser → Terminal
	go func() {
		defer cancel()
		for {
			typ, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			if typ == websocket.MessageBinary {
				if _, err := term.Write(data); err != nil {
					return
				}
				continue
			}
			var m ctrl
			if json.Unmarshal(data, &m) == nil && m.Type == "resize" {
				term.Resize(m.Cols, m.Rows)
			}
		}
	}()

	// Terminal → Browser
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-term.Done():
			// Restliche Ausgabe noch zustellen.
			for {
				select {
				case b := <-term.Output():
					c.Write(ctx, websocket.MessageBinary, b)
					continue
				default:
				}
				break
			}
			send(ctrl{Type: "closed", Message: term.Reason()})
			c.Close(websocket.StatusNormalClosure, "")
			return
		case b := <-term.Output():
			wctx, wc := context.WithTimeout(ctx, 10*time.Second)
			err := c.Write(wctx, websocket.MessageBinary, b)
			wc()
			if err != nil {
				return
			}
		case <-ping.C:
			pctx, pc := context.WithTimeout(ctx, 10*time.Second)
			err := c.Ping(pctx)
			pc()
			if err != nil {
				return
			}
		}
	}
}
