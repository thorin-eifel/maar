package obsd

import (
	"context"
	"sort"
	"strconv"
	"strings"
)

type User struct {
	Name    string   `json:"name"`
	UID     int      `json:"uid"`
	GID     int      `json:"gid"`
	Comment string   `json:"comment"`
	Home    string   `json:"home"`
	Shell   string   `json:"shell"`
	Groups  []string `json:"groups"`
	System  bool     `json:"system"`
	Locked  bool     `json:"locked"`
}

type Group struct {
	Name    string   `json:"name"`
	GID     int      `json:"gid"`
	Members []string `json:"members"`
}

func (h *Host) Groups() ([]Group, error) {
	src, err := h.readFile("/etc/group")
	if err != nil {
		return nil, err
	}
	var list []Group
	for _, l := range lines(src) {
		f := strings.Split(l, ":")
		if len(f) < 4 || strings.HasPrefix(l, "#") {
			continue
		}
		g := Group{Name: f[0], Members: []string{}}
		g.GID, _ = strconv.Atoi(f[2])
		for _, m := range strings.Split(f[3], ",") {
			if m != "" {
				g.Members = append(g.Members, m)
			}
		}
		list = append(list, g)
	}
	return list, nil
}

// InGroup prüft die Mitgliedschaft (primär oder sekundär) eines Benutzers.
func (h *Host) InGroup(user, group string) bool {
	users, err := h.Users()
	if err != nil {
		return false
	}
	for _, u := range users {
		if u.Name == user {
			for _, g := range u.Groups {
				if g == group {
					return true
				}
			}
		}
	}
	return false
}

func (h *Host) Users() ([]User, error) {
	src, err := h.readFile("/etc/passwd")
	if err != nil {
		return nil, err
	}
	groups, _ := h.Groups()
	byGID := map[int]string{}
	for _, g := range groups {
		byGID[g.GID] = g.Name
	}
	var list []User
	for _, l := range lines(src) {
		f := strings.Split(l, ":")
		if len(f) < 7 || strings.HasPrefix(l, "#") {
			continue
		}
		u := User{Name: f[0], Comment: f[4], Home: f[5], Shell: f[6], Groups: []string{}}
		u.UID, _ = strconv.Atoi(f[2])
		u.GID, _ = strconv.Atoi(f[3])
		u.System = u.UID != 0 && (u.UID < 1000 || u.UID >= 32766)
		u.Locked = strings.HasSuffix(u.Shell, "nologin") || strings.HasSuffix(u.Shell, "-")
		if g := byGID[u.GID]; g != "" {
			u.Groups = append(u.Groups, g)
		}
		for _, g := range groups {
			for _, m := range g.Members {
				if m == u.Name && g.GID != u.GID {
					u.Groups = append(u.Groups, g.Name)
				}
			}
		}
		list = append(list, u)
	}
	sort.Slice(list, func(a, b int) bool { return list[a].UID < list[b].UID })
	return list, nil
}

type UserSpec struct {
	Name     string   `json:"name"`
	Comment  string   `json:"comment"`
	Shell    string   `json:"shell"`
	Groups   []string `json:"groups"`
	Class    string   `json:"class"`
	Password string   `json:"password"`
}

var shells = map[string]bool{"/bin/ksh": true, "/bin/sh": true, "/bin/csh": true, "/sbin/nologin": true,
	"/usr/local/bin/bash": true, "/usr/local/bin/zsh": true, "/usr/local/bin/fish": true}

func (s *UserSpec) validate() error {
	if err := checkName("Benutzername", s.Name, reUser); err != nil {
		return err
	}
	if strings.ContainsAny(s.Comment, ":\n\x00") || len(s.Comment) > 100 {
		return invalid("Beschreibung")
	}
	if s.Shell == "" {
		s.Shell = "/bin/ksh"
	}
	if !shells[s.Shell] {
		return invalid("Shell %q", s.Shell)
	}
	for _, g := range s.Groups {
		if err := checkName("Gruppe", g, reUser); err != nil {
			return err
		}
	}
	if s.Class != "" {
		if err := checkName("Login-Klasse", s.Class, reUser); err != nil {
			return err
		}
	}
	return nil
}

func (h *Host) CreateUser(ctx context.Context, s *UserSpec) error {
	if err := s.validate(); err != nil {
		return err
	}
	args := []string{"-m", "-s", s.Shell}
	if s.Comment != "" {
		args = append(args, "-c", s.Comment)
	}
	if len(s.Groups) > 0 {
		args = append(args, "-G", strings.Join(s.Groups, ","))
	}
	if s.Class != "" {
		args = append(args, "-L", s.Class)
	}
	if err := h.run(ctx, "useradd", append(args, s.Name)...); err != nil {
		return err
	}
	if s.Password != "" {
		return h.SetPassword(ctx, s.Name, s.Password)
	}
	return nil
}

func (h *Host) UpdateUser(ctx context.Context, s *UserSpec) error {
	if err := s.validate(); err != nil {
		return err
	}
	args := []string{"-s", s.Shell, "-c", s.Comment, "-S", strings.Join(s.Groups, ",")}
	if s.Class != "" {
		args = append(args, "-L", s.Class)
	}
	return h.run(ctx, "usermod", append(args, s.Name)...)
}

// SetPassword erzeugt mit encrypt(1) einen bcrypt-Hash und setzt ihn per usermod.
func (h *Host) SetPassword(ctx context.Context, name, pw string) error {
	if err := checkName("Benutzername", name, reUser); err != nil {
		return err
	}
	if len(pw) < 8 || strings.ContainsAny(pw, "\n\x00") {
		return invalid("Passwort muss mindestens 8 Zeichen haben")
	}
	r, err := h.R.Run(ctx, []byte(pw+"\n"), "encrypt")
	if err != nil {
		return err
	}
	hash := strings.TrimSpace(string(r.Stdout))
	if !strings.HasPrefix(hash, "$2") {
		return invalid("encrypt lieferte keinen bcrypt-Hash")
	}
	return h.run(ctx, "usermod", "-p", hash, name)
}

func (h *Host) UserAction(ctx context.Context, name, action string) error {
	if err := checkName("Benutzername", name, reUser); err != nil {
		return err
	}
	if name == "root" {
		return invalid("root kann hier nicht gesperrt oder gelöscht werden")
	}
	switch action {
	case "lock":
		return h.run(ctx, "usermod", "-Z", name)
	case "unlock":
		return h.run(ctx, "usermod", "-U", name)
	case "delete":
		return h.run(ctx, "userdel", "-r", name)
	}
	return invalid("Aktion %q", action)
}

func (h *Host) GroupAction(ctx context.Context, name, action string) error {
	if err := checkName("Gruppe", name, reUser); err != nil {
		return err
	}
	switch action {
	case "create":
		return h.run(ctx, "groupadd", name)
	case "delete":
		if name == "wheel" || name == "operator" {
			return invalid("Systemgruppe %q", name)
		}
		return h.run(ctx, "groupdel", name)
	}
	return invalid("Aktion %q", action)
}

// ---- Cron ----

func (h *Host) Crontab(ctx context.Context, user string) (string, error) {
	if err := checkName("Benutzer", user, reUser); err != nil {
		return "", err
	}
	r, err := h.R.Run(ctx, nil, "crontab", "-l", "-u", user)
	if err != nil && strings.Contains(string(r.Stderr), "no crontab") {
		return "", nil
	}
	return string(r.Stdout), err
}

func (h *Host) SetCrontab(ctx context.Context, user, content string) error {
	if err := checkName("Benutzer", user, reUser); err != nil {
		return err
	}
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	_, err := h.R.Run(ctx, []byte(content), "crontab", "-u", user, "-")
	return err
}
