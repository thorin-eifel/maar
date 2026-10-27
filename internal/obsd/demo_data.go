package obsd

// Beispielausgaben eines OpenBSD-8.0-Hosts für den Demo-Modus.

const demoTop = `load averages:  0.42,  0.37,  0.31    nordlicht.ctw.lan 09:41:07
71 processes: 70 idle, 1 on processor  %s
8  CPUs:  6.1%% user,  0.0%% nice,  3.2%% sys,  0.4%% spin,  0.3%% intr, 90.0%% idle
Memory: Real: 3412M/9874M act/tot Free: 22G Cache: 5120M Swap: 0K/4103M

  PID USERNAME PRI NICE  SIZE   RES STATE     WAIT      TIME    CPU COMMAND
48211 _vmd       2    0 1047M 1012M sleep/2   kqread   41:12  4.98%% vmd
`

const demoPs = `  PID USER     %CPU %MEM   RSS STAT STARTED     TIME COMMAND
    1 root      0.0  0.0   440 I    16Oct26   0:00.03 /sbin/init
48211 _vmd      5.0  3.0 1036288 S 16Oct26  41:12.40 vmd: web01 (vmd)
51907 _vmd      2.1  6.1 2072576 S 16Oct26  19:03.11 vmd: db01 (vmd)
 3122 root      0.0  0.0  1104 I    16Oct26   0:01.12 /usr/sbin/vmd
 4410 _syslogd  0.0  0.0  1236 S    16Oct26   0:04.93 syslogd: [priv] (syslogd)
 7731 _pflogd   0.0  0.0   600 S    16Oct26   0:02.30 pflogd: [running] -s 160 -i pflog0 -f /var/log/pflog (pflogd)
 8124 _ntp      0.0  0.0  1340 I<   16Oct26   0:03.77 ntpd: ntp engine (ntpd)
 9001 root      0.0  0.0  1560 I    16Oct26   0:00.21 sshd: /usr/sbin/sshd [listener] 0 of 10-100 startups (sshd)
 9310 www       0.1  0.0  2368 S    16Oct26   0:31.02 httpd: server (httpd)
 9311 www       0.0  0.0  2244 S    16Oct26   0:29.80 httpd: server (httpd)
 9500 _unbound  0.3  0.1 23700 S    16Oct26   2:40.66 /usr/sbin/unbound -c /var/unbound/etc/unbound.conf
 9620 _smtpd    0.0  0.0  2912 I    16Oct26   0:01.08 smtpd: pony express (smtpd)
10002 _dhcp     0.0  0.0   964 I    16Oct26   0:00.40 dhcpd vport0
11203 root      0.0  0.0   860 I    16Oct26   0:00.88 cron: running jobs queue (cron)
22814 root      0.2  0.1 14320 S     9:12AM   0:01.73 maar -listen :9443
22990 thorin    0.0  0.0  2480 Ss    9:31AM   0:00.05 -ksh (ksh)
`

const demoDf = `Filesystem  1024-blocks       Used      Avail Capacity  Mounted on
/dev/sd0a       1005526     235118     720132    25%    /
/dev/sd0k     103145236   12008112   85979864    12%    /home
/dev/sd0d       4119998         38    3913962     0%    /tmp
/dev/sd0f      12371018    2187364    9565104    19%    /usr
/dev/sd0g       1005430     348852     606308    37%    /usr/X11R6
/dev/sd0h      20622862    4411122   15180598    23%    /usr/local
/dev/sd0j       8241486    1288002    6541412    16%    /usr/obj
/dev/sd0e      30905438    1422098   27937068     5%    /var
/dev/sd1a     484096880  331208808  128683228    72%    /var/vmd
`

const demoMount = `/dev/sd0a on / type ffs (local)
/dev/sd0k on /home type ffs (local, nodev, nosuid)
/dev/sd0d on /tmp type ffs (local, nodev, nosuid)
/dev/sd0f on /usr type ffs (local, nodev, wxallowed)
/dev/sd0g on /usr/X11R6 type ffs (local, nodev)
/dev/sd0h on /usr/local type ffs (local, nodev, wxallowed)
/dev/sd0j on /usr/obj type ffs (local, nodev, nosuid)
/dev/sd0e on /var type ffs (local, nodev, nosuid)
/dev/sd1a on /var/vmd type ffs (local, nodev, nosuid, softdep)
`

const demoDisklabel = `# /dev/rsd0c:
type: SCSI
disk: SCSI disk
label: Samsung SSD 990
duid: 2a7c1f9e0b3d4c55
flags:
bytes/sector: 512
sectors/track: 63
tracks/cylinder: 255
sectors/cylinder: 16065
cylinders: 124519
total sectors: 2000409264 # total bytes: 953.9G
boundstart: 64
boundend: 2000397735

16 partitions:
#                size           offset  fstype [fsize bsize   cpg]
  a:             1.0G               64  4.2BSD   2048 16384 12960 # /
  b:             4.0G          2097216    swap                    # none
  c:           953.9G                0  unused
  d:             4.0G         10474368  4.2BSD   2048 16384 12960 # /tmp
  e:            30.0G         18862912  4.2BSD   2048 16384 12960 # /var
  f:            12.0G         81777472  4.2BSD   2048 16384 12960 # /usr
  g:             1.0G        106943264  4.2BSD   2048 16384 12960 # /usr/X11R6
  h:            20.0G        109040448  4.2BSD   2048 16384 12960 # /usr/local
  j:             8.0G        150983488  4.2BSD   2048 16384 12960 # /usr/obj
  k:           100.0G        167760704  4.2BSD   4096 32768 26062 # /home
`

const demoIfconfig = `lo0: flags=2008049<UP,LOOPBACK,RUNNING,MULTICAST,LRO> mtu 32768
	index 5 priority 0 llprio 3
	groups: lo
	inet6 ::1 prefixlen 128
	inet6 fe80::1%lo0 prefixlen 64 scopeid 0x5
	inet 127.0.0.1 netmask 0xff000000
em0: flags=a48843<UP,BROADCAST,RUNNING,SIMPLEX,MULTICAST,INET6_NOPRIVACY,AUTOCONF6TEMP,AUTOCONF6> mtu 1500
	lladdr 3c:ec:ef:4a:91:20
	description: Uplink
	index 1 priority 0 llprio 3
	groups: egress
	media: Ethernet autoselect (1000baseT full-duplex)
	status: active
	inet 192.168.50.10 netmask 0xffffff00 broadcast 192.168.50.255
	inet6 fe80::3eec:efff:fe4a:9120%em0 prefixlen 64 scopeid 0x1
	inet6 2a02:8108:1a40:3e00::10 prefixlen 64 autoconf pltime 604407 vltime 2591607
em1: flags=8802<BROADCAST,SIMPLEX,MULTICAST> mtu 1500
	lladdr 3c:ec:ef:4a:91:21
	index 2 priority 0 llprio 3
	media: Ethernet autoselect (none)
	status: no carrier
veb0: flags=8843<UP,BROADCAST,RUNNING,SIMPLEX,MULTICAST> mtu 1500
	description: VM-Switch intern
	index 6 llprio 7
	groups: veb
	vport0 flags=3<LEARNING,DISCOVER>
		port 7 ifpriority 0 ifcost 0
	tap0 flags=3<LEARNING,DISCOVER>
		port 8 ifpriority 0 ifcost 0
	tap1 flags=3<LEARNING,DISCOVER>
		port 9 ifpriority 0 ifcost 0
vport0: flags=8943<UP,BROADCAST,RUNNING,PROMISC,SIMPLEX,MULTICAST> mtu 1500
	lladdr fe:e1:ba:d0:4c:6a
	description: VM-Gateway
	index 7 priority 0 llprio 3
	groups: vport
	inet 10.0.0.1 netmask 0xffffff00 broadcast 10.0.0.255
tap0: flags=8943<UP,BROADCAST,RUNNING,PROMISC,SIMPLEX,MULTICAST> mtu 1500
	lladdr fe:e1:ba:d0:12:01
	description: vm1-if0-web01
	index 8 priority 0 llprio 3
	groups: tap
	status: active
tap1: flags=8943<UP,BROADCAST,RUNNING,PROMISC,SIMPLEX,MULTICAST> mtu 1500
	lladdr fe:e1:ba:d0:12:02
	description: vm2-if0-db01
	index 9 priority 0 llprio 3
	groups: tap
	status: active
pflog0: flags=141<UP,RUNNING,PROMISC> mtu 33136
	index 10 priority 0 llprio 3
	groups: pflog
enc0: flags=0<>
	index 3 priority 0 llprio 3
	groups: enc
	status: active
`

const demoRoutes = `Routing tables

Internet:
Destination        Gateway            Flags   Refs      Use   Mtu  Prio Iface
default            192.168.50.1       UGS        8   912334     -     8 em0
10.0.0/24          10.0.0.1           UCn        2       77     -     4 vport0
10.0.0.1           fe:e1:ba:d0:4c:6a  UHLl       0      120     -     1 vport0
127/8              127.0.0.1          UGRS       0        0 32768     8 lo0
127.0.0.1          127.0.0.1          UHhl       1      102 32768     1 lo0
192.168.50/24      192.168.50.10      UCn        3     1840     -     4 em0
192.168.50.1       a0:36:9f:2d:01:aa  UHLch      2    40391     -     3 em0
192.168.50.10      3c:ec:ef:4a:91:20  UHLl       0    20021     -     1 em0
`

const demoRoutes6 = `Routing tables

Internet6:
Destination                        Gateway                        Flags   Refs      Use   Mtu  Prio Iface
default                            fe80::a236:9fff:fe2d:1aa%em0   UGS        1     3201     -    56 em0
::1                                ::1                            UHhl      10       42 32768     1 lo0
2a02:8108:1a40:3e00::/64           2a02:8108:1a40:3e00::10        UCn        1      220     -     4 em0
fe80::%em0/64                      fe80::3eec:efff:fe4a:9120%em0  UCn        1       11     -     4 em0
`

const demoPfInfo = `Status: %s for 11 days 04:17:02           Debug: err

State Table                          Total             Rate
  current entries                       42
  half-open tcp                          0
  searches                       128773109          132.7/s
  inserts                          1093271            1.1/s
  removals                         1093229            1.1/s
Counters
  match                            1127310            1.2/s
  bad-offset                             0            0.0/s
  fragment                               3            0.0/s
  short                                  0            0.0/s
  normalize                              0            0.0/s
  memory                                 0            0.0/s
  bad-timestamp                          0            0.0/s
  congestion                             0            0.0/s
  ip-option                              0            0.0/s
  proto-cksum                            0            0.0/s
  state-mismatch                        12            0.0/s
  state-insert                           0            0.0/s
  state-limit                            0            0.0/s
  src-limit                             17            0.0/s
  synproxy                               0            0.0/s
  translate                              0            0.0/s
  no-route                               0            0.0/s
`

const demoPfRules = `match in all scrub (no-df random-id max-mss 1440)
match out on egress inet from 10.0.0.0/24 to any nat-to (em0:0) round-robin
block return log all
block drop in quick from <bruteforce> to any
pass out all flags S/SA
pass in on egress inet proto tcp from any to (egress:0) port = 22 flags S/SA keep state (source-track rule, max-src-conn 10, max-src-conn-rate 5/30, overload <bruteforce> flush global, src.track 30)
pass in on egress inet proto tcp from <lan> to (egress:0) port = 9443 flags S/SA
pass in on egress inet proto tcp from any to (egress:0) port { 80 443 } flags S/SA
pass in on vport0 from 10.0.0.0/24 to any flags S/SA
pass in on vport0 inet proto udp from any port = 68 to any port = 67
`

const demoPfStates = `all tcp 192.168.50.10:22 <- 192.168.50.23:53122       ESTABLISHED:ESTABLISHED
all tcp 192.168.50.10:9443 <- 192.168.50.23:55018       ESTABLISHED:ESTABLISHED
all tcp 10.0.0.11:51342 (192.168.50.10:63022) -> 151.101.2.217:443       FIN_WAIT_2:FIN_WAIT_2
all udp 192.168.50.10:123 -> 162.159.200.1:123       MULTIPLE:SINGLE
all tcp 192.168.50.10:443 <- 77.180.12.44:40122       ESTABLISHED:ESTABLISHED
all udp 10.0.0.12:4122 (192.168.50.10:55100) -> 9.9.9.9:53       MULTIPLE:SINGLE
all tcp 192.168.50.10:80 <- 66.249.66.12:44890       TIME_WAIT:TIME_WAIT
`

const demoPkgInfo = `bash-5.2.37p0 GNU Bourne Again Shell
curl-8.16.0 transfer files with FTP, HTTP, HTTPS, etc.
git-2.51.0 distributed version control system
gettext-runtime-0.23.1 GNU gettext runtime libraries and programs
htop-3.4.1 interactive process viewer
libiconv-1.18p0 character set conversion library
nghttp2-1.66.0 library for HTTP/2
quirks-7.103 exceptions to pkg_add rules and cache
rsync-3.4.1 mirroring/synchronization over low bandwidth links
tmux-3.5ap0 terminal multiplexer
vim-9.1.1554-no_x11 vi clone, many additional features
zsh-5.9p2 Z shell, Bourne shell-compatible
`

const demoPkgCatalog = `bash-5.2.37p0
curl-8.16.0
git-2.51.0
go-1.25.2
htop-3.4.1
nginx-1.28.0
node-22.19.0
postgresql-server-17.6
postgresql-client-17.6
python-3.13.7
py3-pip-25.2
redis-8.2.1
rsync-3.4.1
ruby-3.4.6
tmux-3.5ap0
vim-9.1.1554-no_x11
vim-9.1.1554-gtk3
wireguard-tools-1.0.20250521
zsh-5.9p2
mariadb-server-11.4.8
mariadb-client-11.4.8
neovim-0.11.4
qemu-10.1.0
`

const demoCrontab = `#
SHELL=/bin/sh
PATH=/bin:/sbin:/usr/bin:/usr/sbin
HOME=/var/log
#
#minute	hour	mday	month	wday	[flags] command
#
# rotate log files every hour, if necessary
0	*	*	*	*	/usr/bin/newsyslog
# send log file notifications, if necessary
#1-59	*	*	*	*	/usr/bin/newsyslog -m
#
# do daily/weekly/monthly maintenance
30	1	*	*	*	/bin/sh /etc/daily
30	3	*	*	6	/bin/sh /etc/weekly
30	5	1	*	*	/bin/sh /etc/monthly
~	*	*	*	*	-ns /usr/libexec/spamd-setup
#
# Sicherung der VMs
15	2	*	*	*	/usr/local/sbin/vm-backup.sh
`

const demoSysctlExtra = `kern.maxproc=1310
kern.maxfiles=7030
kern.securelevel=1
kern.somaxconn=128
kern.pool_debug=0
net.inet.ip.forwarding=1
net.inet6.ip6.forwarding=0
net.inet.tcp.mssdflt=512
machdep.allowaperture=0
vm.swapencrypt.enable=1
hw.perfpolicy=auto
hw.smt=0
`

const demoDmesg = `OpenBSD 8.0 (GENERIC.MP) #54: Sun Oct 12 21:13:46 MDT 2026
nvme0 at pci3 dev 0 function 0 "Samsung PM9A1" rev 0x00: msix, NVMe 1.4
scsibus1 at nvme0: 2 targets, initiator 0
sd0 at scsibus1 targ 1 lun 0: <NVMe, Samsung SSD 990 PRO, 4B2Q>
sd0: 953869MB, 512 bytes/sector, 1953525168 sectors
sd1 at scsibus1 targ 2 lun 0: <NVMe, Samsung SSD 990 PRO, 4B2Q>
sd1: 953869MB, 512 bytes/sector, 1953525168 sectors
qla0 at pci5 dev 0 function 0 "QLogic ISP2532" rev 0x02: msix, firmware 8.7.4
scsibus2 at qla0: 2048 targets, initiator WWPN 21000024ff4c6a52, target 0
qla1 at pci5 dev 0 function 1 "QLogic ISP2532" rev 0x02: msix, firmware 8.7.4
scsibus3 at qla1: 2048 targets, initiator WWPN 21000024ff4c6a53, target 0
sd4 at scsibus2 targ 1 lun 0: <NETAPP, LUN C-Mode, 9500> naa.600a098038314c4c6b4a5a4d6e575a31
sd4: 2097152MB, 512 bytes/sector, 4294967296 sectors
sd5 at scsibus3 targ 1 lun 1: <NETAPP, LUN C-Mode, 9500> naa.600a098038314c4c6b4a5a4d6e575a32
sd5: 1048576MB, 512 bytes/sector, 2147483648 sectors
iscsi0 at root
scsibus4 at iscsi0: 256 targets
sd2 at scsibus4 targ 1 lun 0: <LIO-ORG, vm-pool, 4.0> naa.60014050f3a1c25e8f0b4d4a9e1c7b02
sd2: 512000MB, 512 bytes/sector, 1048576000 sectors
sd3 at scsibus4 targ 2 lun 0: <LIO-ORG, scratch, 4.0> naa.60014057b2d9a0c1e33f4e2b8a6d5f10
sd3: 204800MB, 512 bytes/sector, 419430400 sectors
`
