package obsd

import (
	"context"
	"os"
	"regexp"
	"strings"
)

type Addr struct {
	Family  string `json:"family"`
	Addr    string `json:"addr"`
	Netmask string `json:"netmask,omitempty"`
	Prefix  string `json:"prefix,omitempty"`
}

type Iface struct {
	Name    string   `json:"name"`
	Flags   []string `json:"flags"`
	Up      bool     `json:"up"`
	MTU     string   `json:"mtu"`
	LLAddr  string   `json:"lladdr,omitempty"`
	Descr   string   `json:"description,omitempty"`
	Media   string   `json:"media,omitempty"`
	Status  string   `json:"status,omitempty"`
	Groups  []string `json:"groups"`
	Addrs   []Addr   `json:"addrs"`
	Members []string `json:"members,omitempty"`
	HasFile bool     `json:"hasFile"`
	Raw     string   `json:"raw"`
}

var reIfHead = regexp.MustCompile(`^([a-z]+[0-9]+): flags=[0-9a-f]+<([^>]*)>(?:.*mtu (\d+))?`)

func ParseIfconfig(out string) []*Iface {
	var list []*Iface
	var cur *Iface
	for _, l := range strings.Split(out, "\n") {
		if m := reIfHead.FindStringSubmatch(l); m != nil {
			cur = &Iface{Name: m[1], MTU: m[3], Groups: []string{}, Addrs: []Addr{}}
			if m[2] != "" {
				cur.Flags = strings.Split(m[2], ",")
			}
			for _, f := range cur.Flags {
				cur.Up = cur.Up || f == "UP"
			}
			list = append(list, cur)
			cur.Raw = l + "\n"
			continue
		}
		if cur == nil || strings.TrimSpace(l) == "" {
			continue
		}
		cur.Raw += l + "\n"
		f := strings.Fields(l)
		switch f[0] {
		case "lladdr":
			cur.LLAddr = val(f, 1)
		case "description:":
			cur.Descr = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "description:"))
		case "media:":
			cur.Media = strings.Join(f[1:], " ")
		case "status:":
			cur.Status = strings.Join(f[1:], " ")
		case "groups:":
			cur.Groups = f[1:]
		case "inet":
			a := Addr{Family: "inet", Addr: val(f, 1)}
			for i := 2; i+1 < len(f); i++ {
				if f[i] == "netmask" {
					a.Netmask = hexMask(f[i+1])
				}
			}
			cur.Addrs = append(cur.Addrs, a)
		case "inet6":
			a := Addr{Family: "inet6", Addr: strings.SplitN(val(f, 1), "%", 2)[0]}
			for i := 2; i+1 < len(f); i++ {
				if f[i] == "prefixlen" {
					a.Prefix = f[i+1]
				}
			}
			cur.Addrs = append(cur.Addrs, a)
		case "member:":
			// bridge(4): "member: vether0 flags=3<LEARNING,DISCOVER>"
			cur.Members = append(cur.Members, val(f, 1))
		default:
			// veb(4): "vport0 flags=3<LEARNING,DISCOVER>"
			if len(f) >= 2 && strings.HasPrefix(f[1], "flags=") && reIface.MatchString(f[0]) {
				cur.Members = append(cur.Members, f[0])
			}
		}
	}
	return list
}

func val(f []string, i int) string {
	if i < len(f) {
		return f[i]
	}
	return ""
}

// hexMask wandelt 0xffffff00 in 255.255.255.0.
func hexMask(s string) string {
	s = strings.TrimPrefix(s, "0x")
	if len(s) != 8 {
		return s
	}
	var parts []string
	for i := 0; i < 8; i += 2 {
		var n int
		for _, c := range s[i : i+2] {
			n *= 16
			switch {
			case c >= '0' && c <= '9':
				n += int(c - '0')
			case c >= 'a' && c <= 'f':
				n += int(c-'a') + 10
			}
		}
		parts = append(parts, itoa(n))
	}
	return strings.Join(parts, ".")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

type Route struct {
	Dest    string `json:"dest"`
	Gateway string `json:"gateway"`
	Flags   string `json:"flags"`
	Iface   string `json:"iface"`
	Family  string `json:"family"`
}

type Network struct {
	Interfaces []*Iface `json:"interfaces"`
	Routes     []Route  `json:"routes"`
	Resolvers  []string `json:"resolvers"`
	Gateway    string   `json:"gateway"`
}

func (h *Host) Network(ctx context.Context) (*Network, error) {
	out, err := h.out(ctx, "ifconfig", "-A")
	if err != nil {
		return nil, err
	}
	n := &Network{Interfaces: ParseIfconfig(out), Routes: []Route{}, Resolvers: []string{}}
	for _, i := range n.Interfaces {
		_, err := os.Stat(h.P("/etc/hostname." + i.Name))
		i.HasFile = err == nil
	}
	for _, fam := range []string{"inet", "inet6"} {
		rt, _ := h.out(ctx, "netstat", "-rn", "-f", fam)
		// Destination Gateway Flags Refs Use Mtu Prio Iface
		for _, l := range lines(rt) {
			f := strings.Fields(l)
			if len(f) < 8 || f[0] == "Destination" || f[0] == "Routing" || f[0] == "Internet:" || f[0] == "Internet6:" {
				continue
			}
			if strings.Contains(f[2], "L") && fam == "inet6" {
				continue // Link-lokale Einträge ausblenden
			}
			n.Routes = append(n.Routes, Route{Dest: f[0], Gateway: f[1], Flags: f[2], Iface: f[7], Family: fam})
			if f[0] == "default" && fam == "inet" {
				n.Gateway = f[1]
			}
		}
	}
	if rc, err := h.readFile("/etc/resolv.conf"); err == nil {
		for _, l := range lines(rc) {
			if f := strings.Fields(l); len(f) >= 2 && f[0] == "nameserver" {
				n.Resolvers = append(n.Resolvers, f[1])
			}
		}
	}
	return n, nil
}

func (h *Host) IfaceAction(ctx context.Context, name, action string) (string, error) {
	if err := checkName("Interface", name, reIface); err != nil {
		return "", err
	}
	var r Result
	var err error
	switch action {
	case "up", "down":
		r, err = h.R.Run(ctx, nil, "ifconfig", name, action)
	case "netstart":
		r, err = h.R.Run(ctx, nil, "sh", "/etc/netstart", name)
	case "destroy":
		r, err = h.R.Run(ctx, nil, "ifconfig", name, "destroy")
	default:
		return "", invalid("Aktion %q", action)
	}
	return strings.TrimSpace(string(r.Stdout) + string(r.Stderr)), err
}
