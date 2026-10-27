# 10 · Referenz

[← Betrieb und Fehlersuche](09-betrieb.md) · [Dokumentation](README.md)

## Startoptionen

Gesetzt mit `rcctl set maard flags …` (dann `rcctl restart maard`) oder direkt beim Aufruf von `maard`.

| Flag | Standard | Bedeutung |
|---|---|---|
| `-listen` | `:9443` | Adresse und Port |
| `-cert`, `-key` | `/etc/ssl/maar.crt`, `/etc/ssl/private/maar.key` | TLS; fehlende Dateien werden selbst signiert erzeugt |
| `-plain-http` | aus | ohne TLS, nur hinter `relayd` oder `httpd` auf `127.0.0.1` sinnvoll |
| `-group` | `wheel` | Gruppe mit Anmelderecht |
| `-vmdir` | `/var/vmd` | Disk-Images; ISOs in `<vmdir>/iso`, Templates in `<vmdir>/templates` |
| `-no-pledge` | aus | `pledge(2)` abschalten (zur Fehlersuche) |
| `-demo` | aus | simulierter Host (Anmeldung `demo`/`demo`), lauscht nur auf localhost |
| `-webdir` | – | Oberfläche aus einem Verzeichnis statt der eingebetteten (Entwicklung) |
| `-version` | – | Version ausgeben |

## pf: Zugang zur Oberfläche beschränken

```
table <admin> const { 192.168.50.0/24 }
pass in on egress proto tcp from <admin> to (egress) port 9443
```

Als Formular siehe [Firewall absichern: ein Beispiel](04-netzwerk-und-firewall.md#firewall-absichern-ein-beispiel).

## Dateien und Pfade

| Pfad | Zweck |
|---|---|
| `/usr/local/sbin/maard` | das Programm (ein statisches Binary, Oberfläche eingebettet) |
| `/etc/rc.d/maard` | rc.d-Skript |
| `/var/maar/` | Daten von maar (siehe [Betrieb](09-betrieb.md#was-maar-speichert)) |
| `/var/maar/backups/` | Sicherungen geänderter Konfigurationsdateien |
| `/var/vmd/` | VM-Disks (`<datastore>` als Unterverzeichnis); `iso/`, `templates/`, `snapshots/`, `seed/` |
| `/etc/ssl/maar.crt`, `/etc/ssl/private/maar.key` | TLS-Zertifikat und -Schlüssel |
| `/etc/vm.conf` | VM-Definitionen und Switches (von maar verwaltet, Ergänzungen bleiben) |
| `/etc/pf.maar.conf` | Anker `maar` (NAT, DHCP/DNS, Regeln je VM) |
| `/etc/dhcpd.conf`, `/var/unbound/etc/maar.conf` | DHCP-Bereich und DNS-Zone der VM-Netze (Block `# >>> maar`) |
| `/etc/iscsi.conf` | iSCSI-Ziele (Block `# >>> maar`) |
| `/etc/fstab` | Datastores (Zeilen `# maar datastore <name>`, `noauto`) |

## Menü und Befehle im Vergleich

Wofür maar welches OpenBSD-Werkzeug aufruft – nützlich, wenn du etwas nachvollziehen oder von Hand tun willst.

| Menü | Seite | Werkzeuge |
|---|---|---|
| Übersicht | Übersicht | `sysctl`, `top -b`, `df`, `ifconfig` |
| Inventar | Virtuelle Maschinen | `vmctl`, `/etc/vm.conf`, `vmd -n` |
| Inventar | Templates | `vmctl create -b/-i`, qcow2-Header |
| Inventar | VM → Snapshots | `vmctl create -b`, `qemu-img rebase` (optional) |
| Inventar | VM → Konsole | TTY der VM (`vmctl console`) |
| Speicher | Geteilter Speicher | `iscsid`, `iscsictl`, `dmesg`, `fdisk`, `disklabel`, `newfs`, `mount` |
| Speicher | Lokale Datenträger | `df`, `disklabel`, `sysctl hw.disknames` |
| Speicher | Backup | `vmctl create -i`, `mount -t nfs`, `sftp -b` |
| Netzwerk | VM-Netze | `veb(4)`, `vlan(4)`, `vport(4)`, `dhcpd`, `unbound`, pf-Anker `maar` |
| Netzwerk | Adapter & Routen | `ifconfig`, `netstat -rn`, `/etc/netstart`, `hostname.if` |
| Netzwerk | Paketfilter | `pfctl` |
| Überwachung | Leistung & Alarme | `sysctl kern.cp_time`, `vmstat -s`, `netstat -ibn`, `ps`, `mail(1)` |
| Überwachung | Ereignisse, Protokolle | `/var/maar/events.jsonl`, `/var/log/*` |
| Überwachung | Prozesse | `ps`, `kill` |
| Host | Host & Betrieb | `sysctl`, `shutdown` |
| Host | System-Einstellungen | `ntpd`/`ntpctl`, `resolv.conf`, `hosts`, `sshd -t`, `doas -C`, `sysctl.conf` |
| Host | Pakete & Updates | `pkg_add`, `pkg_delete`, `pkg_info`, `syspatch`, `fw_update`, `sysupgrade -n` |
| Host | Benutzer & Gruppen | `useradd`, `usermod`, `encrypt`, `crontab` |
| Dienste | Systemdienste | `rcctl` |
| Dienste | Web, Mail & Lastverteilung | `httpd -n`, `smtpd -n`, `smtpctl`, `relayd -n` |
| Verwaltung | Zugriffssteuerung | `/var/maar/access.json`, `/var/maar/tokens.json` |
| Verwaltung | Konfigurationsdateien | `pfctl -nf`, `vmd -n`, `httpd -n`, `doas -C`, `sshd -t` … |

## Grenzen dieser Version

- Getestet gegen nachgebildete Ausgaben von OpenBSD 8.0 und per Cross-Kompilierung; ein Lauf auf echter
  Hardware steht noch aus. Erste Prüfpunkte dort: Anmeldung über `login_passwd`, `pledge`-Promises,
  Öffnen des VM-TTYs, pf mit `link1` auf veb-Ports, unbound mit zusätzlichen `interface`-Zeilen.
- Keine Partitionierung (`disklabel -E`, `newfs`) per Klick – dafür ist die Host-Shell da.
- Kein Live-Snapshot und keine VM-Migration (vmd bietet beides nicht).
- Ein Datastore auf FFS darf nur auf **einem** Host eingehängt sein.
- iSCSI ohne CHAP; NVMe over Fabrics gibt es unter OpenBSD nicht.
- Sitzungen liegen im Speicher; ein Neustart des Dienstes meldet alle ab.
- Formulare gibt es für ntpd, resolv.conf, hosts, sshd, doas, pf, httpd, smtpd, relayd, sysctl und
  Interfaces; andere Dienstdateien (z. B. `bgpd.conf`, `iked.conf`) sind nur als Text bearbeitbar.

## Hilfe und Rückmeldung

- Fehler und Wünsche: [Issues auf GitHub](https://github.com/thorin-eifel/maar/issues)
- Quellcode und Releases: <https://github.com/thorin-eifel/maar>
- Lizenz: [ISC](../LICENSE) · Hinweise zu Marken und eingebetteter Software: [NOTICE.md](../NOTICE.md)
- Support und Betrieb als Dienstleistung: [IT-Haus CTW-Computer Irrel GmbH](https://www.ctw-computer.de) (siehe „Über maar“ in der Oberfläche)
