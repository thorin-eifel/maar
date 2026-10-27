package obsd

import (
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const logDir = "/var/log"

type LogFile struct {
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	MTime int64  `json:"mtime"`
}

// Binäre oder rotierte Dateien werden nicht angeboten.
func skipLog(name string) bool {
	for _, s := range []string{".gz", ".bz2", ".0", ".1"} {
		if strings.HasSuffix(name, s) {
			return true
		}
	}
	return name == "pflog" || name == "wtmp" || name == "lastlog" || strings.HasPrefix(name, "pflog.")
}

func (h *Host) LogFiles() ([]LogFile, error) {
	root := h.P(logDir)
	var list []LogFile
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			if strings.Count(rel, "/") >= 1 {
				return filepath.SkipDir
			}
			return nil
		}
		if skipLog(d.Name()) || !d.Type().IsRegular() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			list = append(list, LogFile{Name: rel, Size: info.Size(), MTime: info.ModTime().Unix()})
		}
		return nil
	})
	sort.Slice(list, func(a, b int) bool { return list[a].Name < list[b].Name })
	return list, err
}

// LogPath validiert einen relativen Lognamen und liefert den echten Pfad.
func (h *Host) LogPath(name string) (string, error) {
	clean := filepath.Clean(name)
	if clean != name || strings.HasPrefix(clean, "/") || strings.HasPrefix(clean, "..") || strings.Count(clean, "/") > 1 || skipLog(filepath.Base(clean)) {
		return "", invalid("Logdatei %q", name)
	}
	return h.P(filepath.Join(logDir, clean)), nil
}

// TailFile liefert die letzten n Zeilen sowie die aktuelle Dateigröße.
func TailFile(p string, n int) ([]string, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	size := st.Size()
	const chunk = 64 * 1024
	var buf []byte
	pos := size
	for pos > 0 && strings.Count(string(buf), "\n") <= n {
		step := int64(chunk)
		if pos < step {
			step = pos
		}
		pos -= step
		b := make([]byte, step)
		if _, err := f.ReadAt(b, pos); err != nil && err != io.EOF {
			return nil, 0, err
		}
		buf = append(b, buf...)
		if len(buf) > 8*1024*1024 {
			break
		}
	}
	all := strings.Split(strings.TrimRight(string(buf), "\n"), "\n")
	if pos > 0 && len(all) > 0 {
		all = all[1:] // angeschnittene erste Zeile verwerfen
	}
	if len(all) > n {
		all = all[len(all)-n:]
	}
	if len(all) == 1 && all[0] == "" {
		all = []string{}
	}
	return all, size, nil
}
