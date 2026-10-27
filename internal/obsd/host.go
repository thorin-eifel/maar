package obsd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Host bündelt Runner und Dateisystemwurzel. Root ist in Produktion "/",
// im Demo-Modus ein Sandbox-Verzeichnis mit Beispieldateien.
type Host struct {
	R         Runner
	Root      string
	VMDir     string // Ablage für Disk-Images, Standard /var/vmd
	BackupDir string // Sicherungen geänderter Konfigurationsdateien
	Demo      bool

	confMu   sync.Mutex // serialisiert Änderungen an vm.conf
	snapMu   sync.Mutex // serialisiert Snapshot-Vorgänge
	backupMu sync.Mutex // schützt backup.json
	netMu    sync.Mutex // schützt network.json und die daraus erzeugten Dateien

	hookMu   sync.Mutex
	onEvent  func(kind, subject, message string)
	expected map[string]time.Time // gewollte VM-Stopps
}

func NewHost(r Runner, root string, demo bool) *Host {
	return &Host{R: r, Root: root, VMDir: "/var/vmd", BackupDir: "/var/maar/backups", Demo: demo}
}

// P bildet einen absoluten Systempfad auf das (ggf. Sandbox-)Dateisystem ab.
func (h *Host) P(p string) string {
	if h.Root == "" || h.Root == "/" {
		return p
	}
	return filepath.Join(h.Root, p)
}

func (h *Host) out(ctx context.Context, name string, args ...string) (string, error) {
	res, err := h.R.Run(ctx, nil, name, args...)
	return string(res.Stdout), err
}

// outLenient liefert stdout auch bei Exit-Status ≠ 0 (z. B. rcctl ls failed).
func (h *Host) outLenient(ctx context.Context, name string, args ...string) string {
	res, _ := h.R.Run(ctx, nil, name, args...)
	return string(res.Stdout)
}

func (h *Host) run(ctx context.Context, name string, args ...string) error {
	_, err := h.R.Run(ctx, nil, name, args...)
	return err
}

func (h *Host) readFile(p string) (string, error) {
	b, err := os.ReadFile(h.P(p))
	return string(b), err
}

// ErrInvalid kennzeichnet ungültige Eingaben (HTTP 400).
var ErrInvalid = errors.New("ungültige Eingabe")

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

var (
	reName    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	reUser    = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,30}$`)
	reOwner   = regexp.MustCompile(`^([a-z_][a-z0-9_-]{0,30}(:[a-z_][a-z0-9_-]{0,30})?|:[a-z_][a-z0-9_-]{0,30})$`) // user, user:group, :group
	reSize    = regexp.MustCompile(`^[0-9]{1,6}[KMGT]?$`)
	reIface   = regexp.MustCompile(`^[a-z]{1,12}[0-9]{1,4}$`)
	rePkg     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+%-]{0,127}$`)
	reService = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)
	reTable   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,31}$`)
	reAddr    = regexp.MustCompile(`^[0-9A-Fa-f:.]+(/[0-9]{1,3})?$`)
	reSysctl  = regexp.MustCompile(`^[a-z0-9_.]{1,128}$`)
)

func checkName(kind, v string, re *regexp.Regexp) error {
	if !re.MatchString(v) {
		return invalid("%s %q", kind, v)
	}
	return nil
}

// parseSize wandelt Größen wie 412M, 1.5G oder 2047K in Bytes.
func parseSize(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" {
		return 0
	}
	mult := int64(1)
	switch s[len(s)-1] {
	case 'K', 'k':
		mult = 1 << 10
	case 'M':
		mult = 1 << 20
	case 'G':
		mult = 1 << 30
	case 'T':
		mult = 1 << 40
	}
	if mult > 1 || s[len(s)-1] == 'B' {
		s = s[:len(s)-1]
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return int64(f * float64(mult))
}

func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, strings.TrimRight(l, "\r"))
		}
	}
	return out
}
