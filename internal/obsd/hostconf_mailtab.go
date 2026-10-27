package obsd

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

// Mail-Tabellen von smtpd als Formular: Aliase („name: ziel, ziel“) und virtuelle
// Benutzer („adresse ziel, ziel“). Anmeldetabellen (Passwort-Hashes) werden weder
// gelesen noch angezeigt. Nach dem Speichern lädt „smtpctl update table“ die Tabelle neu.

type MailEntry struct {
	ID      int      `json:"id"` // Zeilennummer, 0 = neu
	Key     string   `json:"key"`
	Targets []string `json:"targets"`
	Raw     string   `json:"raw,omitempty"` // Zeile mit |befehl, /datei oder :include: – bleibt unverändert
}

type MailTable struct {
	Name    string      `json:"name"`
	File    string      `json:"file"`
	Kind    string      `json:"kind"` // aliases, virtual
	Entries []MailEntry `json:"entries"`
}

type MailTablesView struct {
	Tables  []MailTable `json:"tables"`
	Hidden  []string    `json:"hidden"` // Tabellen, die dieses Formular absichtlich nicht zeigt
	Users   []string    `json:"users"`
	Domains []string    `json:"domains"`
	Running bool        `json:"running"`
	Missing []string    `json:"missing"`
}

var (
	reMailAliasKey = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)
	reMailAddr     = regexp.MustCompile(`^[A-Za-z0-9._%+-]{1,64}@[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
	reMailVKey     = regexp.MustCompile(`^([A-Za-z0-9._%+-]{1,64})?@[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
)

func isRawTarget(s string) bool {
	return strings.ContainsAny(s, "|/") || strings.Contains(s, ":include:")
}

func parseMailLine(l, kind string) (MailEntry, bool) {
	t := strings.TrimSpace(l)
	if t == "" || strings.HasPrefix(t, "#") {
		return MailEntry{}, false
	}
	var key, rest string
	if kind == "aliases" {
		i := strings.Index(t, ":")
		if i <= 0 {
			return MailEntry{Raw: t}, true
		}
		key, rest = strings.TrimSpace(t[:i]), strings.TrimSpace(t[i+1:])
	} else {
		f := strings.Fields(t)
		if len(f) < 2 {
			return MailEntry{Raw: t}, true
		}
		key, rest = f[0], strings.TrimSpace(strings.TrimPrefix(t, f[0]))
	}
	if strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t") || isRawTarget(rest) || rest == "" {
		return MailEntry{Raw: t}, true
	}
	e := MailEntry{Key: key, Targets: []string{}}
	for _, x := range strings.Split(rest, ",") {
		if x = strings.TrimSpace(x); x != "" {
			e.Targets = append(e.Targets, x)
		}
	}
	if len(e.Targets) == 0 {
		return MailEntry{Raw: t}, true
	}
	return e, true
}

// mailTargets ordnet die Tabellen aus smtpd.conf ihren Rollen zu.
func (h *Host) mailTargets(ctx context.Context) (map[string]string, map[string]string, []string, error) {
	v, err := h.SMTPD(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	files := map[string]string{} // Name → Datei
	for _, t := range v.Conf.Tables {
		if t.Raw == "" && t.Type == "file" {
			files[t.Name] = t.Path
		}
	}
	kind := map[string]string{}
	hidden := map[string]bool{}
	for _, a := range v.Conf.Actions {
		if a.Alias != "" {
			kind[a.Alias] = "aliases"
		}
		if a.Virtual != "" {
			kind[a.Virtual] = "virtual"
		}
		for _, n := range []string{a.Auth, a.Userbase} {
			if n != "" {
				hidden[n] = true
			}
		}
	}
	hid := []string{}
	for n := range hidden {
		hid = append(hid, n)
		delete(kind, n)
	}
	sort.Strings(hid)
	return files, kind, hid, nil
}

func (h *Host) MailTables(ctx context.Context) (*MailTablesView, error) {
	files, kind, hidden, err := h.mailTargets(ctx)
	if err != nil {
		return nil, err
	}
	v := &MailTablesView{Tables: []MailTable{}, Hidden: hidden, Users: []string{}, Domains: []string{}, Missing: []string{}}
	_, v.Running = h.rcActive(ctx, "smtpd")
	if us, err := h.Users(); err == nil {
		for _, u := range us {
			if !u.System || u.UID == 0 {
				v.Users = append(v.Users, u.Name)
			}
		}
	}
	names := make([]string, 0, len(kind))
	for n := range kind {
		names = append(names, n)
	}
	sort.Strings(names)
	doms := map[string]bool{}
	for _, n := range names {
		path, ok := files[n]
		if !ok {
			continue
		}
		src, err := h.readFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				v.Missing = append(v.Missing, path)
				continue
			}
			return nil, err
		}
		t := MailTable{Name: n, File: path, Kind: kind[n], Entries: []MailEntry{}}
		for i, l := range splitFileLines(src) {
			if e, ok := parseMailLine(l, t.Kind); ok {
				e.ID = i + 1
				t.Entries = append(t.Entries, e)
				if j := strings.LastIndex(e.Key, "@"); j >= 0 {
					doms[e.Key[j+1:]] = true
				}
			}
		}
		v.Tables = append(v.Tables, t)
	}
	for d := range doms {
		v.Domains = append(v.Domains, d)
	}
	sort.Strings(v.Domains)
	return v, nil
}

func (h *Host) SaveMailTable(ctx context.Context, name string, entries []MailEntry, preview bool) (*ConfResult, error) {
	cur, err := h.MailTables(ctx)
	if err != nil {
		return nil, err
	}
	var tab *MailTable
	for i := range cur.Tables {
		if cur.Tables[i].Name == name {
			tab = &cur.Tables[i]
		}
	}
	if tab == nil {
		return nil, invalid("Die Tabelle %q ist nicht bearbeitbar (kein Alias- oder Virtual-Verzeichnis, oder Anmeldetabelle)", name)
	}
	if len(entries) > 5000 {
		return nil, invalid("zu viele Einträge")
	}
	users := map[string]bool{}
	for _, u := range cur.Users {
		users[u] = true
	}
	src, _ := h.readFile(tab.File)
	orig := splitFileLines(src)
	rawOK := map[string]bool{}
	for _, e := range tab.Entries {
		if e.Raw != "" {
			rawOK[e.Raw] = true
		}
	}
	byID := map[int]MailEntry{}
	seen := map[string]bool{}
	graph := map[string][]string{}
	var refs [][2]string
	for i := range entries {
		e := &entries[i]
		if e.Raw != "" {
			if !rawOK[e.Raw] {
				return nil, invalid("Rohzeilen lassen sich nur übernehmen, nicht neu anlegen")
			}
			if k := strings.Fields(strings.NewReplacer(":", " ", "\t", " ").Replace(e.Raw)); len(k) > 0 {
				seen[strings.ToLower(k[0])] = true
			}
			if e.ID > 0 {
				byID[e.ID] = *e
			}
			continue
		}
		e.Key = strings.TrimSpace(e.Key)
		if tab.Kind == "aliases" {
			if !reMailAliasKey.MatchString(e.Key) {
				return nil, invalid("Alias %q (Buchstaben, Ziffern, . _ + -)", e.Key)
			}
		} else if !reMailVKey.MatchString(e.Key) {
			return nil, invalid("Adresse %q (name@domain oder @domain)", e.Key)
		}
		lk := strings.ToLower(e.Key)
		if seen[lk] {
			return nil, invalid("%q ist doppelt", e.Key)
		}
		seen[lk] = true
		if len(e.Targets) == 0 || len(e.Targets) > 50 {
			return nil, invalid("%s: ein bis 50 Ziele", e.Key)
		}
		for j, t := range e.Targets {
			t = strings.TrimSpace(t)
			e.Targets[j] = t
			switch {
			case users[t]:
			case reMailAddr.MatchString(t):
			case tab.Kind == "aliases" && reMailAliasKey.MatchString(t):
				refs = append(refs, [2]string{e.Key, t}) // Verweis auf einen anderen Alias – unten geprüft
			default:
				return nil, invalid("%s: Ziel %q ist weder ein lokaler Benutzer noch eine Mail-Adresse", e.Key, t)
			}
			graph[lk] = append(graph[lk], strings.ToLower(t))
		}
		if e.ID > 0 {
			if e.ID > len(orig) {
				return nil, invalid("Eintrag %d gibt es nicht mehr – bitte neu laden", e.ID)
			}
			byID[e.ID] = *e
		}
	}
	for _, r := range refs {
		if !seen[strings.ToLower(r[1])] {
			return nil, invalid("%s: Ziel %q ist weder ein lokaler Benutzer, noch eine Mail-Adresse, noch ein vorhandener Alias", r[0], r[1])
		}
	}
	// Schleifen unter Aliasen
	if tab.Kind == "aliases" {
		state := map[string]int{}
		var visit func(string) bool
		visit = func(n string) bool {
			if state[n] == 1 {
				return true
			}
			if state[n] == 2 {
				return false
			}
			state[n] = 1
			for _, t := range graph[n] {
				if _, ok := graph[t]; ok && !users[t] && visit(t) {
					return true
				}
			}
			state[n] = 2
			return false
		}
		for k := range graph {
			if visit(k) {
				return nil, invalid("Die Aliase verweisen im Kreis (Schleife bei %q)", k)
			}
		}
		if !seen["root"] {
			return nil, invalid("Der Alias „root“ darf nicht fehlen – Systemmails (cron, Sicherheitsberichte) gingen verloren")
		}
	}
	line := func(e MailEntry) string {
		if e.Raw != "" {
			return e.Raw
		}
		if tab.Kind == "aliases" {
			return e.Key + ": " + strings.Join(e.Targets, ", ")
		}
		return e.Key + "\t" + strings.Join(e.Targets, ", ")
	}
	var out []string
	for i, l := range orig {
		e, isEntry := parseMailLine(l, tab.Kind)
		if !isEntry {
			out = append(out, l)
			continue
		}
		_ = e
		if ne, ok := byID[i+1]; ok {
			out = append(out, line(ne))
		}
	}
	for _, e := range entries {
		if e.ID == 0 {
			out = append(out, line(e))
		}
	}
	content := strings.Join(out, "\n") + "\n"
	res := &ConfResult{Warnings: []string{}}
	if tab.Kind == "aliases" && !seen["postmaster"] {
		res.Warnings = append(res.Warnings, "Es gibt keinen Alias „postmaster“ – das ist nach RFC 5321 für jeden Mailserver vorgeschrieben.")
	}
	if !cur.Running {
		res.Warnings = append(res.Warnings, "smtpd läuft nicht – die Änderung gilt, sobald er startet.")
	}
	// Datei direkt schreiben: Aliasdateien haben keine Syntaxprüfung; Sicherung wie bei Konfigurationsdateien.
	if preview {
		res.Content = content
		return res, nil
	}
	if src == content {
		res.Output = "Keine Änderung."
		return res, nil
	}
	if _, err := h.backupAny(tab.File); err != nil {
		return nil, fmt.Errorf("Sicherung fehlgeschlagen: %w", err)
	}
	if err := h.writeAtomic(tab.File, []byte(content), 0o644); err != nil {
		return nil, err
	}
	if cur.Running {
		if err := h.run(ctx, "smtpctl", "update", "table", name); err != nil {
			return nil, fmt.Errorf("Datei gespeichert, aber smtpctl update table %s schlug fehl: %w", name, err)
		}
		res.Applied = "Gespeichert und von smtpd neu eingelesen."
	} else {
		res.Applied = "Gespeichert."
	}
	return res, nil
}

// backupAny sichert eine beliebige Datei im Sicherungsverzeichnis (wie backup() für freigegebene Dateien).
func (h *Host) backupAny(p string) (string, error) {
	return h.backup(p)
}
