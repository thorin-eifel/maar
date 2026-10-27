package obsd

import (
	"context"
	"regexp"
	"strings"
)

type Package struct {
	Name    string `json:"name"`
	Stem    string `json:"stem"`
	Version string `json:"version"`
	Comment string `json:"comment"`
	Manual  bool   `json:"manual"`
}

var reVersion = regexp.MustCompile(`^(.+?)-([0-9][^-]*)(-[a-z0-9_]+)?$`)

func splitPkg(n string) (stem, ver string) {
	if m := reVersion.FindStringSubmatch(n); m != nil {
		return m[1], m[2] + m[3]
	}
	return n, ""
}

func (h *Host) Packages(ctx context.Context) ([]Package, error) {
	out, err := h.out(ctx, "pkg_info")
	if err != nil {
		return nil, err
	}
	manual := map[string]bool{}
	for _, l := range lines(h.outLenient(ctx, "pkg_info", "-mq")) {
		manual[strings.TrimSpace(l)] = true
	}
	list := []Package{}
	for _, l := range lines(out) {
		name, comment, _ := strings.Cut(l, " ")
		stem, ver := splitPkg(name)
		list = append(list, Package{Name: name, Stem: stem, Version: ver, Comment: strings.TrimSpace(comment), Manual: manual[name]})
	}
	return list, nil
}

type PkgHit struct {
	Name      string `json:"name"`
	Installed bool   `json:"installed"`
}

func (h *Host) SearchPackages(ctx context.Context, q string) ([]PkgHit, error) {
	if !rePkg.MatchString(q) || len(q) < 2 {
		return nil, invalid("Suchbegriff %q", q)
	}
	out, err := h.out(ctx, "pkg_info", "-Q", q)
	if err != nil && out == "" {
		return nil, err
	}
	hits := []PkgHit{}
	for _, l := range lines(out) {
		l = strings.TrimSpace(l)
		inst := strings.HasSuffix(l, "(installed)")
		hits = append(hits, PkgHit{Name: strings.TrimSpace(strings.TrimSuffix(l, "(installed)")), Installed: inst})
	}
	return hits, nil
}

type Patches struct {
	Installed []string `json:"installed"`
	Available []string `json:"available"`
}

func (h *Host) Patches(ctx context.Context) (*Patches, error) {
	p := &Patches{Installed: []string{}, Available: []string{}}
	p.Installed = append(p.Installed, lines(h.outLenient(ctx, "syspatch", "-l"))...)
	out, err := h.out(ctx, "syspatch", "-c")
	if err != nil && out == "" {
		return p, err
	}
	p.Available = append(p.Available, lines(out)...)
	return p, nil
}

// JobCommand beschreibt einen lang laufenden Befehl für den Job-Manager.
type JobCommand struct {
	Title string
	Name  string
	Args  []string
}

func (h *Host) PkgJob(action string, names []string) (*JobCommand, error) {
	for _, n := range names {
		if !rePkg.MatchString(n) {
			return nil, invalid("Paketname %q", n)
		}
	}
	switch action {
	case "install":
		if len(names) == 0 {
			return nil, invalid("kein Paket angegeben")
		}
		return &JobCommand{"Installiere " + strings.Join(names, ", "), "pkg_add", append([]string{"-I", "-v"}, names...)}, nil
	case "delete":
		if len(names) == 0 {
			return nil, invalid("kein Paket angegeben")
		}
		return &JobCommand{"Entferne " + strings.Join(names, ", "), "pkg_delete", append([]string{"-I"}, names...)}, nil
	case "update":
		return &JobCommand{"Pakete aktualisieren", "pkg_add", append([]string{"-I", "-u", "-v"}, names...)}, nil
	case "autoremove":
		return &JobCommand{"Verwaiste Abhängigkeiten entfernen", "pkg_delete", []string{"-I", "-a"}}, nil
	case "syspatch":
		return &JobCommand{"Systempatches einspielen", "syspatch", nil}, nil
	case "fw_update":
		return &JobCommand{"Firmware aktualisieren", "fw_update", []string{"-v"}}, nil
	case "sysupgrade":
		return &JobCommand{"Systemupgrade vorbereiten (ohne Neustart)", "sysupgrade", []string{"-n"}}, nil
	case "sysupgrade-snapshot":
		return &JobCommand{"Snapshot-Upgrade vorbereiten (ohne Neustart)", "sysupgrade", []string{"-n", "-s"}}, nil
	}
	return nil, invalid("Aktion %q", action)
}
