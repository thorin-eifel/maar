// Package obsd kapselt alle Zugriffe auf das OpenBSD-System: Programmaufrufe
// (immer mit Argumentlisten, nie über eine Shell), Konfigurationsdateien und
// das Parsen der Ausgaben der Basiswerkzeuge.
package obsd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// Runner führt Programme aus. Die Produktion nutzt ExecRunner, der Demo-Modus
// einen simulierten Runner mit realistischen OpenBSD-Ausgaben.
type Runner interface {
	Run(ctx context.Context, stdin []byte, name string, args ...string) (Result, error)
	Stream(ctx context.Context, w io.Writer, name string, args ...string) error
}

type Result struct {
	Stdout []byte
	Stderr []byte
	Code   int
}

// CmdError trägt die Fehlerausgabe eines Programms bis in die Oberfläche.
type CmdError struct {
	Cmd    string
	Code   int
	Stderr string
}

func (e *CmdError) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		msg = fmt.Sprintf("Exit-Status %d", e.Code)
	}
	return e.Cmd + ": " + msg
}

type ExecRunner struct {
	Timeout time.Duration
}

func (r ExecRunner) Run(ctx context.Context, stdin []byte, name string, args ...string) (Result, error) {
	if r.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.Timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = safeEnv()
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	err := cmd.Run()
	res := Result{Stdout: out.Bytes(), Stderr: errb.Bytes()}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			res.Code = ee.ExitCode()
			return res, &CmdError{Cmd: name, Code: res.Code, Stderr: errb.String()}
		}
		return res, &CmdError{Cmd: name, Code: -1, Stderr: err.Error()}
	}
	return res, nil
}

func (r ExecRunner) Stream(ctx context.Context, w io.Writer, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = safeEnv()
	cmd.Stdout, cmd.Stderr = w, w
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return &CmdError{Cmd: name, Code: ee.ExitCode()}
		}
		return err
	}
	return nil
}

func safeEnv() []string {
	return []string{
		"PATH=/bin:/sbin:/usr/bin:/usr/sbin:/usr/local/bin:/usr/local/sbin",
		"LC_ALL=C",
		"HOME=/root",
		"TERM=dumb",
	}
}
