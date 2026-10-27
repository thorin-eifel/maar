package obsd

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Konfiguration der Sicherung: Ziele, Jobs mit Zeitplan und Aufbewahrung,
// Laufverlauf. Gespeichert in /var/maar/backup.json (0600).

const backupConfPath = "/var/maar/backup.json"

type BackupTarget struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"` // local, nfs, sftp
	Path string `json:"path"` // local: Verzeichnis; nfs: server:/export; sftp: entferntes Verzeichnis
	Host string `json:"host,omitempty"`
	User string `json:"user,omitempty"`
	Port int    `json:"port,omitempty"`
}

type Schedule struct {
	Kind    string `json:"kind"` // manual, hourly, daily, weekly
	Time    string `json:"time"` // HH:MM (bei hourly zählt nur MM)
	Weekday int    `json:"weekday"`
}

type BackupJob struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	VMs      []string `json:"vms"` // leer = alle VMs aus vm.conf
	Target   string   `json:"target"`
	Mode     string   `json:"mode"` // snapshot, offline, pause
	Schedule Schedule `json:"schedule"`
	Keep     int      `json:"keep"`
	Enabled  bool     `json:"enabled"`
}

type BackupRun struct {
	Job     string `json:"job"`
	Name    string `json:"name"`
	Trigger string `json:"trigger"`
	TaskID  string `json:"task"`
	Started int64  `json:"started"`
	Ended   int64  `json:"ended,omitempty"`
	State   string `json:"state"`
	Error   string `json:"error,omitempty"`
}

type BackupConfig struct {
	Targets []*BackupTarget  `json:"targets"`
	Jobs    []*BackupJob     `json:"jobs"`
	LastRun map[string]int64 `json:"lastRun"`
	History []BackupRun      `json:"history"`
}

func (h *Host) loadBackupConfig() (*BackupConfig, error) {
	c := &BackupConfig{Targets: []*BackupTarget{}, Jobs: []*BackupJob{}, LastRun: map[string]int64{}, History: []BackupRun{}}
	b, err := os.ReadFile(h.P(backupConfPath))
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("backup.json: %w", err)
	}
	if c.LastRun == nil {
		c.LastRun = map[string]int64{}
	}
	return c, nil
}

func (h *Host) saveBackupConfig(c *BackupConfig) error {
	if len(c.History) > 200 {
		c.History = c.History[len(c.History)-200:]
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	p := h.P(backupConfPath)
	os.MkdirAll(filepath.Dir(p), 0o700)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// BackupConfig liefert eine Kopie der Konfiguration.
func (h *Host) BackupConfig() (*BackupConfig, error) {
	h.backupMu.Lock()
	defer h.backupMu.Unlock()
	return h.loadBackupConfig()
}

// updateBackup ändert die Konfiguration unter Sperre.
func (h *Host) updateBackup(fn func(c *BackupConfig) error) error {
	h.backupMu.Lock()
	defer h.backupMu.Unlock()
	c, err := h.loadBackupConfig()
	if err != nil {
		return err
	}
	if err := fn(c); err != nil {
		return err
	}
	return h.saveBackupConfig(c)
}

var (
	reNFS       = regexp.MustCompile(`^[A-Za-z0-9.-]{1,253}:/[A-Za-z0-9._/-]{0,250}$`)
	reRemoteDir = regexp.MustCompile(`^[A-Za-z0-9._/~-]{1,250}$`)
	reSSHUser   = regexp.MustCompile(`^[A-Za-z0-9._-]{1,32}$`)
	reID        = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)
)

// Diese Bäume dürfen nie Sicherungsziel sein.
var forbiddenTargets = []string{"/", "/bin", "/sbin", "/usr", "/etc", "/dev", "/var", "/var/vmd", "/root", "/tmp"}

func slug(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 30 {
		out = out[:30]
	}
	if out == "" {
		out = "x"
	}
	return out
}

func (t *BackupTarget) validate(vmDir string) error {
	t.Name = strings.TrimSpace(t.Name)
	if t.Name == "" || len(t.Name) > 60 || strings.ContainsAny(t.Name, "\n\x00") {
		return invalid("Name des Ziels")
	}
	switch t.Type {
	case "local":
		p := filepath.Clean(t.Path)
		if !filepath.IsAbs(t.Path) || p != t.Path || strings.ContainsAny(p, "\"\n ") {
			return invalid("Verzeichnis %q (absoluter Pfad ohne Leerzeichen)", t.Path)
		}
		for _, f := range forbiddenTargets {
			if p == f {
				return invalid("%s ist als Sicherungsziel nicht erlaubt – ein eigenes Unterverzeichnis oder Dateisystem wählen", p)
			}
		}
		if p == vmDir || strings.HasPrefix(p, vmDir+"/") {
			return invalid("das Sicherungsziel darf nicht im VM-Verzeichnis liegen")
		}
		t.Host, t.User, t.Port = "", "", 0
	case "nfs":
		if !reNFS.MatchString(t.Path) {
			return invalid("NFS-Freigabe %q (Form server:/pfad)", t.Path)
		}
		t.Host, t.User, t.Port = "", "", 0
	case "sftp":
		if len(t.Host) > 253 || (!reHostname.MatchString(t.Host) && net.ParseIP(t.Host) == nil) {
			return invalid("Host %q", t.Host)
		}
		if !reSSHUser.MatchString(t.User) {
			return invalid("Benutzer %q", t.User)
		}
		if t.Port == 0 {
			t.Port = 22
		}
		if t.Port < 1 || t.Port > 65535 {
			return invalid("Port %d", t.Port)
		}
		if !reRemoteDir.MatchString(t.Path) || strings.Contains(t.Path, "..") {
			return invalid("entferntes Verzeichnis %q", t.Path)
		}
	default:
		return invalid("Zieltyp %q", t.Type)
	}
	return nil
}

func (t *BackupTarget) Location() string {
	switch t.Type {
	case "sftp":
		return fmt.Sprintf("sftp://%s@%s:%d/%s", t.User, t.Host, t.Port, strings.TrimPrefix(t.Path, "/"))
	case "nfs":
		return "nfs://" + strings.Replace(t.Path, ":/", "/", 1)
	}
	return t.Path
}

func (h *Host) SaveTarget(t *BackupTarget) (*BackupTarget, error) {
	if err := t.validate(h.VMDir); err != nil {
		return nil, err
	}
	err := h.updateBackup(func(c *BackupConfig) error {
		if t.ID == "" {
			t.ID = uniqueID(slug(t.Name), func(id string) bool { return c.target(id) != nil })
			c.Targets = append(c.Targets, t)
			return nil
		}
		for i, x := range c.Targets {
			if x.ID == t.ID {
				c.Targets[i] = t
				return nil
			}
		}
		return invalid("Ziel %q existiert nicht", t.ID)
	})
	return t, err
}

func uniqueID(base string, taken func(string) bool) string {
	id := base
	for i := 2; taken(id); i++ {
		id = fmt.Sprintf("%s-%d", base, i)
	}
	return id
}

func (c *BackupConfig) target(id string) *BackupTarget {
	for _, t := range c.Targets {
		if t.ID == id {
			return t
		}
	}
	return nil
}

func (c *BackupConfig) job(id string) *BackupJob {
	for _, j := range c.Jobs {
		if j.ID == id {
			return j
		}
	}
	return nil
}

func (h *Host) DeleteTarget(id string) error {
	return h.updateBackup(func(c *BackupConfig) error {
		for _, j := range c.Jobs {
			if j.Target == id {
				return invalid("Ziel wird noch von Job „%s“ verwendet", j.Name)
			}
		}
		for i, t := range c.Targets {
			if t.ID == id {
				c.Targets = append(c.Targets[:i], c.Targets[i+1:]...)
				return nil
			}
		}
		return invalid("Ziel %q existiert nicht", id)
	})
}

var backupModes = map[string]bool{"snapshot": true, "offline": true, "pause": true}
var reHHMM = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

func (j *BackupJob) validate(c *BackupConfig) error {
	j.Name = strings.TrimSpace(j.Name)
	if j.Name == "" || len(j.Name) > 60 || strings.ContainsAny(j.Name, "\n\x00") {
		return invalid("Name des Jobs")
	}
	if c.target(j.Target) == nil {
		return invalid("Ziel %q existiert nicht", j.Target)
	}
	for _, v := range j.VMs {
		if err := checkName("VM", v, reName); err != nil {
			return err
		}
	}
	if !backupModes[j.Mode] {
		return invalid("Modus %q", j.Mode)
	}
	if j.Keep < 1 || j.Keep > 365 {
		return invalid("Aufbewahrung 1–365 Sicherungen")
	}
	s := &j.Schedule
	switch s.Kind {
	case "manual":
	case "hourly", "daily", "weekly":
		if !reHHMM.MatchString(s.Time) {
			return invalid("Uhrzeit %q (HH:MM)", s.Time)
		}
		if s.Weekday < 0 || s.Weekday > 6 {
			return invalid("Wochentag")
		}
	default:
		return invalid("Zeitplan %q", s.Kind)
	}
	return nil
}

func (h *Host) SaveJob(j *BackupJob) (*BackupJob, error) {
	err := h.updateBackup(func(c *BackupConfig) error {
		if err := j.validate(c); err != nil {
			return err
		}
		if j.ID == "" {
			j.ID = uniqueID(slug(j.Name), func(id string) bool { return c.job(id) != nil })
			c.Jobs = append(c.Jobs, j)
			c.LastRun[j.ID] = time.Now().Unix() // erster Lauf zum nächsten Termin, nicht sofort
			return nil
		}
		for i, x := range c.Jobs {
			if x.ID == j.ID {
				c.Jobs[i] = j
				return nil
			}
		}
		return invalid("Job %q existiert nicht", j.ID)
	})
	return j, err
}

func (h *Host) DeleteJob(id string) error {
	return h.updateBackup(func(c *BackupConfig) error {
		for i, j := range c.Jobs {
			if j.ID == id {
				c.Jobs = append(c.Jobs[:i], c.Jobs[i+1:]...)
				delete(c.LastRun, id)
				return nil
			}
		}
		return invalid("Job %q existiert nicht", id)
	})
}

// NextRun berechnet den ersten Termin nach after (lokale Zeit).
func (s Schedule) NextRun(after time.Time) time.Time {
	if s.Kind == "manual" || !reHHMM.MatchString(s.Time) {
		return time.Time{}
	}
	var hh, mm int
	fmt.Sscanf(s.Time, "%d:%d", &hh, &mm)
	switch s.Kind {
	case "hourly":
		t := time.Date(after.Year(), after.Month(), after.Day(), after.Hour(), mm, 0, 0, after.Location())
		if !t.After(after) {
			t = t.Add(time.Hour)
		}
		return t
	case "daily":
		t := time.Date(after.Year(), after.Month(), after.Day(), hh, mm, 0, 0, after.Location())
		if !t.After(after) {
			t = t.AddDate(0, 0, 1)
		}
		return t
	case "weekly":
		t := time.Date(after.Year(), after.Month(), after.Day(), hh, mm, 0, 0, after.Location())
		for t.Weekday() != time.Weekday(s.Weekday) || !t.After(after) {
			t = t.AddDate(0, 0, 1)
		}
		return t
	}
	return time.Time{}
}

// RecordRun hängt einen Lauf an den Verlauf an bzw. aktualisiert ihn.
func (h *Host) RecordRun(r BackupRun) {
	h.updateBackup(func(c *BackupConfig) error {
		for i := range c.History {
			if c.History[i].TaskID == r.TaskID && r.TaskID != "" {
				c.History[i] = r
				return nil
			}
		}
		c.History = append(c.History, r)
		if r.Job != "" {
			c.LastRun[r.Job] = r.Started
		}
		return nil
	})
}
