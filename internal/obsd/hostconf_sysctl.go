package obsd

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const sysctlConfPath = "/etc/sysctl.conf"

// Kernel-Parameter für /etc/sysctl.conf als Formular. Zur Auswahl stehen die Parameter,
// die der laufende Kernel hat; häufige haben Beschreibung, Wertebereich und Hinweise.
// Beim Speichern prüft maar, dass der Parameter schreibbar ist, und kann ihn sofort setzen.

type SysctlEntry struct {
	ID    int    `json:"id"` // Zeilennummer, 0 = neu
	Key   string `json:"key"`
	Value string `json:"value"`
	Raw   string `json:"raw,omitempty"` // Zeile, die das Formular nicht versteht
}

type SysctlInfo struct {
	Key     string   `json:"key"`
	Label   string   `json:"label"`
	Desc    string   `json:"description"`
	Kind    string   `json:"kind"` // bool, int, enum, string
	Options []string `json:"options,omitempty"`
	Min     int64    `json:"min"`
	Max     int64    `json:"max"`
	Risk    string   `json:"risk,omitempty"`
	Group   string   `json:"group"`
	Current string   `json:"current"`
}

type SysctlConfView struct {
	Entries []SysctlEntry     `json:"entries"`
	Catalog []SysctlInfo      `json:"catalog"`
	All     []string          `json:"all"`     // alle Parameter dieses Kernels
	Current map[string]string `json:"current"` // aktuelle Werte der Einträge
	Header  []string          `json:"header"`
}

var sysctlCatalog = []SysctlInfo{
	{Key: "net.inet.ip.forwarding", Group: "Netzwerk", Label: "IPv4-Weiterleitung (Router)", Desc: "Pakete zwischen Netzen weiterreichen. Nötig, wenn der Host VMs oder Netze routet.", Kind: "bool"},
	{Key: "net.inet6.ip6.forwarding", Group: "Netzwerk", Label: "IPv6-Weiterleitung (Router)", Desc: "Wie oben für IPv6.", Kind: "bool"},
	{Key: "net.inet.ip.redirect", Group: "Netzwerk", Label: "ICMP-Redirects senden", Desc: "Andere Hosts auf bessere Gateways hinweisen. Auf Routern meist aus.", Kind: "bool"},
	{Key: "net.inet.ip.sourceroute", Group: "Netzwerk", Label: "Source-Routing erlauben", Desc: "Absender bestimmt den Weg – unsicher.", Kind: "bool", Risk: "Source-Routing ermöglicht Umgehung von Filtern; nur für Sonderfälle einschalten."},
	{Key: "net.inet.icmp.errppslimit", Group: "Netzwerk", Label: "ICMP-Fehlermeldungen pro Sekunde", Desc: "Begrenzt ICMP-Fehlerpakete.", Kind: "int", Min: 0, Max: 100000},
	{Key: "net.inet.tcp.sack", Group: "Netzwerk", Label: "TCP Selective Acknowledgements", Desc: "Schnellere Erholung bei Paketverlust.", Kind: "bool"},
	{Key: "net.inet.tcp.mssdflt", Group: "Netzwerk", Label: "Standard-TCP-Segmentgröße", Desc: "MSS ohne Pfad-MTU-Information.", Kind: "int", Min: 64, Max: 65535},
	{Key: "net.inet.udp.recvspace", Group: "Netzwerk", Label: "UDP-Empfangspuffer (Bytes)", Desc: "Größer bei hohem UDP-Aufkommen (z. B. DNS-Server).", Kind: "int", Min: 1024, Max: 4194304},
	{Key: "kern.somaxconn", Group: "Kernel", Label: "Maximale Warteschlange für listen()", Desc: "Höher bei vielen gleichzeitigen Verbindungen (Webserver, Proxy).", Kind: "int", Min: 1, Max: 65535},
	{Key: "kern.maxfiles", Group: "Kernel", Label: "Offene Dateien systemweit", Desc: "Obergrenze für offene Dateien aller Prozesse.", Kind: "int", Min: 64, Max: 10000000},
	{Key: "kern.maxproc", Group: "Kernel", Label: "Prozesse systemweit", Desc: "Obergrenze für Prozesse.", Kind: "int", Min: 32, Max: 1000000},
	{Key: "kern.maxthread", Group: "Kernel", Label: "Threads systemweit", Desc: "Obergrenze für Threads.", Kind: "int", Min: 32, Max: 1000000},
	{Key: "kern.bufcachepercent", Group: "Speicher", Label: "Puffercache (Prozent des Speichers)", Desc: "Mehr Cache beschleunigt Dateizugriffe, lässt aber weniger Speicher für VMs.", Kind: "int", Min: 5, Max: 90},
	{Key: "kern.audio.record", Group: "Kernel", Label: "Audioaufnahme erlauben", Desc: "Mikrofonzugriff für Programme.", Kind: "bool"},
	{Key: "kern.allowkmem", Group: "Sicherheit", Label: "Zugriff auf /dev/kmem erlauben", Desc: "Nur für alte Diagnosewerkzeuge.", Kind: "bool", Risk: "Senkt die Sicherheit des Kernels deutlich; nur zur Fehlersuche."},
	{Key: "machdep.allowaperture", Group: "Sicherheit", Label: "Zugriff auf den Grafikspeicher (Aperture)", Desc: "Für X-Server ohne KMS.", Kind: "enum", Options: []string{"0", "1", "2"}, Risk: "Erlaubt Programmen Zugriff auf Hardware-Speicher; nur bei Bedarf."},
	{Key: "kern.securelevel", Group: "Sicherheit", Label: "Sicherheitsstufe", Desc: "Höhere Stufen sperren Änderungen an Dateien und Kernel. Sie lässt sich nur im Einzelbenutzerbetrieb senken.", Kind: "enum", Options: []string{"-1", "0", "1", "2"}, Risk: "Stufe 2 verhindert u. a. das Setzen der Uhr und das Laden von Regeln – ohne Neustart nicht rückgängig zu machen."},
	{Key: "vm.swapencrypt.enable", Group: "Speicher", Label: "Swap verschlüsseln", Desc: "Verschlüsselt ausgelagerten Speicher.", Kind: "bool"},
	{Key: "hw.smt", Group: "Hardware", Label: "Simultanes Multithreading (SMT)", Desc: "Aus = Hyperthreads ungenutzt (sicherer gegen Seitenkanal-Angriffe, weniger Leistung für VMs).", Kind: "bool"},
	{Key: "hw.perfpolicy", Group: "Hardware", Label: "CPU-Leistungsrichtlinie", Desc: "auto, manual oder eine Prozentzahl.", Kind: "enum", Options: []string{"auto", "manual", "high"}},
}

func (c SysctlInfo) check(v string) error {
	switch c.Kind {
	case "bool":
		if v != "0" && v != "1" {
			return invalid("%s: nur 0 oder 1", c.Key)
		}
	case "int":
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < c.Min || n > c.Max {
			return invalid("%s: ganze Zahl von %d bis %d", c.Key, c.Min, c.Max)
		}
	case "enum":
		for _, o := range c.Options {
			if o == v {
				return nil
			}
		}
		return invalid("%s: erlaubt sind %s", c.Key, strings.Join(c.Options, ", "))
	}
	return nil
}

var (
	reSysctlInt = regexp.MustCompile(`^-?[0-9]{1,18}$`)
	reSysctlStr = regexp.MustCompile(`^[A-Za-z0-9_.,:/@+-]{0,200}$`)
)

func parseSysctlLine(l string) (SysctlEntry, bool) {
	t := strings.TrimSpace(l)
	if t == "" || strings.HasPrefix(t, "#") {
		return SysctlEntry{}, false
	}
	k, v, ok := strings.Cut(t, "=")
	if !ok || strings.HasSuffix(k, "+") || !reSysctl.MatchString(strings.TrimSpace(k)) {
		return SysctlEntry{Raw: t}, true
	}
	v = strings.TrimSpace(v)
	if i := strings.Index(v, "#"); i >= 0 {
		return SysctlEntry{Raw: t}, true // Kommentar hinter dem Wert: unverändert lassen
	}
	return SysctlEntry{Key: strings.TrimSpace(k), Value: v}, true
}

func (h *Host) sysctlValues(ctx context.Context) (map[string]string, []string) {
	vals := map[string]string{}
	var keys []string
	list, err := h.Sysctls(ctx)
	if err != nil {
		return vals, keys
	}
	for _, s := range list {
		vals[s.Name] = s.Value
		keys = append(keys, s.Name)
	}
	sort.Strings(keys)
	return vals, keys
}

func (h *Host) SysctlConf(ctx context.Context) (*SysctlConfView, error) {
	src, err := h.readFile(sysctlConfPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	vals, keys := h.sysctlValues(ctx)
	v := &SysctlConfView{Entries: []SysctlEntry{}, Catalog: []SysctlInfo{}, All: keys, Current: map[string]string{}, Header: []string{}}
	for i, l := range splitFileLines(src) {
		if e, ok := parseSysctlLine(l); ok {
			e.ID = i + 1
			v.Entries = append(v.Entries, e)
			if e.Key != "" {
				v.Current[e.Key] = vals[e.Key]
			}
		} else if len(v.Entries) == 0 {
			v.Header = append(v.Header, l)
		}
	}
	for _, c := range sysctlCatalog {
		if cur, ok := vals[c.Key]; ok { // nur, was dieser Kernel kennt
			c.Current = cur
			v.Catalog = append(v.Catalog, c)
		}
	}
	return v, nil
}

func (h *Host) SaveSysctlConf(ctx context.Context, entries []SysctlEntry, applyNow, preview bool) (*ConfResult, error) {
	cur, err := h.SysctlConf(ctx)
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, k := range cur.All {
		known[k] = true
	}
	cat := map[string]SysctlInfo{}
	for _, c := range sysctlCatalog {
		cat[c.Key] = c
	}
	vals, _ := h.sysctlValues(ctx)
	src, _ := h.readFile(sysctlConfPath)
	orig := splitFileLines(src)
	if len(entries) > 500 {
		return nil, invalid("zu viele Einträge")
	}
	rawOK := map[string]bool{}
	oldVal := map[string]string{}
	for _, e := range cur.Entries {
		if e.Raw != "" {
			rawOK[e.Raw] = true
		} else {
			oldVal[e.Key] = e.Value
		}
	}
	byID := map[int]SysctlEntry{}
	seen := map[string]bool{}
	res := &ConfResult{Warnings: []string{}}
	var changed []SysctlEntry
	for i := range entries {
		e := &entries[i]
		if e.Raw != "" {
			if !rawOK[e.Raw] {
				return nil, invalid("Rohzeilen lassen sich nur übernehmen, nicht neu anlegen")
			}
			if e.ID > 0 {
				byID[e.ID] = *e
			}
			continue
		}
		e.Key, e.Value = strings.TrimSpace(e.Key), strings.TrimSpace(e.Value)
		if !reSysctl.MatchString(e.Key) || !known[e.Key] {
			return nil, invalid("Der Parameter %q existiert in diesem Kernel nicht", e.Key)
		}
		if seen[e.Key] {
			return nil, invalid("%s ist doppelt", e.Key)
		}
		seen[e.Key] = true
		if c, ok := cat[e.Key]; ok {
			if err := c.check(e.Value); err != nil {
				return nil, err
			}
			if c.Risk != "" && oldVal[e.Key] != e.Value {
				res.Warnings = append(res.Warnings, fmt.Sprintf("%s: %s", e.Key, c.Risk))
			}
		} else {
			if e.Value == "" && vals[e.Key] != "" {
				return nil, invalid("%s: der Wert darf nicht leer sein", e.Key)
			}
			if reSysctlInt.MatchString(vals[e.Key]) && !reSysctlInt.MatchString(e.Value) {
				return nil, invalid("%s: erwartet eine ganze Zahl (aktuell %s)", e.Key, vals[e.Key])
			}
			if !reSysctlStr.MatchString(e.Value) {
				return nil, invalid("%s: Wert enthält unzulässige Zeichen", e.Key)
			}
		}
		if oldVal[e.Key] != e.Value {
			changed = append(changed, *e)
		}
		if e.ID > 0 {
			if e.ID > len(orig) {
				return nil, invalid("Eintrag %d gibt es nicht mehr – bitte neu laden", e.ID)
			}
			byID[e.ID] = *e
		}
	}
	line := func(e SysctlEntry) string {
		if e.Raw != "" {
			return e.Raw
		}
		return e.Key + "=" + e.Value
	}
	var out []string
	for i, l := range orig {
		if _, isEntry := parseSysctlLine(l); !isEntry {
			out = append(out, l)
			continue
		}
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
	for k := range oldVal {
		if !seen[k] {
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s wird aus der Datei entfernt; der laufende Wert bleibt bis zum Neustart.", k))
		}
	}
	if preview {
		res.Content = content
		return res, nil
	}
	// Schreibbarkeit prüfen: den aktuellen Wert noch einmal setzen (ändert nichts).
	for _, e := range changed {
		if vals[e.Key] == "" && e.Value != "" {
			continue
		}
		if err := h.run(ctx, "sysctl", e.Key+"="+vals[e.Key]); err != nil {
			return nil, invalid("%s lässt sich auf diesem System nicht setzen (nur lesbar?): %v", e.Key, err)
		}
	}
	if src != content {
		r, err := h.WriteConfig(ctx, sysctlConfPath, content, 0)
		if err != nil {
			return nil, err
		}
		res.Output = r.Check
	} else {
		res.Output = "Keine Änderung der Datei."
	}
	if applyNow {
		var failed []string
		for _, e := range changed {
			if err := h.run(ctx, "sysctl", e.Key+"="+e.Value); err != nil {
				failed = append(failed, e.Key)
			}
		}
		if len(failed) > 0 {
			return nil, fmt.Errorf("Datei gespeichert, aber %s ließ sich nicht sofort setzen", strings.Join(failed, ", "))
		}
		if len(changed) > 0 {
			res.Applied = fmt.Sprintf("Gespeichert und %d Wert(e) sofort gesetzt.", len(changed))
		} else {
			res.Applied = "Gespeichert."
		}
	} else {
		res.Applied = "Gespeichert. Die Werte gelten ab dem nächsten Start."
	}
	return res, nil
}
