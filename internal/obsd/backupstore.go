package obsd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Ablage einer Sicherung: <ziel>/<vm>/<id>/{manifest.json, disk0.qcow2, …}.
// Die Disks sind eigenständige qcow2-Images (vmctl create -i), unabhängig
// von Templates oder Snapshots.

type BackupManifest struct {
	VM      string       `json:"vm"`
	ID      string       `json:"id"`
	Created int64        `json:"created"`
	Mode    string       `json:"mode"`
	Host    string       `json:"host"`
	Block   string       `json:"block"` // vm.conf-Block zum Zeitpunkt der Sicherung
	Disks   []BackupDisk `json:"disks"`
	Target  string       `json:"target,omitempty"`
}

type BackupDisk struct {
	Source string `json:"source"`
	File   string `json:"file"`
	Size   int64  `json:"size"`
}

func (m *BackupManifest) Size() (n int64) {
	for _, d := range m.Disks {
		n += d.Size
	}
	return
}

// store abstrahiert lokale Verzeichnisse, NFS-Freigaben und SFTP-Ziele.
type store interface {
	open(ctx context.Context, w io.Writer) error
	close(ctx context.Context)
	writeDisk(ctx context.Context, w io.Writer, src, rel string) (int64, error)
	writeFile(ctx context.Context, rel string, data []byte) error
	fetchDisk(ctx context.Context, w io.Writer, rel, dst string) error
	manifests(ctx context.Context) ([]*BackupManifest, error)
	remove(ctx context.Context, m *BackupManifest) error
}

const backupSSHKey = "/var/maar/backup_ed25519"

func (h *Host) newStore(t *BackupTarget) store {
	switch t.Type {
	case "nfs":
		return &fsStore{h: h, root: filepath.Join("/var/maar/mnt", t.ID), nfs: t.Path}
	case "sftp":
		return &sftpStore{h: h, t: t}
	}
	return &fsStore{h: h, root: t.Path}
}

// ---- lokal und NFS ----

type fsStore struct {
	h       *Host
	root    string // Systempfad
	nfs     string // server:/export, leer = lokal
	mounted bool
}

func (s *fsStore) open(ctx context.Context, w io.Writer) error {
	if err := os.MkdirAll(s.h.P(s.root), 0o700); err != nil {
		return err
	}
	if s.nfs == "" {
		return nil
	}
	step(w, "Binde %s unter %s ein", s.nfs, s.root)
	if _, err := s.h.R.Run(ctx, nil, "mount", "-t", "nfs", "-o", "nodev,nosuid,noexec", s.nfs, s.root); err != nil {
		return err
	}
	s.mounted = true
	return nil
}

func (s *fsStore) close(ctx context.Context) {
	if s.mounted {
		s.h.R.Run(context.Background(), nil, "umount", s.root)
		s.mounted = false
	}
}

func (s *fsStore) abs(rel string) string { return filepath.Join(s.root, rel) }

func (s *fsStore) writeDisk(ctx context.Context, w io.Writer, src, rel string) (int64, error) {
	dst := s.abs(rel)
	if err := os.MkdirAll(s.h.P(filepath.Dir(dst)), 0o700); err != nil {
		return 0, err
	}
	if err := s.h.R.Stream(ctx, w, "vmctl", "create", "-i", src, dst); err != nil {
		os.Remove(s.h.P(dst))
		return 0, err
	}
	st, err := os.Stat(s.h.P(dst))
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}

func (s *fsStore) writeFile(_ context.Context, rel string, data []byte) error {
	p := s.h.P(s.abs(rel))
	os.MkdirAll(filepath.Dir(p), 0o700)
	return os.WriteFile(p, data, 0o600)
}

func (s *fsStore) fetchDisk(ctx context.Context, w io.Writer, rel, dst string) error {
	return s.h.R.Stream(ctx, w, "vmctl", "create", "-i", s.abs(rel), dst)
}

func (s *fsStore) manifests(context.Context) ([]*BackupManifest, error) {
	files, _ := filepath.Glob(filepath.Join(s.h.P(s.root), "*", "*", "manifest.json"))
	var list []*BackupManifest
	for _, f := range files {
		if m := readManifest(f); m != nil {
			list = append(list, m)
		}
	}
	return list, nil
}

func readManifest(p string) *BackupManifest {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	m := &BackupManifest{}
	if json.Unmarshal(b, m) != nil || !reName.MatchString(m.VM) || !reID.MatchString(m.ID) {
		return nil
	}
	return m
}

func (s *fsStore) remove(_ context.Context, m *BackupManifest) error {
	return os.RemoveAll(s.h.P(s.abs(filepath.Join(m.VM, m.ID))))
}

// ---- SFTP ----

type sftpStore struct {
	h *Host
	t *BackupTarget
}

func (s *sftpStore) staging() string { return filepath.Join(s.h.VMDir, ".backup-staging") }

func (s *sftpStore) open(ctx context.Context, w io.Writer) error {
	if _, err := s.h.BackupPubKey(ctx); err != nil {
		return err
	}
	return os.MkdirAll(s.h.P(s.staging()), 0o700)
}

func (s *sftpStore) close(context.Context) {}

func (s *sftpStore) remote(rel string) string { return strings.TrimSuffix(s.t.Path, "/") + "/" + rel }

// batch führt sftp-Befehle aus einer Datei aus (ohne Zeitlimit, für große Dateien).
func (s *sftpStore) batch(ctx context.Context, w io.Writer, cmds []string) error {
	f, err := os.CreateTemp(s.h.P(s.staging()), "sftp-*.batch")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	f.WriteString(strings.Join(cmds, "\n") + "\n")
	f.Close()
	sys := filepath.Join(s.staging(), filepath.Base(f.Name()))
	return s.h.R.Stream(ctx, w, "sftp", "-b", sys, "-i", backupSSHKey, "-P", strconv.Itoa(s.t.Port),
		"-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=accept-new", "-o", "UserKnownHostsFile=/var/maar/known_hosts",
		s.t.User+"@"+s.t.Host)
}

func q(p string) string { return `"` + p + `"` }

// mkdirs legt alle Ebenen an; „-“ ignoriert Fehler bei vorhandenen Verzeichnissen.
func (s *sftpStore) mkdirs(rel string) []string {
	var cmds []string
	cur := ""
	for _, part := range strings.Split(rel, "/") {
		cur = filepath.Join(cur, part)
		cmds = append(cmds, "-mkdir "+q(s.remote(cur)))
	}
	return cmds
}

func (s *sftpStore) writeDisk(ctx context.Context, w io.Writer, src, rel string) (int64, error) {
	tmp := filepath.Join(s.staging(), strings.ReplaceAll(rel, "/", "_"))
	defer os.Remove(s.h.P(tmp))
	if err := s.h.R.Stream(ctx, w, "vmctl", "create", "-i", src, tmp); err != nil {
		return 0, err
	}
	st, err := os.Stat(s.h.P(tmp))
	if err != nil {
		return 0, err
	}
	cmds := append([]string{"-mkdir " + q(s.t.Path)}, s.mkdirs(filepath.Dir(rel))...)
	cmds = append(cmds, "put "+q(tmp)+" "+q(s.remote(rel)))
	return st.Size(), s.batch(ctx, w, cmds)
}

func (s *sftpStore) writeFile(ctx context.Context, rel string, data []byte) error {
	tmp := filepath.Join(s.staging(), strings.ReplaceAll(rel, "/", "_"))
	if err := os.WriteFile(s.h.P(tmp), data, 0o600); err != nil {
		return err
	}
	defer os.Remove(s.h.P(tmp))
	return s.batch(ctx, io.Discard, append(s.mkdirs(filepath.Dir(rel)), "put "+q(tmp)+" "+q(s.remote(rel))))
}

func (s *sftpStore) fetchDisk(ctx context.Context, w io.Writer, rel, dst string) error {
	tmp := dst + ".download"
	defer os.Remove(s.h.P(tmp))
	if err := s.batch(ctx, w, []string{"get " + q(s.remote(rel)) + " " + q(tmp)}); err != nil {
		return err
	}
	// Prüft das Image und schreibt es dünn belegt.
	return s.h.R.Stream(ctx, w, "vmctl", "create", "-i", tmp, dst)
}

func (s *sftpStore) manifests(ctx context.Context) ([]*BackupManifest, error) {
	dir, err := os.MkdirTemp(s.h.P(s.staging()), "list-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	sysDir := filepath.Join(s.staging(), filepath.Base(dir))
	// Erst auflisten, dann jedes Manifest unter eindeutigem Namen holen
	// (alle heißen manifest.json und würden sich sonst überschreiben).
	var list []*BackupManifest
	var lsOut strings.Builder
	if err := s.batch(ctx, &lsOut, []string{"-ls -1 " + q(s.remote("*/*/manifest.json"))}); err != nil {
		return nil, err
	}
	var gets []string
	for _, l := range strings.Split(lsOut.String(), "\n") {
		l = strings.TrimSpace(l)
		if !strings.HasSuffix(l, "/manifest.json") || strings.HasPrefix(l, "sftp>") {
			continue
		}
		rel := strings.TrimPrefix(l, strings.TrimSuffix(s.t.Path, "/")+"/")
		parts := strings.Split(rel, "/")
		if len(parts) != 3 || !reName.MatchString(parts[0]) || !reID.MatchString(parts[1]) {
			continue
		}
		gets = append(gets, "-get "+q(l)+" "+q(filepath.Join(sysDir, parts[0]+"_"+parts[1]+".json")))
	}
	if len(gets) > 0 {
		s.batch(ctx, io.Discard, gets)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*_*.json"))
	for _, f := range files {
		if m := readManifest(f); m != nil {
			list = append(list, m)
		}
	}
	return list, nil
}

func (s *sftpStore) remove(ctx context.Context, m *BackupManifest) error {
	dir := filepath.Join(m.VM, m.ID)
	var cmds []string
	for _, d := range m.Disks {
		cmds = append(cmds, "-rm "+q(s.remote(filepath.Join(dir, d.File))))
	}
	cmds = append(cmds, "-rm "+q(s.remote(filepath.Join(dir, "manifest.json"))), "-rmdir "+q(s.remote(dir)))
	return s.batch(ctx, io.Discard, cmds)
}

// BackupPubKey erzeugt bei Bedarf den SSH-Schlüssel für SFTP-Ziele.
func (h *Host) BackupPubKey(ctx context.Context) (string, error) {
	if _, err := os.Stat(h.P(backupSSHKey)); os.IsNotExist(err) {
		os.MkdirAll(h.P(filepath.Dir(backupSSHKey)), 0o700)
		if _, err := h.R.Run(ctx, nil, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "maar-backup@"+h.Hostname(ctx), "-f", backupSSHKey); err != nil {
			return "", err
		}
	}
	b, err := os.ReadFile(h.P(backupSSHKey + ".pub"))
	return strings.TrimSpace(string(b)), err
}

// ---- Abfragen ----

func (h *Host) targetByID(id string) (*BackupTarget, error) {
	c, err := h.BackupConfig()
	if err != nil {
		return nil, err
	}
	t := c.target(id)
	if t == nil {
		return nil, invalid("Ziel %q existiert nicht", id)
	}
	return t, nil
}

// ListBackups liest alle Sicherungen eines Ziels, neueste zuerst.
func (h *Host) ListBackups(ctx context.Context, targetID string) ([]*BackupManifest, error) {
	t, err := h.targetByID(targetID)
	if err != nil {
		return nil, err
	}
	s := h.newStore(t)
	if err := s.open(ctx, io.Discard); err != nil {
		return nil, err
	}
	defer s.close(ctx)
	list, err := s.manifests(ctx)
	if list == nil {
		list = []*BackupManifest{}
	}
	sort.Slice(list, func(a, b int) bool {
		if list[a].Created != list[b].Created {
			return list[a].Created > list[b].Created
		}
		return list[a].VM < list[b].VM
	})
	return list, err
}

// TestTarget schreibt und löscht eine Probedatei.
func (h *Host) TestTarget(ctx context.Context, targetID string) (string, error) {
	t, err := h.targetByID(targetID)
	if err != nil {
		return "", err
	}
	s := h.newStore(t)
	if err := s.open(ctx, io.Discard); err != nil {
		return "", err
	}
	defer s.close(ctx)
	if err := s.writeFile(ctx, ".maar-probe", []byte("ok\n")); err != nil {
		return "", fmt.Errorf("Schreiben fehlgeschlagen: %w", err)
	}
	if fs, ok := s.(*fsStore); ok {
		os.Remove(h.P(fs.abs(".maar-probe")))
	} else {
		s.(*sftpStore).batch(ctx, io.Discard, []string{"-rm " + q(s.(*sftpStore).remote(".maar-probe"))})
	}
	list, _ := s.manifests(ctx)
	return fmt.Sprintf("%s ist beschreibbar, %d Sicherungen gefunden.", t.Location(), len(list)), nil
}
