package obsd

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Ein schlanker Parser für vm.conf(5). Er erkennt die Blöcke "vm" und
// "switch" mit ihren Byte-Positionen, damit einzelne Blöcke ersetzt oder
// entfernt werden können, ohne den Rest der Datei anzufassen.

type token struct {
	s          string
	start, end int
	quoted     bool
}

func tokenize(src string) []token {
	var toks []token
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == '#':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == ',':
			i++
		case c == '{' || c == '}' || c == '=':
			toks = append(toks, token{s: string(c), start: i, end: i + 1})
			i++
		case c == '"':
			j := i + 1
			var b strings.Builder
			for j < len(src) && src[j] != '"' {
				if src[j] == '\\' && j+1 < len(src) {
					j++
				}
				b.WriteByte(src[j])
				j++
			}
			toks = append(toks, token{s: b.String(), start: i, end: min(j+1, len(src)), quoted: true})
			i = j + 1
		default:
			j := i
			for j < len(src) && !strings.ContainsRune(" \t\r\n{}=#\",", rune(src[j])) {
				j++
			}
			toks = append(toks, token{s: src[i:j], start: i, end: j})
			i = j
		}
	}
	return toks
}

type VMIface struct {
	Name   string `json:"name,omitempty"`
	Switch string `json:"switch,omitempty"`
	LLAddr string `json:"lladdr,omitempty"`
}

type VMConf struct {
	Name       string    `json:"name"`
	Memory     string    `json:"memory,omitempty"`
	CPUs       int       `json:"cpus"`
	SEV        string    `json:"sev,omitempty"` // "", "sev" oder "seves" (AMD-Speicherverschlüsselung)
	Enabled    bool      `json:"enabled"`
	Disks      []string  `json:"disks"`
	CDROM      string    `json:"cdrom,omitempty"`
	Boot       string    `json:"boot,omitempty"`
	BootDev    string    `json:"bootDevice,omitempty"` // boot device cdrom|disk|net
	Owner      string    `json:"owner,omitempty"`
	Local      bool      `json:"local,omitempty"`
	Interfaces []VMIface `json:"interfaces"`
	Raw        string    `json:"raw"`
	start, end int
}

type SwitchConf struct {
	Name      string `json:"name"`
	Interface string `json:"interface"`
	Group     string `json:"group,omitempty"`
	Locked    bool   `json:"locked,omitempty"`
	Enabled   bool   `json:"enabled"`
	Raw       string `json:"raw"`
	start     int
	end       int
}

type VMConfFile struct {
	Src      string
	VMs      []*VMConf
	Switches []*SwitchConf
}

func ParseVMConf(src string) *VMConfFile {
	f := &VMConfFile{Src: src}
	toks := tokenize(src)
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		if (t.s != "vm" && t.s != "switch") || t.quoted || i+2 >= len(toks) || toks[i+2].s != "{" {
			continue
		}
		// Blockende über Klammertiefe finden.
		depth, j := 0, i+2
		for ; j < len(toks); j++ {
			if toks[j].s == "{" && !toks[j].quoted {
				depth++
			} else if toks[j].s == "}" && !toks[j].quoted {
				depth--
				if depth == 0 {
					break
				}
			}
		}
		if j >= len(toks) {
			break
		}
		start := lineStart(src, t.start)
		end := lineEnd(src, toks[j].end)
		body := toks[i+3 : j]
		if t.s == "vm" {
			vm := parseVMBody(toks[i+1].s, body)
			vm.Raw, vm.start, vm.end = src[start:end], start, end
			f.VMs = append(f.VMs, vm)
		} else {
			sw := &SwitchConf{Name: toks[i+1].s, Enabled: true, Raw: src[start:end], start: start, end: end}
			for k := 0; k < len(body); k++ {
				switch body[k].s {
				case "interface":
					if k+1 < len(body) {
						sw.Interface = body[k+1].s
					}
				case "disable":
					sw.Enabled = false
				case "group":
					if k+1 < len(body) {
						sw.Group = body[k+1].s
					}
				case "locked":
					sw.Locked = true
				}
			}
			f.Switches = append(f.Switches, sw)
		}
		i = j
	}
	return f
}

func parseVMBody(name string, b []token) *VMConf {
	vm := &VMConf{Name: name, Enabled: true, CPUs: 1, Disks: []string{}, Interfaces: []VMIface{}}
	next := func(k int) string {
		if k+1 < len(b) {
			return b[k+1].s
		}
		return ""
	}
	for k := 0; k < len(b); k++ {
		switch b[k].s {
		case "memory":
			vm.Memory = next(k)
			k++
		case "cpus":
			if n, err := strconv.Atoi(next(k)); err == nil {
				vm.CPUs = n
			}
			k++
		case "sev", "seves":
			vm.SEV = b[k].s
		case "disk":
			vm.Disks = append(vm.Disks, next(k))
			k++
			if next(k) == "format" {
				k += 2
			}
		case "cdrom":
			vm.CDROM = next(k)
			k++
		case "allow":
			// allow instance { … } hat eigene Voreinstellungen, die nicht zur VM gehören.
			for k+1 < len(b) && b[k+1].s != "{" {
				k++
			}
			depth := 0
			for k+1 < len(b) {
				k++
				if b[k].s == "{" && !b[k].quoted {
					depth++
				} else if b[k].s == "}" && !b[k].quoted {
					if depth--; depth <= 0 {
						break
					}
				}
			}
		case "enable":
			vm.Enabled = true
		case "disable":
			vm.Enabled = false
		case "owner":
			vm.Owner = next(k)
			k++
		case "boot":
			if next(k) == "device" {
				vm.Boot = "device " + valueAt(b, k+2)
				vm.BootDev = valueAt(b, k+2)
				k += 2
			} else {
				vm.Boot = next(k)
				k++
			}
		case "local":
			if next(k) == "interface" {
				vm.Local = true
				vm.Interfaces = append(vm.Interfaces, VMIface{Name: "local"})
				k++
			}
		case "interface":
			ifc := VMIface{}
			k++
			if k < len(b) && b[k].s != "{" {
				ifc.Name = b[k].s
				k++
			}
			if k < len(b) && b[k].s == "{" {
				for k++; k < len(b) && b[k].s != "}"; k++ {
					switch b[k].s {
					case "switch":
						ifc.Switch = valueAt(b, k+1)
						k++
					case "lladdr":
						if valueAt(b, k+1) == "locked" {
							k++
						}
						ifc.LLAddr = valueAt(b, k+1)
						k++
					}
				}
			} else {
				k--
			}
			vm.Interfaces = append(vm.Interfaces, ifc)
		}
	}
	return vm
}

func valueAt(b []token, k int) string {
	if k < len(b) {
		return b[k].s
	}
	return ""
}

func lineStart(src string, pos int) int {
	for pos > 0 && src[pos-1] != '\n' {
		pos--
	}
	return pos
}

func lineEnd(src string, pos int) int {
	for pos < len(src) && src[pos] != '\n' {
		pos++
	}
	if pos < len(src) {
		pos++
	}
	return pos
}

func (f *VMConfFile) VM(name string) *VMConf {
	for _, v := range f.VMs {
		if v.Name == name {
			return v
		}
	}
	return nil
}

// Replace ersetzt einen Block (oder entfernt ihn bei leerem repl).
func (f *VMConfFile) replace(start, end int, repl string) string {
	if repl != "" && !strings.HasSuffix(repl, "\n") {
		repl += "\n"
	}
	return f.Src[:start] + repl + f.Src[end:]
}

func (f *VMConfFile) ReplaceVM(name, block string) (string, error) {
	v := f.VM(name)
	if v == nil {
		return "", fmt.Errorf("VM %q ist nicht in vm.conf definiert", name)
	}
	return f.replace(v.start, v.end, block), nil
}

func (f *VMConfFile) Switch(name string) *SwitchConf {
	for _, s := range f.Switches {
		if s.Name == name {
			return s
		}
	}
	return nil
}

// SetSwitch ersetzt einen Switch-Block, entfernt ihn (leerer block) oder
// fügt ihn vor dem ersten VM-Block ein, damit vmd ihn beim Laden kennt.
func (f *VMConfFile) SetSwitch(name, block string) string {
	if s := f.Switch(name); s != nil {
		return f.replace(s.start, s.end, block)
	}
	if block == "" {
		return f.Src
	}
	if len(f.VMs) > 0 {
		at := f.VMs[0].start
		return f.Src[:at] + strings.TrimRight(block, "\n") + "\n\n" + f.Src[at:]
	}
	return f.Append(block)
}

// SetInterfaces ersetzt alle "interface"-Anweisungen eines VM-Blocks durch
// die angegebenen Anschlüsse. "local interface" bleibt erhalten.
func (v *VMConf) SetInterfaces(ifs []VMIface) string {
	raw := v.Raw
	toks := tokenize(raw)
	type span struct{ a, b int }
	var cut []span
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		if t.s != "interface" || t.quoted || (i > 0 && toks[i-1].s == "local") {
			continue
		}
		j := i + 1
		if j < len(toks) && toks[j].s != "{" && j+1 < len(toks) && toks[j+1].s == "{" {
			j++ // benanntes Interface: interface tap3 { … }
		}
		end := t.end
		if j < len(toks) && toks[j].s == "{" {
			for ; j < len(toks) && toks[j].s != "}"; j++ {
			}
			if j < len(toks) {
				end = toks[j].end
			}
		} else if i+1 < len(toks) && toks[i+1].s != "}" && !toks[i+1].quoted {
			end = toks[i+1].end
		}
		// ganze Zeile entfernen, wenn sonst nur Leerraum darauf steht
		a, b := t.start, end
		if ls := lineStart(raw, a); strings.TrimSpace(raw[ls:a]) == "" {
			if le := lineEnd(raw, b); strings.TrimSpace(raw[b:le]) == "" {
				a, b = ls, le
			}
		}
		cut = append(cut, span{a, b})
		for i < len(toks) && toks[i].start < end {
			i++
		}
		i--
	}
	var out strings.Builder
	pos := 0
	for _, c := range cut {
		out.WriteString(raw[pos:c.a])
		pos = c.b
	}
	out.WriteString(raw[pos:])
	s := out.String()
	close := strings.LastIndex(s, "}")
	if close < 0 {
		return s
	}
	var add strings.Builder
	for _, ifc := range ifs {
		add.WriteString("\tinterface { switch " + quote(ifc.Switch))
		if ifc.LLAddr != "" {
			add.WriteString(" lladdr " + ifc.LLAddr)
		}
		add.WriteString(" }\n")
	}
	ls := lineStart(s, close)
	if strings.TrimSpace(s[ls:close]) != "" { // einzeiliger Block
		return s[:close] + "\n" + add.String() + s[close:]
	}
	return s[:ls] + add.String() + s[ls:]
}

func (f *VMConfFile) Append(block string) string {
	s := f.Src
	if s != "" && !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	if s != "" && !strings.HasSuffix(s, "\n\n") {
		s += "\n"
	}
	return s + strings.TrimRight(block, "\n") + "\n"
}

// VMSpec beschreibt eine neue VM aus dem Anlage-Dialog.
type VMSpec struct {
	Name     string   `json:"name"`
	Memory   string   `json:"memory"`
	CPUs     int      `json:"cpus"`
	SEV      string   `json:"sev"`
	DiskSize string   `json:"diskSize"`
	DiskPath string   `json:"diskPath"`
	DiskFmt  string   `json:"diskFormat"`
	BaseDisk string   `json:"baseDisk"`
	CDROM    string   `json:"cdrom"`
	BootCD   bool     `json:"bootCdrom"`
	Switches []string `json:"switches"`
	Local    bool     `json:"local"`
	Owner    string   `json:"owner"`
	Enabled  bool     `json:"enabled"`
	StartNow bool     `json:"startNow"`
}

func quote(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}

func (s *VMSpec) Block() string {
	var b strings.Builder
	fmt.Fprintf(&b, "vm %s {\n", quote(s.Name))
	fmt.Fprintf(&b, "\tmemory %s\n", s.Memory)
	if s.CPUs > 1 {
		fmt.Fprintf(&b, "\tcpus %d\n", s.CPUs)
	}
	if s.SEV != "" {
		fmt.Fprintf(&b, "\t%s\n", s.SEV)
	}
	if s.Enabled {
		b.WriteString("\tenable\n")
	} else {
		b.WriteString("\tdisable\n")
	}
	if s.DiskPath != "" {
		fmt.Fprintf(&b, "\tdisk %s\n", quote(s.DiskPath))
	}
	if s.CDROM != "" {
		fmt.Fprintf(&b, "\tcdrom %s\n", quote(s.CDROM))
		if s.BootCD {
			b.WriteString("\tboot device cdrom\n")
		}
	}
	if s.Local {
		b.WriteString("\tlocal interface\n")
	}
	for _, sw := range s.Switches {
		fmt.Fprintf(&b, "\tinterface { switch %s }\n", quote(sw))
	}
	if s.Owner != "" {
		fmt.Fprintf(&b, "\towner %s\n", s.Owner)
	}
	b.WriteString("}\n")
	return b.String()
}

// ---- Strukturierte Änderung eines VM-Blocks ----

// VMEdit sind die Einstellungen, die die Oberfläche per Auswahl ändert. Alle
// übrigen Anweisungen des Blocks (interface, boot <Kernel>, allow instance, …)
// bleiben unberührt.
type VMEdit struct {
	Memory     string   `json:"memory"`
	CPUs       int      `json:"cpus"`
	SEV        string   `json:"sev"` // "", sev, seves
	Enabled    bool     `json:"enabled"`
	Disks      []string `json:"disks"`
	CDROM      string   `json:"cdrom"`
	BootDevice string   `json:"bootDevice"` // "", cdrom, disk, net
	Owner      string   `json:"owner"`
	Local      bool     `json:"local"`
}

// Edit liest die aktuellen Einstellungen zurück.
func (v *VMConf) Edit() VMEdit {
	e := VMEdit{Memory: v.Memory, CPUs: v.CPUs, SEV: v.SEV, Enabled: v.Enabled, Disks: append([]string{}, v.Disks...), CDROM: v.CDROM, Owner: v.Owner, Local: v.Local}
	e.BootDevice = v.BootDev
	return e
}

var vmDirectives = map[string]bool{"memory": true, "cpus": true, "cdrom": true, "owner": true, "sev": true, "seves": true, "enable": true, "disable": true,
	"disk": true, "boot": true, "local": true, "interface": true, "interfaces": true, "allow": true, "instance": true}

// skipBlock liefert den Index nach der schließenden Klammer, wenn toks[i] eine öffnende ist.
func skipBlock(toks []token, i, limit int) int {
	if i >= limit || toks[i].s != "{" || toks[i].quoted {
		return i
	}
	depth := 0
	for ; i < limit; i++ {
		if toks[i].quoted {
			continue
		}
		if toks[i].s == "{" {
			depth++
		} else if toks[i].s == "}" {
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return limit
}

type textEdit struct {
	a, b int
	repl string
}

// ApplyEdit ersetzt die zu e gehörenden Anweisungen im Block und lässt alles andere stehen.
func (v *VMConf) ApplyEdit(e VMEdit, formats map[string]string) string {
	raw := v.Raw
	toks := tokenize(raw)
	var edits []textEdit
	open, closeIdx := -1, len(toks)
	for i, t := range toks {
		if t.s == "{" && !t.quoted {
			open = i
			break
		}
	}
	if open < 0 {
		return raw
	}
	if end := skipBlock(toks, open, len(toks)); end <= len(toks) && end > open {
		closeIdx = end - 1
	}
	cut := func(a, b int) {
		if ls := lineStart(raw, a); strings.TrimSpace(raw[ls:a]) == "" {
			if le := lineEnd(raw, b); strings.TrimSpace(raw[b:le]) == "" {
				a, b = ls, le
			}
		}
		edits = append(edits, textEdit{a: a, b: b})
	}
	hadLocal := false
	for i := open + 1; i < closeIdx; {
		t := toks[i]
		end, remove := i+1, false
		if !t.quoted {
			switch t.s {
			case "memory", "cpus", "cdrom", "owner":
				end, remove = i+2, true
			case "sev", "seves", "enable", "disable":
				end, remove = i+1, true
			case "disk":
				end, remove = i+2, true
				if end < closeIdx && toks[end].s == "format" {
					end += 2
				}
			case "boot":
				if i+1 < closeIdx && toks[i+1].s == "device" {
					end, remove = i+3, true
				} else {
					end = i + 2
				}
			case "local":
				end = i + 2
				for end < closeIdx && !vmDirectives[toks[end].s] && toks[end].s != "{" {
					end++
				}
				end = skipBlock(toks, end, closeIdx)
				hadLocal = true
				remove = !e.Local
			case "interface":
				for end < closeIdx && !vmDirectives[toks[end].s] && toks[end].s != "{" {
					end++
				}
				end = skipBlock(toks, end, closeIdx)
			default:
				for end < closeIdx && !vmDirectives[toks[end].s] && toks[end].s != "{" {
					end++
				}
				end = skipBlock(toks, end, closeIdx)
			}
		}
		if end > closeIdx {
			end = closeIdx
		}
		if remove && end > i {
			cut(toks[i].start, toks[end-1].end)
		}
		i = end
	}
	var b strings.Builder
	line := func(f string, a ...any) { fmt.Fprintf(&b, "\n\t"+f, a...) }
	line("memory %s", e.Memory)
	if e.CPUs > 1 {
		line("cpus %d", e.CPUs)
	}
	if e.SEV != "" {
		line("%s", e.SEV)
	}
	if e.Enabled {
		line("enable")
	} else {
		line("disable")
	}
	for _, d := range e.Disks {
		if f := formats[d]; f != "" {
			line("disk %s format %s", quote(d), f)
		} else {
			line("disk %s", quote(d))
		}
	}
	if e.CDROM != "" {
		line("cdrom %s", quote(e.CDROM))
	}
	if e.BootDevice != "" {
		line("boot device %s", e.BootDevice)
	}
	if e.Owner != "" {
		line("owner %s", e.Owner)
	}
	if e.Local && !hadLocal {
		line("local interface")
	}
	edits = append(edits, textEdit{a: toks[open].end, b: toks[open].end, repl: b.String()})
	// von hinten nach vorn anwenden, damit die Positionen gültig bleiben
	sort.Slice(edits, func(i, j int) bool { return edits[i].a > edits[j].a })
	out := raw
	for _, ed := range edits {
		out = out[:ed.a] + ed.repl + out[ed.b:]
	}
	return out
}
