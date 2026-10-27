package obsd

import (
	"context"
	"embed"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"math"
	"math/rand"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed all:demofs
var demoFS embed.FS

// DemoRunner simuliert einen OpenBSD-8.0-Host für Entwicklung und Vorführung.
// Zustandsänderungen (VMs, Dienste, pf, Pakete) werden im Speicher gehalten.
type DemoRunner struct {
	mu      sync.Mutex
	root    string
	boot    time.Time
	vms     map[string]*demoVM
	nextID  int
	on      map[string]bool
	started map[string]bool
	flags   map[string]string
	pfOn    bool
	pkgs    map[string]string
	patches []string
	tables  map[string][]string
	sanDuid map[string]string // SAN-Disk → DUID (nach disklabel)
	sanMnt  map[string]string // Gerät → Einhängepunkt der Datastores
	sanSeq  int
	ifs     map[string]string // per netstart erzeugte oder zerstörte Interfaces ("" = zerstört)

	// Zähler für Leistungsdaten
	cpuTicks []int64
	netCtr   map[string][2]int64
	ctrAt    time.Time
}

type demoVM struct {
	id, pid, tty, state string
	vcpus               int
	mem                 string
}

// NewDemo legt eine Sandbox mit Beispieldateien an und liefert Runner und Wurzel.
func NewDemo() (*DemoRunner, string, error) {
	root, err := os.MkdirTemp("", "maar-demo-")
	if err != nil {
		return nil, "", err
	}
	err = fs.WalkDir(demoFS, "demofs", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(p, "demofs")
		dst := filepath.Join(root, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		b, err := demoFS.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o644)
	})
	if err != nil {
		return nil, "", err
	}
	// Spärliche Disk-Images, damit Größen realistisch aussehen.
	for name, size := range map[string]int64{
		"var/vmd/web01.qcow2": 3 << 30, "var/vmd/db01.qcow2": 9 << 30, "var/vmd/build.qcow2": 14 << 30,
		"var/vmd/alpine-base.qcow2": 600 << 20, "var/vmd/iso/install80.iso": 712 << 20,
		"var/vmd/iso/alpine-virt-3.22.iso": 64 << 20,
	} {
		p := filepath.Join(root, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		f, err := os.Create(p)
		if err == nil {
			f.Truncate(size)
			f.Close()
		}
	}
	os.MkdirAll(filepath.Join(root, "var/vmd/templates"), 0o755)
	for name, meta := range map[string]string{
		"openbsd80-base": `{"name":"openbsd80-base","description":"OpenBSD 8.0, frisch installiert, mit maar-guest","os":"openbsd","memory":"1G","cpus":2,"source":"VM build","created":1792900000}`,
		"debian13-cloud": `{"name":"debian13-cloud","description":"Debian 13 genericcloud mit cloud-init","os":"linux","memory":"2G","cpus":2,"source":"https://cloud.debian.org/images/cloud/trixie/latest/debian-13-genericcloud-amd64.qcow2","created":1792800000}`,
	} {
		writeFakeQcow2(filepath.Join(root, "var/vmd/templates", name+".qcow2"), "", 1200<<20)
		os.WriteFile(filepath.Join(root, "var/vmd/templates", name+".json"), []byte(meta), 0o644)
	}
	writeFakeQcow2(filepath.Join(root, "var/vmd/web01.qcow2"), "/var/vmd/templates/openbsd80-base.qcow2", 3<<30)
	for _, n := range []string{"db01", "build"} {
		writeFakeQcow2(filepath.Join(root, "var/vmd", n+".qcow2"), "", map[string]int64{"db01": 9 << 30, "build": 14 << 30}[n])
	}
	seedDemoBackups(root)
	os.MkdirAll(filepath.Join(root, "usr/local/bin"), 0o755)
	os.WriteFile(filepath.Join(root, "usr/local/bin/qemu-img"), []byte("#!/bin/sh\n"), 0o755)
	d := &DemoRunner{
		root: root, boot: time.Now().Add(-(11*24*time.Hour + 4*time.Hour + 17*time.Minute)),
		vms: map[string]*demoVM{
			"web01": {id: "1", pid: "48211", tty: "ttyp0", state: "running", vcpus: 1, mem: "1.0G"},
			"db01":  {id: "2", pid: "51907", tty: "ttyp1", state: "running", vcpus: 4, mem: "2.0G"},
		},
		nextID:  3,
		on:      map[string]bool{},
		started: map[string]bool{},
		flags:   map[string]string{"httpd": "", "sshd": "", "vmd": "", "unbound": "", "ntpd": "-s", "smtpd": "", "dhcpd": "vport0"},
		pfOn:    true,
		pkgs:    map[string]string{},
		patches: []string{"078_xserver", "079_libssl"},
		ifs:     map[string]string{},
		sanDuid: map[string]string{"sd2": "c0ffee00deadbeef"},
		sanMnt:  map[string]string{"/dev/sd2a": "/var/vmd/vm-pool"},
		tables:  map[string][]string{"bruteforce": {"185.220.101.4", "45.95.147.231"}, "lan": {"10.0.0.0/24", "192.168.50.0/24"}},
	}
	d.on["iscsid"], d.started["iscsid"] = true, true
	for _, s := range []string{"pf", "library_aslr", "check_quotas", "cron", "dhcpd", "httpd", "ntpd", "pflogd", "slaacd", "smtpd", "sndiod", "sshd", "syslogd", "unbound", "vmd", "dhcpleased", "resolvd"} {
		d.on[s] = true
	}
	for _, s := range []string{"cron", "dhcpd", "httpd", "ntpd", "pflogd", "slaacd", "smtpd", "sshd", "syslogd", "unbound", "vmd", "dhcpleased", "resolvd"} {
		d.started[s] = true
	}
	for _, l := range lines(demoPkgInfo) {
		n, c, _ := strings.Cut(l, " ")
		d.pkgs[n] = strings.TrimSpace(c)
	}
	// Dienste passend zur mitgelieferten network.json erzeugen.
	if err := NewHost(d, root, true).SyncNetwork().Run(context.Background(), io.Discard); err != nil {
		return nil, "", fmt.Errorf("Demo-Netz: %w", err)
	}
	return d, root, nil
}

func (d *DemoRunner) Root() string { return d.root }

func ok(s string) (Result, error) { return Result{Stdout: []byte(s)}, nil }

func fail(cmd, msg string, code int) (Result, error) {
	return Result{Stderr: []byte(msg), Code: code}, &CmdError{Cmd: cmd, Code: code, Stderr: msg}
}

func (d *DemoRunner) Run(ctx context.Context, stdin []byte, name string, args ...string) (Result, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	time.Sleep(40 * time.Millisecond)
	a := strings.Join(args, " ")
	switch name {
	case "sysctl":
		return d.sysctl(args)
	case "top":
		return ok(fmt.Sprintf(demoTop, d.uptimeStr()))
	case "ps":
		if len(args) > 0 && args[0] == "-o" {
			return ok(d.psVMs(args))
		}
		return ok(demoPs)
	case "vmstat":
		active := 2_570_000 + int64(time.Now().Unix()%60)*4000
		return ok(fmt.Sprintf("     4096 bytes per page\n  8340478 pages managed\n  5523123 pages free\n  %7d pages active\n   612004 pages inactive\n", active))
	case "netstat":
		if len(args) > 0 && args[0] == "-ibn" {
			return ok(d.netstat())
		}
		if strings.Contains(strings.Join(args, " "), "inet6") {
			return ok(demoRoutes6)
		}
		return ok(demoRoutes)
	case "df":
		return ok(demoDf + d.sanDf())
	case "mount":
		if len(args) > 0 {
			return d.sanMount(args[0])
		}
		return ok(demoMount + d.sanMountLines())
	case "smtpctl":
		return ok("table update requested\n")
	case "ntpctl":
		return ok(demoNtpctl)
	case "dmesg":
		return ok(demoDmesg)
	case "fdisk", "newfs":
		if name == "fdisk" && !d.sanDisk(val(args, len(args)-1)) || name == "newfs" && !d.sanDisk(strings.TrimSuffix(val(args, len(args)-1), "a")) {
			return fail(name, name+": "+val(args, len(args)-1)+": No such file or directory\n", 1)
		}
		return ok("")
	case "iscsictl":
		if val(args, 0) == "show" {
			return ok(d.iscsiSummary())
		}
		return ok("")
	case "disklabel":
		return d.disklabel(args, stdin)
	case "ifconfig":
		if a == "-A" || a == "" {
			return ok(d.ifconfig())
		}
		if len(args) == 2 && args[1] == "destroy" {
			d.ifs[args[0]] = ""
		}
		return ok("")

	case "vmctl":
		return d.vmctl(args)
	case "vmd", "pfctl", "httpd", "relayd", "smtpd", "doas", "sshd", "ntpd", "unbound-checkconf", "dhcpd", "iked", "bgpd", "acme-client", "iscsid":
		if name == "pfctl" && !strings.HasPrefix(a, "-n") {
			return d.pfctl(args)
		}
		return d.check(name, args)
	case "rcctl":
		return d.rcctl(args)
	case "pkg_info":
		return d.pkgInfo(args)
	case "syspatch":
		switch a {
		case "-l":
			return ok("077_vmd\n076_unwind\n075_xserver\n")
		case "-c":
			return ok(strings.Join(d.patches, "\n") + "\n")
		}
		return ok("")
	case "encrypt":
		return ok("$2b$10$Rlj0Q8nJm2wz0kLD4QXfDOP2hJ3rS3Vq3QvL3Sx3m6yR5pN8hU1OK\n")
	case "useradd", "usermod", "userdel", "groupadd", "groupdel":
		return d.users(name, args)
	case "crontab":
		if len(args) > 0 && args[0] == "-l" {
			if val(args, 2) == "root" {
				return ok(demoCrontab)
			}
			return fail("crontab", "crontab: no crontab for "+val(args, 2)+"\n", 1)
		}
		return ok("")
	case "kill", "shutdown", "pkill":
		return ok("")
	case "umount":
		for dev, mp := range d.sanMnt {
			if mp == val(args, 0) {
				delete(d.sanMnt, dev)
			}
		}
		return ok("")
	case "mail":
		// mail -s betreff empfänger… – in der Sandbox an /var/mail/<empfänger> anhängen
		os.MkdirAll(filepath.Join(d.root, "var/mail"), 0o700)
		for _, r := range args[2:] {
			f, err := os.OpenFile(filepath.Join(d.root, "var/mail", filepath.Base(r)), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
			if err == nil {
				fmt.Fprintf(f, "From maar %s\nSubject: %s\n\n%s\n", time.Now().Format(time.ANSIC), val(args, 1), stdin)
				f.Close()
			}
		}
		return ok("")
	case "ssh-keygen":
		for i, a := range args {
			if a == "-f" {
				p := filepath.Join(d.root, val(args, i+1))
				os.WriteFile(p, []byte("-----BEGIN OPENSSH PRIVATE KEY-----\n(Demo)\n"), 0o600)
				os.WriteFile(p+".pub", []byte("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIDemoDemoDemoDemoDemoDemoDemoDemoDemo maar-backup@nordlicht.ctw.lan\n"), 0o644)
			}
		}
		return ok("")
	case "makefs":
		// makefs -t cd9660 -o … image directory
		p := filepath.Join(d.root, val(args, len(args)-2))
		if err := os.WriteFile(p, make([]byte, 64<<10), 0o600); err != nil {
			return fail("makefs", err.Error(), 1)
		}
		return ok("")
	case "sh":
		if val(args, 0) == "/etc/netstart" {
			for _, i := range args[1:] {
				d.ifs[i] = d.synthIf(i)
			}
		}
		return ok("")
	}
	return fail(name, name+": im Demo-Modus nicht simuliert", 127)
}

func (d *DemoRunner) uptimeStr() string {
	up := time.Since(d.boot)
	return fmt.Sprintf("up %d days, %2d:%02d", int(up.Hours())/24, int(up.Hours())%24, int(up.Minutes())%60)
}

// advance lässt die Demo-Zähler mit der verstrichenen Zeit wachsen.
func (d *DemoRunner) advance() {
	now := time.Now()
	if d.cpuTicks == nil {
		d.cpuTicks = []int64{50000, 0, 20000, 1000, 800, 900000}
		d.netCtr = map[string][2]int64{"em0": {9e10, 4e10}, "em1": {0, 0}, "lo0": {1e6, 1e6}, "tap0": {3e9, 5e9}, "tap1": {1e9, 2e9}, "vport0": {1e9, 1e9}}
		d.ctrAt = now.Add(-10 * time.Second)
	}
	dt := now.Sub(d.ctrAt).Seconds()
	d.ctrAt = now
	h := float64(now.Hour()) + float64(now.Minute())/60
	load := 0.5 + 0.5*math.Sin((h-9)/24*2*math.Pi)
	busy := 0.08 + 0.2*load + rand.Float64()*0.06
	ticks := dt * 800
	d.cpuTicks[0] += int64(ticks * busy * 0.65)
	d.cpuTicks[2] += int64(ticks * busy * 0.3)
	d.cpuTicks[4] += int64(ticks * busy * 0.05)
	d.cpuTicks[5] += int64(ticks * (1 - busy))
	rates := map[string][2]float64{"em0": {2e5 + 2.4e6*load, 1e5 + 0.9e6*load}, "tap0": {8e4 + 2.1e6*load, 4e4 + 1.6e6*load}, "tap1": {1.5e4 + 0.4e6*load, 2e4 + 0.6e6*load}}
	for k, r := range rates {
		c := d.netCtr[k]
		j := 0.85 + rand.Float64()*0.3
		d.netCtr[k] = [2]int64{c[0] + int64(r[0]*dt*j), c[1] + int64(r[1]*dt*j)}
	}
}

func (d *DemoRunner) netstat() string {
	d.advance()
	var b strings.Builder
	b.WriteString("Name    Mtu   Network     Address              Ibytes       Obytes\n")
	for _, n := range []string{"lo0", "em0", "em1", "veb0", "vport0", "tap0", "tap1"} {
		c := d.netCtr[n]
		fmt.Fprintf(&b, "%-7s %5d <Link#1>    fe:e1:ba:d0:00:01 %12d %12d\n", n, 1500, c[0], c[1])
		if n == "em0" {
			fmt.Fprintf(&b, "%-7s %5d 192.168.50/ 192.168.50.10     %12d %12d\n", n, 1500, c[0], c[1])
		}
	}
	return b.String()
}

func (d *DemoRunner) psVMs(args []string) string {
	pids := map[string]bool{}
	for _, p := range strings.Split(val(args, len(args)-1), ",") {
		pids[p] = true
	}
	h := float64(time.Now().Hour())
	load := 0.5 + 0.5*math.Sin((h-9)/24*2*math.Pi)
	var b strings.Builder
	b.WriteString("  PID %CPU   RSS\n")
	for name, v := range d.vms {
		if !pids[v.pid] || v.state != "running" {
			continue
		}
		cpu := 3 + 28*load + rand.Float64()*6
		rss := 742000 + rand.Int63n(30000)
		if name == "db01" {
			cpu, rss = 8+35*load+rand.Float64()*8, 1670000+rand.Int63n(60000)
		}
		fmt.Fprintf(&b, "%5s %4.1f %7d\n", v.pid, cpu, rss)
	}
	return b.String()
}

func (d *DemoRunner) sysctl(args []string) (Result, error) {
	if len(args) == 1 && args[0] == "hw.sensors" {
		return ok("hw.sensors.cpu0.temp0=48.00 degC\nhw.sensors.udcf0.timedelta0=0.001840 secs (ok), OK, Mon Oct 27 14:02:11.000\nhw.sensors.gps0.timedelta0=-0.000212 secs (ok), OK\n")
	}
	if len(args) == 2 && args[0] == "-n" && args[1] == "kern.cp_time" {
		d.advance()
		s := make([]string, len(d.cpuTicks))
		for i, t := range d.cpuTicks {
			s[i] = fmt.Sprint(t)
		}
		return ok(strings.Join(s, ",") + "\n")
	}
	if len(args) == 2 && args[0] == "-n" && args[1] == "vm.loadavg" {
		l := 0.3 + rand.Float64()*0.4
		return ok(fmt.Sprintf("%.2f %.2f %.2f\n", l, l*0.9, l*0.8))
	}
	vals := map[string]string{
		"kern.hostname": "nordlicht.ctw.lan", "kern.ostype": "OpenBSD", "kern.osrelease": "8.0",
		"hw.machine": "amd64", "hw.model": "AMD Ryzen 7 7840HS w/ Radeon 780M Graphics", "hw.ncpuonline": "8",
		"hw.physmem": "34163654656", "kern.boottime": fmt.Sprint(d.boot.Unix()),
		"kern.version": "OpenBSD 8.0 (GENERIC.MP) #54: Sun Oct 12 21:13:46 MDT 2026\n    deraadt@amd64.openbsd.org:/usr/src/sys/arch/amd64/compile/GENERIC.MP\n",
		"hw.disknames": d.disknames(),
	}
	if len(args) > 0 && args[0] == "-a" {
		keys := make([]string, 0, len(vals))
		for k := range vals {
			if k != "kern.version" {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		var b strings.Builder
		for _, k := range keys {
			fmt.Fprintf(&b, "%s=%s\n", k, vals[k])
		}
		b.WriteString(demoSysctlExtra)
		return ok(b.String())
	}
	n := args[0] == "-n"
	if n {
		args = args[1:]
	}
	var b strings.Builder
	for _, k := range args {
		if strings.Contains(k, "=") {
			return ok(strings.Replace(k, "=", ": -> ", 1) + "\n")
		}
		if n {
			b.WriteString(vals[k] + "\n")
		} else {
			fmt.Fprintf(&b, "%s=%s\n", k, vals[k])
		}
	}
	return ok(b.String())
}

func (d *DemoRunner) vmctl(args []string) (Result, error) {
	if len(args) == 0 {
		return fail("vmctl", "usage: vmctl command", 1)
	}
	target := val(args, len(args)-1)
	switch args[0] {
	case "status":
		names := make([]string, 0, len(d.vms))
		for n := range d.vms {
			names = append(names, n)
		}
		sort.Slice(names, func(a, b int) bool { return d.vms[names[a]].id < d.vms[names[b]].id })
		var b strings.Builder
		b.WriteString("   ID   PID VCPUS  MAXMEM  CURMEM     TTY        OWNER    STATE   NAME\n")
		for _, n := range names {
			v := d.vms[n]
			cur := v.mem
			if v.state != "running" && v.state != "paused" {
				cur = "-"
			}
			fmt.Fprintf(&b, "%5s %5s %5d %7s %7s %7s %12s %8s   %s\n", v.id, v.pid, v.vcpus, v.mem, cur, v.tty, "root", v.state, n)
		}
		return ok(b.String())
	case "start":
		conf := d.vmConf()
		c := conf.VM(target)
		if c == nil {
			return fail("vmctl", "vmctl: start vm command failed: No such file or directory\n", 1)
		}
		v := d.vms[target]
		if v != nil && v.state == "running" {
			return fail("vmctl", "vmctl: start vm command failed: Operation already in progress\n", 1)
		}
		mem := c.Memory
		if mem == "" {
			mem = "512M"
		}
		d.vms[target] = &demoVM{id: fmt.Sprint(d.nextID), pid: fmt.Sprint(60000 + d.nextID*37), tty: fmt.Sprintf("ttyp%d", d.nextID), state: "running", vcpus: max(c.CPUs, 1), mem: normMem(mem)}
		d.nextID++
		return ok(fmt.Sprintf("vmctl: started vm %d successfully, tty /dev/%s\n", d.nextID-1, d.vms[target].tty))
	case "stop":
		v := d.vms[target]
		if v == nil {
			return fail("vmctl", "vmctl: terminate vm command failed: Invalid argument\n", 1)
		}
		delete(d.vms, target)
		return ok("stopping vm " + target + ": requested to shutdown vm " + v.id + "\n")
	case "pause", "unpause":
		v := d.vms[target]
		if v == nil {
			return fail("vmctl", "vmctl: no such vm\n", 1)
		}
		if args[0] == "pause" {
			v.state = "paused"
		} else {
			v.state = "running"
		}
		return ok(fmt.Sprintf("%sd vm %s successfully\n", args[0], target))
	case "create":
		if err := d.createDisk(args); err != nil {
			return fail("vmctl", "vmctl: "+err.Error()+"\n", 1)
		}
		return ok("vmctl: qcow2 imagefile created\n")
	case "reload":
		return ok("")
	}
	return fail("vmctl", "vmctl: unknown command", 1)
}

func normMem(m string) string {
	switch {
	case strings.HasSuffix(m, "G"):
		return strings.TrimSuffix(m, "G") + ".0G"
	case strings.HasSuffix(m, "M"):
		return m
	}
	return m + "M"
}

func (d *DemoRunner) vmConf() *VMConfFile {
	b, _ := os.ReadFile(filepath.Join(d.root, "etc/vm.conf"))
	return ParseVMConf(string(b))
}

// check simuliert Syntaxprüfungen: Zeilen mit "SYNTAXFEHLER" gelten als Fehler.
func (d *DemoRunner) check(name string, args []string) (Result, error) {
	file := ""
	for i, a := range args {
		if a == "-f" || a == "-nf" || a == "-C" || a == "-c" {
			file = val(args, i+1)
		}
	}
	if name == "unbound-checkconf" {
		file = val(args, 0)
	}
	b, _ := os.ReadFile(filepath.Join(d.root, file))
	for i, l := range strings.Split(string(b), "\n") {
		if strings.Contains(l, "SYNTAXFEHLER") {
			return fail(name, fmt.Sprintf("%s:%d: syntax error\n", file, i+1), 1)
		}
	}
	if name == "httpd" || name == "relayd" || name == "smtpd" || name == "vmd" {
		return ok("configuration OK\n")
	}
	return ok("")
}

func (d *DemoRunner) pfctl(args []string) (Result, error) {
	a := strings.Join(args, " ")
	switch {
	case a == "-si":
		st := "Disabled"
		if d.pfOn {
			st = "Enabled"
		}
		return ok(fmt.Sprintf(demoPfInfo, st))
	case a == "-sr":
		if b, _ := os.ReadFile(filepath.Join(d.root, "etc/pf.conf")); reAnchorRef.Match(b) {
			return ok(demoPfRules + "anchor \"maar\" all\n")
		}
		return ok(demoPfRules)
	case a == "-ss":
		return ok(demoPfStates)
	case a == "-sT":
		names := make([]string, 0, len(d.tables))
		for n := range d.tables {
			names = append(names, n)
		}
		sort.Strings(names)
		return ok(strings.Join(names, "\n") + "\n")
	case a == "-e":
		if d.pfOn {
			return fail("pfctl", "pfctl: pf already enabled\n", 1)
		}
		d.pfOn = true
		return Result{Stderr: []byte("pf enabled\n")}, nil
	case a == "-d":
		d.pfOn = false
		return Result{Stderr: []byte("pf disabled\n")}, nil
	case strings.HasPrefix(a, "-f") || strings.HasPrefix(a, "-F"):
		return ok("")
	case strings.HasPrefix(a, "-a ") && val(args, 2) == "-f":
		return ok("")
	case strings.HasPrefix(a, "-a ") && val(args, 2) == "-sr":
		b, _ := os.ReadFile(filepath.Join(d.root, "etc/pf.maar.conf"))
		var out []string
		for _, l := range lines(string(b)) {
			if !strings.HasPrefix(strings.TrimSpace(l), "#") {
				out = append(out, strings.TrimSpace(l))
			}
		}
		return ok(strings.Join(out, "\n") + "\n")
	case strings.HasPrefix(a, "-t"):
		t, op, addr := val(args, 1), val(args, 3), val(args, 4)
		switch op {
		case "show":
			var b strings.Builder
			for _, x := range d.tables[t] {
				b.WriteString("   " + x + "\n")
			}
			return ok(b.String())
		case "add":
			d.tables[t] = append(d.tables[t], addr)
			return Result{Stderr: []byte("1/1 addresses added.\n")}, nil
		case "delete":
			out := d.tables[t][:0]
			for _, x := range d.tables[t] {
				if x != addr {
					out = append(out, x)
				}
			}
			d.tables[t] = out
			return Result{Stderr: []byte("1/1 addresses deleted.\n")}, nil
		}
	}
	return fail("pfctl", "pfctl: nicht simuliert", 1)
}

func (d *DemoRunner) rcctl(args []string) (Result, error) {
	all := []string{"iscsid", "accounting", "amd", "apmd", "bgpd", "bootparamd", "check_quotas", "cron", "dhcp6leased", "dhcpd", "dhcpleased", "dhcrelay", "dvmrpd", "eigrpd", "ftpd", "ftpproxy", "hostapd", "hotplugd", "httpd", "identd", "ifstated", "iked", "inetd", "ipsec", "isakmpd", "ldapd", "ldattach", "ldpd", "library_aslr", "lockd", "lpd", "mopd", "mountd", "mrouted", "multicast", "nfsd", "npppd", "ntpd", "ospf6d", "ospfd", "pf", "pflogd", "portmap", "rad", "radiusd", "rarpd", "rbootd", "relayd", "resolvd", "ripd", "route6d", "sasyncd", "sensorsd", "slaacd", "slowcgi", "smtpd", "sndiod", "snmpd", "spamd", "spamd_black", "spamlogd", "sshd", "statd", "switchd", "syslogd", "tftpd", "tftpproxy", "unbound", "unwind", "vmd", "watchdogd", "wsmoused", "xenodm", "ypbind", "ypldap", "yppasswdd", "ypserv"}
	if len(args) < 2 {
		return fail("rcctl", "usage: rcctl ...", 1)
	}
	svc := args[1]
	switch args[0] {
	case "ls":
		var out []string
		for _, s := range all {
			switch svc {
			case "all":
				out = append(out, s)
			case "on":
				if d.on[s] {
					out = append(out, s)
				}
			case "started":
				if d.started[s] {
					out = append(out, s)
				}
			case "failed":
				if d.on[s] && !d.started[s] && !rcSpecial[s] {
					out = append(out, s)
				}
			case "stopped":
				if !d.started[s] {
					out = append(out, s)
				}
			}
		}
		if len(out) == 0 {
			return ok("")
		}
		return ok(strings.Join(out, "\n") + "\n")
	case "get":
		if len(args) == 3 && args[2] == "status" {
			if d.on[svc] {
				return ok("")
			}
			return fail("rcctl", "", 1)
		}
		flags := d.flags[svc]
		if !d.on[svc] {
			flags = "NO"
		}
		if len(args) == 3 && args[2] == "flags" {
			return ok(flags + "\n")
		}
		return ok(fmt.Sprintf("%s_class=daemon\n%s_flags=%s\n%s_logger=\n%s_rtable=0\n%s_timeout=30\n%s_user=root\n", svc, svc, flags, svc, svc, svc, svc))
	case "check":
		if d.started[svc] {
			return ok(svc + "(ok)\n")
		}
		return fail("rcctl", svc+"(failed)\n", 1)
	case "start", "restart":
		if !d.on[svc] && args[0] == "start" {
			return fail("rcctl", "/usr/sbin/rcctl: need -f to force "+svc+" start since "+svc+"_flags=NO\n", 1)
		}
		d.started[svc] = true
		return ok(svc + "(ok)\n")
	case "stop":
		delete(d.started, svc)
		return ok(svc + "(ok)\n")
	case "reload":
		return ok(svc + "(ok)\n")
	case "enable":
		d.on[svc] = true
		return ok("")
	case "disable":
		delete(d.on, svc)
		return ok("")
	case "set":
		if len(args) >= 3 && args[2] == "flags" {
			d.flags[svc] = strings.TrimSpace(strings.Join(args[3:], " "))
			d.on[svc] = true
		}
		return ok("")
	}
	return fail("rcctl", "unbekannt", 1)
}

func (d *DemoRunner) pkgInfo(args []string) (Result, error) {
	a := strings.Join(args, " ")
	names := make([]string, 0, len(d.pkgs))
	for n := range d.pkgs {
		names = append(names, n)
	}
	sort.Strings(names)
	switch {
	case a == "":
		var b strings.Builder
		for _, n := range names {
			fmt.Fprintf(&b, "%-28s %s\n", n, d.pkgs[n])
		}
		return ok(b.String())
	case a == "-mq":
		var b strings.Builder
		for _, n := range names {
			if !strings.HasPrefix(n, "lib") && !strings.HasPrefix(n, "gettext") && !strings.HasPrefix(n, "quirks") {
				b.WriteString(n + "\n")
			}
		}
		return ok(b.String())
	case strings.HasPrefix(a, "-Q "):
		q := strings.ToLower(args[1])
		var b strings.Builder
		for _, c := range strings.Split(demoPkgCatalog, "\n") {
			if c != "" && strings.Contains(strings.ToLower(c), q) {
				inst := ""
				if _, ok := d.pkgs[c]; ok {
					inst = " (installed)"
				}
				b.WriteString(c + inst + "\n")
			}
		}
		return ok(b.String())
	}
	return ok("")
}

func (d *DemoRunner) users(name string, args []string) (Result, error) {
	target := val(args, len(args)-1)
	passwd := filepath.Join(d.root, "etc/passwd")
	group := filepath.Join(d.root, "etc/group")
	switch name {
	case "useradd":
		b, _ := os.ReadFile(passwd)
		if strings.Contains(string(b), "\n"+target+":") {
			return fail("useradd", "useradd: Can't add user `"+target+"': user already exists\n", 1)
		}
		uid := 1000 + strings.Count(string(b), ":/home/")
		comment, shell := "", "/bin/ksh"
		for i, x := range args {
			if x == "-c" {
				comment = val(args, i+1)
			}
			if x == "-s" {
				shell = val(args, i+1)
			}
			if x == "-G" {
				d.addToGroups(group, target, strings.Split(val(args, i+1), ","))
			}
		}
		appendLine(passwd, fmt.Sprintf("%s:*:%d:%d:%s:/home/%s:%s", target, uid, uid, comment, target, shell))
		appendLine(group, fmt.Sprintf("%s:*:%d:", target, uid))
	case "userdel":
		filterLines(passwd, func(l string) bool { return !strings.HasPrefix(l, target+":") })
		filterLines(group, func(l string) bool { return !strings.HasPrefix(l, target+":") })
	case "usermod":
		b, _ := os.ReadFile(passwd)
		var out []string
		for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
			f := strings.Split(l, ":")
			if len(f) == 7 && f[0] == target {
				for i, x := range args {
					switch x {
					case "-c":
						f[4] = val(args, i+1)
					case "-s":
						f[6] = val(args, i+1)
					case "-Z":
						f[6] = strings.TrimSuffix(f[6], "-") + "-"
					case "-U":
						f[6] = strings.TrimSuffix(f[6], "-")
					case "-S":
						d.setGroups(group, target, strings.Split(val(args, i+1), ","))
					}
				}
				l = strings.Join(f, ":")
			}
			out = append(out, l)
		}
		os.WriteFile(passwd, []byte(strings.Join(out, "\n")+"\n"), 0o644)
	case "groupadd":
		appendLine(group, fmt.Sprintf("%s:*:%d:", target, 2000+int(time.Now().Unix()%1000)))
	case "groupdel":
		filterLines(group, func(l string) bool { return !strings.HasPrefix(l, target+":") })
	}
	return ok("")
}

func (d *DemoRunner) addToGroups(group, user string, groups []string) {
	d.editGroups(group, func(name string, members []string) []string {
		for _, g := range groups {
			if g == name {
				return append(members, user)
			}
		}
		return members
	})
}

func (d *DemoRunner) setGroups(group, user string, groups []string) {
	d.editGroups(group, func(name string, members []string) []string {
		var out []string
		for _, m := range members {
			if m != user {
				out = append(out, m)
			}
		}
		for _, g := range groups {
			if g == name {
				out = append(out, user)
			}
		}
		return out
	})
}

func (d *DemoRunner) editGroups(group string, fn func(string, []string) []string) {
	b, _ := os.ReadFile(group)
	var out []string
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		f := strings.Split(l, ":")
		if len(f) == 4 {
			var m []string
			if f[3] != "" {
				m = strings.Split(f[3], ",")
			}
			f[3] = strings.Join(fn(f[0], m), ",")
			l = strings.Join(f, ":")
		}
		out = append(out, l)
	}
	os.WriteFile(group, []byte(strings.Join(out, "\n")+"\n"), 0o644)
}

func appendLine(p, l string) {
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
	if err == nil {
		f.WriteString(l + "\n")
		f.Close()
	}
}

func filterLines(p string, keep func(string) bool) {
	b, _ := os.ReadFile(p)
	var out []string
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if keep(l) {
			out = append(out, l)
		}
	}
	os.WriteFile(p, []byte(strings.Join(out, "\n")+"\n"), 0o644)
}

// createDisk bildet vmctl create nach: -b legt ein Overlay mit Basisverweis
// im qcow2-Header an, -i und -s erzeugen ein eigenständiges Image.
func (d *DemoRunner) createDisk(args []string) error {
	target := filepath.Join(d.root, val(args, len(args)-1))
	if _, err := os.Stat(target); err == nil {
		return fmt.Errorf("disk already exists")
	}
	backing := ""
	for i, a := range args {
		if a == "-b" {
			backing = val(args, i+1)
		}
		if a == "-i" {
			if _, err := os.Stat(filepath.Join(d.root, val(args, i+1))); err != nil {
				return fmt.Errorf("could not open input disk %s", val(args, i+1))
			}
		}
	}
	return writeFakeQcow2(target, backing, 192<<20)
}

// seedDemoBackups legt Ziele, einen nächtlichen Job und ältere Sicherungen an.
func seedDemoBackups(root string) {
	now := time.Now()
	conf, _ := os.ReadFile(filepath.Join(root, "etc/vm.conf"))
	vc := ParseVMConf(string(conf))
	nas := filepath.Join(root, "var/maar/mnt/nas")
	var hist []BackupRun
	for day := 3; day >= 1; day-- {
		at := time.Date(now.Year(), now.Month(), now.Day(), 2, 15, 0, 0, now.Location()).AddDate(0, 0, -day+1)
		if at.After(now) {
			continue
		}
		for _, vm := range []string{"web01", "db01"} {
			id := at.Add(time.Duration(len(vm)) * time.Minute).Format("20060102-150405")
			dir := filepath.Join(nas, vm, id)
			os.MkdirAll(dir, 0o700)
			size := map[string]int64{"web01": 2900 << 20, "db01": 8400 << 20}[vm]
			writeFakeQcow2(filepath.Join(dir, "disk0.qcow2"), "", size)
			v := vc.VM(vm)
			m := BackupManifest{VM: vm, ID: id, Created: at.Unix(), Mode: "snapshot", Host: "nordlicht.ctw.lan", Block: v.Raw,
				Disks: []BackupDisk{{Source: v.Disks[0], File: "disk0.qcow2", Size: size}}}
			b, _ := json.MarshalIndent(m, "", "  ")
			os.WriteFile(filepath.Join(dir, "manifest.json"), b, 0o600)
		}
		hist = append(hist, BackupRun{Job: "naechtlich", Name: "Nächtlich → NAS", Trigger: "Zeitplan", Started: at.Unix(), Ended: at.Add(11 * time.Minute).Unix(), State: "done"})
	}
	last := int64(0)
	if len(hist) > 0 {
		last = hist[len(hist)-1].Started
	}
	c := BackupConfig{
		Targets: []*BackupTarget{
			{ID: "nas", Name: "NAS", Type: "nfs", Path: "nas.ctw.lan:/volume1/vm-backup"},
			{ID: "usb", Name: "USB-Platte", Type: "local", Path: "/backup/vms"},
		},
		Jobs: []*BackupJob{{ID: "naechtlich", Name: "Nächtlich", VMs: []string{"web01", "db01"}, Target: "nas", Mode: "snapshot",
			Schedule: Schedule{Kind: "daily", Time: "02:15"}, Keep: 7, Enabled: true}},
		LastRun: map[string]int64{"naechtlich": last},
		History: hist,
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	os.MkdirAll(filepath.Join(root, "var/maar"), 0o700)
	os.WriteFile(filepath.Join(root, backupConfPath), b, 0o600)
}

// sftp simuliert einen SFTP-Server unter <sandbox>/srv/sftp/<user@host>.
func (d *DemoRunner) sftp(w io.Writer, args []string) error {
	batch, dest := "", val(args, len(args)-1)
	for i, a := range args {
		if a == "-b" {
			batch = val(args, i+1)
		}
	}
	b, err := os.ReadFile(filepath.Join(d.root, batch))
	if err != nil {
		return err
	}
	remote := func(p string) string { return filepath.Join(d.root, "srv/sftp", dest, filepath.Clean("/"+p)) }
	local := func(p string) string { return filepath.Join(d.root, p) }
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		ignore := strings.HasPrefix(line, "-")
		f := strings.Fields(strings.ReplaceAll(strings.TrimPrefix(line, "-"), `"`, ""))
		if len(f) == 0 {
			continue
		}
		fmt.Fprintf(w, "sftp> %s\n", strings.Join(f, " "))
		var err error
		switch f[0] {
		case "mkdir":
			err = os.Mkdir(remote(f[1]), 0o755)
		case "put":
			os.MkdirAll(filepath.Dir(remote(f[2])), 0o755)
			err = copyFile(local(f[1]), remote(f[2]))
		case "get":
			err = copyFile(remote(f[1]), local(f[2]))
		case "rm":
			err = os.Remove(remote(f[1]))
		case "rmdir":
			err = os.Remove(remote(f[1]))
		case "ls":
			m, _ := filepath.Glob(remote(f[len(f)-1]))
			base := filepath.Join(d.root, "srv/sftp", dest)
			for _, x := range m {
				fmt.Fprintln(w, strings.TrimPrefix(x, base))
			}
		}
		if err != nil && !ignore {
			return &CmdError{Cmd: "sftp", Code: 1, Stderr: err.Error()}
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	st, _ := in.Stat()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	// Sparse-Dateien nicht aufblähen: nur Kopf kopieren, Größe übernehmen.
	io.CopyN(out, in, 64<<10)
	return out.Truncate(st.Size())
}

// rebase bildet „qemu-img rebase … -b neu datei“ nach: neuer Basisverweis, Größe bleibt.
func (d *DemoRunner) rebase(args []string) error {
	target := filepath.Join(d.root, val(args, len(args)-1))
	backing := ""
	for i, a := range args {
		if a == "-b" {
			backing = val(args, i+1)
		}
	}
	st, err := os.Stat(target)
	if err != nil {
		return fmt.Errorf("Could not open '%s'", val(args, len(args)-1))
	}
	return writeFakeQcow2(target, backing, st.Size())
}

func writeFakeQcow2(p, backing string, size int64) error {
	hdr := make([]byte, 512+len(backing))
	copy(hdr, "QFI\xfb")
	binary.BigEndian.PutUint32(hdr[4:], 3)
	if size > 0 {
		binary.BigEndian.PutUint64(hdr[24:], uint64(size)) // virtuelle Größe
	}
	if backing != "" {
		binary.BigEndian.PutUint64(hdr[8:], 512)
		binary.BigEndian.PutUint32(hdr[16:], uint32(len(backing)))
		copy(hdr[512:], backing)
	}
	if err := os.WriteFile(p, hdr, 0o644); err != nil {
		return err
	}
	return os.Truncate(p, size)
}

// Stream simuliert lang laufende Befehle zeilenweise.
func (d *DemoRunner) Stream(ctx context.Context, w io.Writer, name string, args ...string) error {
	var script []string
	switch name {
	case "pkg_add":
		var pk []string
		for _, a := range args {
			if !strings.HasPrefix(a, "-") {
				pk = append(pk, a)
			}
		}
		if len(pk) == 0 {
			script = []string{"quirks-7.103 signed on 2026-10-25T19:44:01Z", "Update candidates: quirks-7.103 -> quirks-7.103", "Update candidates: curl-8.16.0 -> curl-8.16.0p0", "curl-8.16.0p0: ok", "Read shared items: ok"}
		} else {
			script = []string{"quirks-7.103 signed on 2026-10-25T19:44:01Z"}
			for _, p := range pk {
				script = append(script, p+": ok")
				d.mu.Lock()
				if _, v := splitPkg(p); v == "" {
					p += "-1.0"
				}
				d.pkgs[p] = "installiert über maar (Demo)"
				d.mu.Unlock()
			}
		}
	case "pkg_delete":
		for _, a := range args {
			if !strings.HasPrefix(a, "-") {
				d.mu.Lock()
				for n := range d.pkgs {
					if n == a || strings.HasPrefix(n, a+"-") {
						delete(d.pkgs, n)
					}
				}
				d.mu.Unlock()
				script = append(script, a+": ok")
			}
		}
		script = append(script, "Read shared items: ok")
	case "syspatch":
		d.mu.Lock()
		for _, p := range d.patches {
			script = append(script, "Get/Verify syspatch80-"+p+".tgz 100% |**************************|  1180 KB    00:01", "Installing patch "+p)
		}
		d.patches = nil
		d.mu.Unlock()
		script = append(script, "Relinking to create unique kernel... done; reboot to load the new kernel")
	case "fw_update":
		script = []string{"fw_update: added none; updated none; kept amdgpu,amd-ucode"}
	case "sysupgrade":
		script = []string{"Fetching from https://cdn.openbsd.org/pub/OpenBSD/8.0/amd64/", "SHA256.sig   100% |*************************|  2144       00:00", "Signature Verified", "INSTALL.amd64 100% |************************| 43554       00:00", "base80.tgz   100% |*************************|   339 MB    00:12", "comp80.tgz   100% |*************************| 82418 KB    00:03", "Verifying sets.", "Fetching updated firmware.", "Will upgrade on next reboot"}
	case "sftp":
		return d.sftp(w, args)
	case qemuImg:
		if err := d.rebase(args); err != nil {
			return &CmdError{Cmd: "qemu-img", Code: 1, Stderr: err.Error()}
		}
		script = []string{"    (100.00/100%)"}
	case "vmctl":
		if err := d.createDisk(args); err != nil {
			return &CmdError{Cmd: "vmctl", Code: 1, Stderr: err.Error()}
		}
		script = []string{"vmctl: converting " + val(args, 2) + " …", "vmctl: qcow2 imagefile created"}
	case "ftp":
		script = []string{"Trying 151.101.2.217...", "Requesting " + val(args, len(args)-1), "100% |**************************************|   712 MB    00:21", "746586112 bytes received in 21.30 seconds (33.42 MB/s)"}
		if len(args) >= 2 && args[0] == "-o" {
			p := filepath.Join(d.root, args[1])
			if f, err := os.Create(p); err == nil {
				f.Truncate(712 << 20)
				f.Close()
			}
		}
	default:
		script = []string{"$ " + name + " " + strings.Join(args, " "), "(Demo) fertig"}
	}
	for _, l := range script {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(450 * time.Millisecond):
		}
		fmt.Fprintln(w, l)
	}
	return nil
}

// ifconfig liefert die statische Demo-Ausgabe, ergänzt um per netstart
// erzeugte und ohne die zerstörten Interfaces.
func (d *DemoRunner) ifconfig() string {
	var blocks []string
	var names []string
	cur := ""
	for _, l := range strings.Split(strings.TrimRight(demoIfconfig, "\n"), "\n") {
		if l != "" && l[0] != '\t' {
			if cur != "" {
				blocks = append(blocks, cur)
			}
			cur = ""
			names = append(names, l[:strings.Index(l, ":")])
		}
		cur += l + "\n"
	}
	blocks = append(blocks, cur)
	var out strings.Builder
	seen := map[string]bool{}
	for i, b := range blocks {
		seen[names[i]] = true
		if o, ok := d.ifs[names[i]]; ok {
			b = o
		}
		out.WriteString(b)
	}
	extra := make([]string, 0, len(d.ifs))
	for n := range d.ifs {
		if !seen[n] {
			extra = append(extra, n)
		}
	}
	sort.Strings(extra)
	for _, n := range extra {
		out.WriteString(d.ifs[n])
	}
	return out.String()
}

// synthIf erzeugt einen ifconfig-Block aus /etc/hostname.<name> der Sandbox.
func (d *DemoRunner) synthIf(name string) string {
	src, err := os.ReadFile(filepath.Join(d.root, "etc/hostname."+name))
	if err != nil {
		return ""
	}
	kind := strings.TrimRight(name, "0123456789")
	flags := "8843<UP,BROADCAST,RUNNING,SIMPLEX,MULTICAST"
	var body strings.Builder
	var members []string
	for _, l := range lines(string(src)) {
		f := strings.Fields(l)
		switch f[0] {
		case "description":
			fmt.Fprintf(&body, "\tdescription: %s\n", strings.Trim(strings.TrimPrefix(strings.TrimSpace(l), "description"), " \""))
		case "inet":
			if len(f) >= 3 {
				p := netip.MustParsePrefix(inetToCIDR(f[1], "", f[2]))
				m := ^uint32(0) << (32 - p.Bits())
				fmt.Fprintf(&body, "\tinet %s netmask 0x%08x broadcast %s\n", f[1], m, broadcast(p.Masked()))
			}
		case "parent":
			fmt.Fprintf(&body, "\tencap: vnetid %s parent %s txprio packet rxprio outer\n", val(f, 3), val(f, 1))
		case "add":
			members = append(members, val(f, 1))
		case "link1":
			flags += ",LINK1"
		}
	}
	if kind == "veb" {
		// laufende VMs an diesem Switch hängen mit ihren tap-Interfaces daran
		conf := d.vmConf()
		for _, sw := range conf.Switches {
			if sw.Interface != name {
				continue
			}
			for _, v := range conf.VMs {
				for _, ifc := range v.Interfaces {
					if dv := d.vms[v.Name]; ifc.Switch == sw.Name && dv != nil && dv.state == "running" {
						n, _ := strconv.Atoi(dv.id)
						members = append(members, fmt.Sprintf("tap%d", n-1))
					}
				}
			}
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s: flags=%s> mtu 1500\n", name, flags)
	if kind != "veb" {
		sum := 0
		for _, c := range name {
			sum = sum*31 + int(c)
		}
		fmt.Fprintf(&b, "\tlladdr fe:e1:ba:d1:%02x:%02x\n", sum>>8&255, sum&255)
	}
	b.WriteString(body.String())
	fmt.Fprintf(&b, "\tindex %d priority 0 llprio 3\n\tgroups: %s\n", 20+len(d.ifs), kind)
	if kind == "em" {
		b.WriteString("\tmedia: Ethernet autoselect (none)\n\tstatus: no carrier\n")
	}
	for i, m := range members {
		fmt.Fprintf(&b, "\t%s flags=3<LEARNING,DISCOVER>\n\t\tport %d ifpriority 0 ifcost 0\n", m, 30+i)
	}
	return b.String()
}

// ---- SAN-Simulation ----

func (d *DemoRunner) sanDisk(n string) bool {
	return n == "sd2" || n == "sd3" || n == "sd4" || n == "sd5"
}

func (d *DemoRunner) disknames() string {
	out := "sd0:2a7c1f9e0b3d4c55,sd1:9e41c07a2bd31f80"
	for _, n := range []string{"sd2", "sd3", "sd4", "sd5"} {
		duid := d.sanDuid[n]
		if duid == "" {
			duid = "0000000000000000"
		}
		out += "," + n + ":" + duid
	}
	return out + ",cd0:"
}

func (d *DemoRunner) disklabel(args []string, stdin []byte) (Result, error) {
	dev := val(args, len(args)-1)
	if len(args) > 0 && args[0] == "-E" && d.sanDisk(dev) {
		if !strings.Contains(string(stdin), "w") {
			return fail("disklabel", "disklabel: label nicht geschrieben\n", 1)
		}
		d.sanSeq++
		d.sanDuid[dev] = fmt.Sprintf("%016x", uint64(0x5a17c0de00000000)+uint64(d.sanSeq)*0x1111+uint64(dev[2]))
		return ok("")
	}
	if d.sanDisk(dev) {
		duid := d.sanDuid[dev]
		if duid == "" {
			duid = "0000000000000000"
		}
		return ok(fmt.Sprintf("# /dev/r%sc:\ntype: SCSI\ndisk: SCSI disk\nlabel: SAN LUN\nduid: %s\nflags:\nbytes/sector: 512\n", dev, duid))
	}
	return ok(strings.ReplaceAll(demoDisklabel, "sd0", dev))
}

// sanMount hängt einen fstab-Eintrag ein (mount /var/vmd/<name>).
func (d *DemoRunner) sanMount(mp string) (Result, error) {
	b, _ := os.ReadFile(filepath.Join(d.root, "etc/fstab"))
	for _, l := range lines(string(b)) {
		f := strings.Fields(l)
		if len(f) >= 2 && f[1] == mp && !strings.HasPrefix(l, "#") {
			duid := strings.SplitN(f[0], ".", 2)
			for disk, du := range d.sanDuid {
				if du == duid[0] {
					d.sanMnt["/dev/"+disk+duid[len(duid)-1]] = mp
					return ok("")
				}
			}
			return fail("mount", "mount: "+f[0]+": No such device\n", 1)
		}
	}
	return ok("") // NFS u. a.: Daten liegen in der Sandbox
}

func (d *DemoRunner) sanMountLines() string {
	var b strings.Builder
	for dev, mp := range d.sanMnt {
		fmt.Fprintf(&b, "%s on %s type ffs (local, nodev, nosuid, noatime)\n", dev, mp)
	}
	return b.String()
}

func (d *DemoRunner) sanDf() string {
	var b strings.Builder
	for dev, mp := range d.sanMnt {
		fmt.Fprintf(&b, "%s  524288000  98765432  425522568   19%%    %s\n", dev, mp)
	}
	return b.String()
}

func (d *DemoRunner) iscsiSummary() string {
	b, _ := os.ReadFile(filepath.Join(d.root, "etc/iscsi.conf"))
	var out strings.Builder
	for _, m := range reTargetSc.FindAllStringSubmatch(string(b), -1) {
		fmt.Fprintf(&out, "%-20s 10.0.0.50:3260  Session: Logged in (Full Feature Phase)\n", strings.Trim(m[1], `"`))
	}
	return out.String()
}
