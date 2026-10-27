package console

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// DemoSerial simuliert die serielle Konsole einer OpenBSD-VM: Bootmeldungen,
// Login und eine minimale Shell. Nur für den Demo-Modus.
type DemoSerial struct {
	name   string
	r      *io.PipeReader
	w      *io.PipeWriter
	mu     sync.Mutex
	line   []byte
	state  int // 0 login, 1 passwort, 2 shell
	user   string
	closed chan struct{}
}

func NewDemoSerial(name string) *DemoSerial {
	r, w := io.Pipe()
	d := &DemoSerial{name: name, r: r, w: w, closed: make(chan struct{})}
	go d.boot()
	return d
}

func (d *DemoSerial) emit(s string) {
	d.w.Write([]byte(strings.ReplaceAll(s, "\n", "\r\n")))
}

func (d *DemoSerial) boot() {
	msgs := []string{
		"Connected to /dev/ttyp0 (speed 115200)\n",
		">> OpenBSD/amd64 BOOT 3.67\nboot> \nbooting hd0a:/bsd: 18431220+4207632+356512+0+1196032 [1450633+128+1234560+1018745]=0x1a2b3c8\n",
		"entry point at 0xffffffff81001000\n[ using 3705032 bytes of bsd ELF symbol table ]\n",
		"Copyright (c) 1982, 1986, 1989, 1991, 1993\n\tThe Regents of the University of California.  All rights reserved.\nCopyright (c) 1995-2026 OpenBSD. All rights reserved.  https://www.OpenBSD.org\n\n",
		"OpenBSD 8.0 (GENERIC) #54: Sun Oct 12 21:13:46 MDT 2026\n    deraadt@amd64.openbsd.org:/usr/src/sys/arch/amd64/compile/GENERIC\n",
		"real mem = 1056952320 (1007MB)\navail mem = 1005379584 (958MB)\n",
		"pvbus0 at mainbus0: OpenBSD\nvirtio0 at pci0 dev 2 function 0 \"Qumranet Virtio Network\" rev 0x00\nvio0 at virtio0: address fe:e1:ba:d0:12:01\n",
		"virtio1 at pci0 dev 3 function 0 \"Qumranet Virtio Storage\" rev 0x00\nvioblk0 at virtio1\nscsibus1 at vioblk0: 1 targets\nsd0 at scsibus1 targ 0 lun 0: <VirtIO, Block Device, >\n",
		"vmmci0 at virtio3: \"VMM Control Interface\" rev 0x00\nroot on sd0a (8c2f61b0a9e7d413.a) swap on sd0b dump on sd0b\n",
		"Automatic boot in progress: starting file system checks.\n/dev/sd0a (8c2f61b0a9e7d413.a): file system is clean; not checking\n",
		"pf enabled\nstarting network\nvio0: 10.0.0.11 lease accepted from 10.0.0.1 (fe:e1:ba:d0:4c:6a)\n",
		"reordering: ld.so libc libcrypto sshd.\nstarting early daemons: syslogd pflogd ntpd.\nstarting RPC daemons:.\nsavecore: no core dump\n",
		"starting network daemons: sshd smtpd sndiod.\nstarting local daemons: cron.\n",
		fmt.Sprintf("%s\n\nOpenBSD/amd64 (%s.vm.ctw.lan) (tty00)\n\nlogin: ", time.Now().Format("Mon Jan  2 15:04:05 MST 2006"), d.name),
	}
	for _, m := range msgs {
		select {
		case <-d.closed:
			return
		case <-time.After(220 * time.Millisecond):
		}
		d.emit(m)
	}
}

func (d *DemoSerial) Read(p []byte) (int, error) { return d.r.Read(p) }

func (d *DemoSerial) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, c := range p {
		switch {
		case c == '\r' || c == '\n':
			d.emit("\n")
			d.enter(string(d.line))
			d.line = d.line[:0]
		case c == 0x7f || c == 0x08:
			if len(d.line) > 0 {
				d.line = d.line[:len(d.line)-1]
				if d.state != 1 {
					d.emit("\b \b")
				}
			}
		case c == 0x03:
			d.line = d.line[:0]
			d.emit("^C\n" + d.prompt())
		case c >= 0x20:
			d.line = append(d.line, c)
			if d.state != 1 {
				d.w.Write([]byte{c})
			}
		}
	}
	return len(p), nil
}

func (d *DemoSerial) prompt() string {
	switch d.state {
	case 0:
		return "login: "
	case 1:
		return "Password:"
	}
	if d.user == "root" {
		return d.name + "# "
	}
	return d.name + "$ "
}

func (d *DemoSerial) enter(l string) {
	l = strings.TrimSpace(l)
	switch d.state {
	case 0:
		if l != "" {
			d.user, d.state = l, 1
		}
	case 1:
		d.state = 2
		d.emit(fmt.Sprintf("Last login: %s on tty00\nOpenBSD 8.0 (GENERIC) #54: Sun Oct 12 21:13:46 MDT 2026\n\nWelcome to OpenBSD: The proactively secure Unix-like operating system.\n\n", time.Now().Add(-26*time.Hour).Format("Mon Jan  2 15:04:05")))
	case 2:
		d.shell(l)
	}
	d.emit(d.prompt())
}

func (d *DemoSerial) shell(l string) {
	f := strings.Fields(l)
	if len(f) == 0 {
		return
	}
	switch f[0] {
	case "exit", "logout":
		d.state = 0
		d.emit(fmt.Sprintf("\nOpenBSD/amd64 (%s.vm.ctw.lan) (tty00)\n\n", d.name))
	case "uname":
		d.emit(fmt.Sprintf("OpenBSD %s.vm.ctw.lan 8.0 GENERIC#54 amd64\n", d.name))
	case "uptime":
		d.emit(time.Now().Format("3:04PM") + "  up 11 days,  4:17, 1 user, load averages: 0.08, 0.04, 0.01\n")
	case "hostname":
		d.emit(d.name + ".vm.ctw.lan\n")
	case "whoami":
		d.emit(d.user + "\n")
	case "ls":
		d.emit(".profile  .ssh  backups  www\n")
	case "date":
		d.emit(time.Now().Format("Mon Jan  2 15:04:05 MST 2006") + "\n")
	case "ifconfig":
		d.emit("vio0: flags=808843<UP,BROADCAST,RUNNING,SIMPLEX,MULTICAST,AUTOCONF4> mtu 1500\n\tlladdr fe:e1:ba:d0:12:01\n\tinet 10.0.0.11 netmask 0xffffff00 broadcast 10.0.0.255\n")
	case "help":
		d.emit("Demo-Konsole: uname, uptime, hostname, whoami, ls, date, ifconfig, exit\n")
	default:
		d.emit("ksh: " + f[0] + ": not found\n")
	}
}

func (d *DemoSerial) Close() error {
	select {
	case <-d.closed:
	default:
		close(d.closed)
	}
	return d.w.Close()
}
