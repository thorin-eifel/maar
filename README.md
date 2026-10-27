# maar – OpenBSD-Verwaltung im CTW Design System 2027

Web-Oberfläche für einen OpenBSD-Host (getestet gegen die Ausgaben von OpenBSD 8.0): System,
virtuelle Maschinen (vmd) inklusive serieller Konsolen im Browser, Dienste, Pakete und Patches,
Benutzer, Netzwerk, Paketfilter, Konfigurationsdateien und Protokolle.

Ein einzelnes statisches Go-Binary; die Oberfläche (HTML, CSS, JS, xterm.js) ist eingebettet.
Auf dem Host wird außer dem Basissystem nichts benötigt.

## Funktionen

Das Menü folgt der Logik des vSphere Clients: **Inventar** (Hosts, VMs, Templates, Konsolen), **Speicher** (geteilter Speicher, lokale Datenträger, Backup), **Netzwerk** (VM-Netze/Portgruppen, Adapter & Routen, Paketfilter), **Überwachung** (Leistung & Alarme, Ereignisse, Prozesse, Protokolle), **Host-Konfiguration** (Dienste, Pakete & Updates, Benutzer, System, Konfigurationsdateien) und **Verwaltung** (Zugriffssteuerung). Snapshots liegen wie in vSphere an der einzelnen VM.

**Inventarbaum:** Neben den Seiten Hosts, VMs, Templates und Konsolen steht ein Baum (Hosts → Host → VMs, Templates, Datastores, Portgruppen) mit Statuspunkten, Suche und Tastatursteuerung (Pfeiltasten, Pos1/Ende). Er lässt sich über „Inventar“ in der Kopfzeile ein- und ausblenden. Eingegrenzte Konten sehen nur ihre VMs. VMs anderer Hosts öffnen die Host-Seite; dort bleibt es beim Lesen.

**VM-Detailseite** (Klick auf eine VM im Baum oder in der Liste) mit den Reitern Zusammenfassung (Status, CPU, Speicher, Hardware, Netz, Firewall, Sicherung, Ereignisse), Überwachen (Verlauf, Alarme, Ereignisse), Konfigurieren (vm.conf, Festplatten mit Format, Größe, Datastore und Basis-Template, Netzwerk, Firewall), Snapshots und Sicherung. Die Reiter sind verlinkbar (`#/vm/<name>/<reiter>`).

**Datastore- und Portgruppen-Seiten** (Klick im Baum oder in den Listen): Beide zeigen „Verwendet von“ – beim Datastore die VMs mit Disks darauf samt Belegung, die Images (unbenutzte werden markiert) und das Gerät dahinter (Transport, WWN, DUID); bei der Portgruppe alle VM-Anschlüsse mit Status, MAC, Adresse, DNS-Name und Firewall, die Konfiguration (Uplink, VLAN, Gateway, NAT, DHCP, DNS) und die erzeugten Dateien. Eingegrenzte Konten sehen dort nur ihre VMs und die Zahl der ausgeblendeten.

| Bereich | Was geht | Werkzeuge darunter |
|---|---|---|
| Übersicht | CPU, Speicher, Laufzeit, Dateisysteme, VMs, ausgefallene Dienste, pf-Status | `sysctl`, `top -b`, `df`, `ifconfig` |
| Virtuelle Maschinen | anlegen (Disk, ISO, Switch, Besitzer, Autostart), starten, von ISO starten, herunterfahren, hart beenden, pausieren, vm.conf-Block bearbeiten, löschen; Disk-Images anlegen (auch qcow2-Overlay), ISOs herunterladen/hochladen; Switches | `vmctl`, `/etc/vm.conf`, `vmd -n` |
| Templates | Bibliothek schreibgeschützter Basisimages; Import per URL (z. B. Debian-Cloud-Image) oder aus vorhandenem Image, „VM als Template speichern“; Bereitstellung als verknüpfter Klon (qcow2-Overlay) oder Vollkopie; Klonen gestoppter VMs; Löschschutz, solange Klone darauf basieren | `vmctl create -b/-i`, qcow2-Header |
| Snapshots | Offline-Snapshots pro VM als qcow2-Kette (alle Disks), Baumansicht mit Verzweigungen, Zurücksetzen (optional mit Sicherung des aktuellen Zustands), Zusammenführen, Umbenennen, alles konsolidieren | `vmctl create -b`, `qemu-img rebase` (Paket `qemu`, nur fürs Zusammenführen) |
| Leistung & Alarme | Verlauf von CPU, Speicher, Load und Netz für den Host sowie CPU, Speicher und Netz je VM (2 h in 10-s-Schritten, 7 Tage als 5-min-Mittel), Diagramme mit Tooltip und Tabellenansicht; Alarmregeln mit Schwellwert und Mindestdauer, Ereignisse (Dienst ausgefallen, VM unerwartet gestoppt, Sicherung fehlgeschlagen), Mail an lokale Konten oder Adressen | `sysctl kern.cp_time`, `vmstat -s`, `netstat -ibn`, `ps`, `mail(1)` |
| Backup | Jobs mit Zeitplan (stündlich, täglich, wöchentlich, manuell) und Aufbewahrung je VM; Ziele lokal, NFS oder SFTP; Modi Snapshot, Offline, Pause; „Jetzt sichern“ je VM; Wiederherstellen als neue VM (optional ohne Netz) oder über die bestehende VM mit Sicherheits-Snapshot; Verlauf | `vmctl create -i`, `mount -t nfs`, `sftp -b`, Snapshot-Kette |
| Gastanpassung | Hostname, DHCP/statische IP, Gateway, DNS, Benutzer, Passwort, SSH-Schlüssel und neue SSH-Hostschlüssel beim ersten Start – über eine Seed-ISO (Label `CIDATA`) | cloud-init NoCloud (Linux), `maar-guest` (OpenBSD), `makefs -t cd9660` |
| Konsolen | serielle Konsole jeder laufenden VM (geteilt zwischen Administratoren, mit 64 KB Verlauf), Login-Shell auf dem Host | TTY der VM (wie `vmctl console`), `su -l` im PTY |
| Dienste | starten, stoppen, neu laden, aktivieren, Flags setzen, Filter „ausgefallen“ | `rcctl` |
| Pakete & Updates | suchen, installieren, entfernen, alle aktualisieren, verwaiste entfernen, `syspatch`, `fw_update`, `sysupgrade -n` (ohne Neustart) | `pkg_add`, `pkg_delete`, `pkg_info` |
| Benutzer | anlegen, bearbeiten, Passwort, sperren, löschen, Gruppen, crontab | `useradd`, `usermod`, `encrypt`, `crontab` |
| Prozesse / Speicher / System | Prozessliste mit Signalen, Dateisysteme, Disklabels, sysctl lesen/setzen, Neustart/Ausschalten | `ps`, `kill`, `disklabel`, `sysctl`, `shutdown` |
| VM-Netze | Portgruppen auf veb mit optionalem Uplink oder VLAN und vport-Gateway; NAT, DHCP-Bereich, DNS-Domain, pf zwischen VMs (`link1`), `locked lladdr`; Anschlüsse je VM mit fester MAC und fester oder dynamischer Adresse; automatische DHCP-Reservierungen und DNS-Einträge (A + PTR); Firewall je VM (Grundregel ein/aus, Regeln mit Port, Netz, `<tabelle>` oder VM als Gegenstelle); Vorschau der erzeugten Dateien | `veb(4)`, `vlan(4)`, `vport(4)`, `vm.conf`, `dhcpd`, `unbound`, pf-Anker `maar` |
| Netzwerk | Schnittstellen mit Adressen, veb-Mitgliedern und Status, up/down, netstart, hostname.if aus Vorlagen (DHCP, statisch, veb, vport, VLAN, WireGuard), Routen, Resolver | `ifconfig`, `netstat -rn`, `/etc/netstart` |
| Paketfilter | Status, Regeln, Tabellen (Adressen hinzufügen/entfernen), States, Regelsatz laden, pf an/aus | `pfctl` |
| Konfiguration | 24 freigegebene Dateien + alle `/etc/hostname.*`; Syntaxprüfung vor dem Speichern, Sicherung, Konflikterkennung, „Übernehmen“-Aktion | `pfctl -nf`, `vmd -n`, `httpd -n`, `doas -C`, `sshd -t`, … |
| Ereignisse | Ereignisprotokoll (Anmeldungen, Aktionen, Aufträge, Ablehnungen; filterbar, die letzten 2000 bleiben erhalten) | `/var/maar/events.jsonl` |
| Zugriffssteuerung | Rollen Administrator, Operator, Betrachter mit Eingrenzung auf bestimmte VMs | `/var/maar/access.json`, `/var/maar/tokens.json` |
| Hosts | mehrere maard-Instanzen in einer Übersicht: Status, CPU, Speicher, VMs, Meldungen, Ereignisse aller Hosts; Detailseite mit Verlauf; „Öffnen“ führt zur lokalen Oberfläche des Hosts | API-Token (nur lesen), Zertifikat-Fingerabdruck |
| Geteilter Speicher | iSCSI-Ziele (anlegen, ändern, aktivieren, löschen, Sitzungsstatus), Fibre-Channel-HBAs mit WWPN, alle SAN-Disks mit Transport, Hersteller, WWN und Verwendung; Datastores (FFS) unter `/var/vmd/<name>`: anlegen, ein-/aushängen, entfernen; Disk-Images direkt in einem Datastore anlegen; NVMe-oF als ausdrücklicher Hinweis | `iscsid`, `iscsictl`, `dmesg`, `fdisk`, `disklabel`, `newfs`, `mount`, `/etc/iscsi.conf`, `/etc/fstab` |
| Protokolle | `/var/log/*` lesen, filtern, live verfolgen | Server-Sent Events |

Lang laufende Befehle (Pakete, Patches, Upgrades, ISO-Downloads) laufen als **Aufgaben** im
Hintergrund; ihre Ausgabe erscheint live in der Aufgabenleiste oben rechts.

## Templates und Gastanpassung

Templates liegen als schreibgeschützte qcow2-Images mit JSON-Beschreibung unter `<vmdir>/templates`.
Eine Bereitstellung legt die Disk an (`vmctl create -b` für verknüpfte Klone, `-i` für Vollkopien),
erzeugt bei Bedarf eine Seed-ISO unter `<vmdir>/seed/<vm>-cidata.iso`, trägt die VM in `vm.conf` ein
und startet sie. Welche VMs auf einem Template aufbauen, liest maar direkt aus dem qcow2-Header.

- **Linux-Gäste:** Cloud-Images mit cloud-init werten die Seed-ISO automatisch aus
  (`meta-data`, `user-data`, `network-config` v2).
- **OpenBSD-Gäste:** Im Template einmalig [`maar-guest`](web/guest/maar-guest.sh) installieren
  (die Templates-Seite zeigt die drei Befehle). Das Skript läuft aus `/etc/rc.local`, wertet
  `maar.cfg` nur bei neuer Instanz-ID aus und erhält das Passwort ausschließlich als bcrypt-Hash.

Klone einer laufenden VM sind nicht möglich, VM-Klone sind immer Vollkopien: Ein Overlay auf eine
Disk, die weiter beschrieben wird, würde beschädigt.

## Snapshots

Snapshots liegen unter `<vmdir>/snapshots/<vm>/<id>/diskN.qcow2` (schreibgeschützt) mit `meta.json`.
Beim Anlegen wird die aktive Disk dorthin verschoben und durch ein leeres Overlay ersetzt – der Pfad
in `vm.conf` bleibt gleich, die Dauer ist unabhängig von der Diskgröße. Die Baumstruktur liest maar
aus den qcow2-Headern, nicht aus einer eigenen Datenbank.

| Vorgang | Ohne qemu-img | Mit qemu-img |
|---|---|---|
| Anlegen, Zurücksetzen, Umbenennen | ✓ | ✓ |
| Blatt löschen (Snapshot ohne Nachfolger) | ✓ | ✓ |
| Snapshot mit Nachfolgern zusammenführen | – | ✓ (`qemu-img rebase`) |
| Alle konsolidieren | ✓, Disk wird eigenständig (`vmctl create -i`) | ✓, Template-Basis bleibt |

Alle Vorgänge verlangen eine gestoppte VM. Live-Snapshots kann vmd nicht.

## Backup

Eine Sicherung liegt auf dem Ziel unter `<vm>/<JJJJMMTT-hhmmss>/` als `manifest.json` (mit dem
`vm.conf`-Block) und `diskN.qcow2`. Die Disks sind vollständig zusammengeführte, eigenständige
qcow2-Images (`vmctl create -i`) – sie lassen sich ohne Template, Snapshots oder maar einsetzen.

| Modus (laufende VM) | Ablauf | Ausfall |
|---|---|---|
| **Snapshot** (empfohlen) | herunterfahren → Disks als Snapshot `maar-backup` einfrieren → starten → aus dem Snapshot sichern; der vorige Backup-Snapshot wird im selben Stopp zusammengeführt | Herunterfahren + Booten |
| Offline | herunterfahren → sichern → starten | gesamte Kopierdauer |
| Pause | pausieren → sichern → fortsetzen (absturzkonsistent) | keine Neustarts, VM steht während der Kopie |

Der Snapshot-Modus braucht `qemu-img` und qcow2-Disks, sonst sichert maar offline und vermerkt das
im Protokoll.

- **Ziele:** lokal (eigene Platte empfohlen; Systemverzeichnisse und das VM-Verzeichnis sind gesperrt),
  NFS (wird pro Lauf unter `/var/maar/mnt/<ziel>` eingebunden), SFTP (eigener Schlüssel
  `/var/maar/backup_ed25519`, die Oberfläche zeigt den öffentlichen Teil; Zwischenablage unter
  `<vmdir>/.backup-staging`).
- **Zeitpläne** laufen im maar-Dienst. Verpasste Termine (Dienst war aus) werden einmal nachgeholt.
  Konfiguration und Verlauf: `/var/maar/backup.json`.
- **Wiederherstellen:** als neue, beim Systemstart deaktivierte VM (standardmäßig ohne Netz, um
  IP-Konflikte mit dem Original zu vermeiden) oder über die gestoppte VM – vorher sichert maar
  deren aktuellen Zustand als Snapshot.

## Leistung & Alarme

maar misst alle 10 Sekunden mit leichtgewichtigen Zählern (kein `top`): CPU aus `kern.cp_time`,
aktiven Speicher aus `vmstat -s`, Netz aus `netstat -ibn` (physische Schnittstellen bzw. die Taps
einer VM, erkannt an der Beschreibung `vmN-ifM-name`) und CPU/RSS der vmd-Prozesse aus `ps`. Dazu
minütlich Dateisystembelegung und ausgefallene Dienste. Die Zeitreihen liegen im Speicher und werden
alle 5 Minuten nach `/var/maar/metrics.json.gz` gesichert.

- **Regeln** vergleichen einen Messwert (mit `*` für „jede VM“ bzw. „jedes Dateisystem“) mit einem
  Schwellwert; ausgelöst wird erst, wenn die Bedingung die Mindestdauer lang ununterbrochen gilt.
  Voreingestellt: Host-CPU und -Speicher > 90 % für 5 min, Dateisystem > 90 %, VM-CPU > 95 % für 15 min.
- **Ereignisse:** Dienst ausgefallen (`rcctl ls failed`), VM unerwartet gestoppt (Stopps über maar
  und Sicherungen zählen nicht), Sicherung fehlgeschlagen.
- **Mail** geht über `mail(1)` an den lokalen smtpd – lokale Konten wie `root` oder Adressen, sofern
  smtpd weiterleiten darf. Gleiche Meldungen höchstens alle 30 Minuten. Konfiguration und Verlauf:
  `/var/maar/alerts.json`.
- Disk-I/O je VM stellt vmd nicht bereit und fehlt deshalb.

## VM-Netze

Die Absicht steht in `/var/maar/network.json`, alles andere wird daraus erzeugt (mit Prüfung und Sicherung wie im Konfigurationseditor):

| Erzeugt | Inhalt |
|---|---|
| `/etc/hostname.vebN`, `.vportN`, `.vlanN` | Switch mit Mitgliedern, Gateway-Adresse, VLAN auf dem Uplink |
| `/etc/vm.conf` | `switch "<name>" { interface vebN; group "<name>_vm"; locked lladdr }`, Anschlüsse der VMs mit `lladdr` |
| `/etc/dhcpd.conf` | eigener Bereich zwischen `# >>> maar` und `# <<< maar`; `dhcpd_flags` um die vports ergänzt |
| `/var/unbound/etc/maar.conf` | `interface`, `access-control`, `local-zone`, A- und PTR-Einträge; per `include:` eingebunden |
| `/etc/pf.maar.conf` | Anker `maar` (NAT, DHCP/DNS zum Gateway, Regeln je VM als `quick` ohne Richtung); `anchor` + `load anchor` am Ende von pf.conf |

Bestehende Switches aus vm.conf werden als „vorgefunden“ übernommen; erst beim Speichern schreibt maar ihre Dateien. Ungetaggte Uplinks dürfen keine eigene IP haben. Deklariert dhcpd.conf ein Subnetz schon außerhalb des maar-Bereichs, bricht der Abgleich mit einem Hinweis ab. Ungeprüft auf echter Hardware: pf mit `link1` auf veb-Ports, Interface-Gruppen über `group` in vm.conf, unbound mit zusätzlichen `interface`-Zeilen.

## Einstellungen per Auswahl statt Konfigurationsdatei

Wo es geht, ändert maar Einstellungen über Formulare mit Auswahlfeldern, die nur Werte anbieten, die es auf dem Host gibt, und schreibt die Konfiguration im Hintergrund (mit Syntaxprüfung und Sicherung). Das verhindert Tippfehler und Werte, die nicht funktionieren.

**VM-Einstellungen** (Knopf „Einstellungen“ an der VM, Reiter Konfigurieren, und der Dialog „Neue VM“):

| Einstellung | Auswahl |
|---|---|
| Arbeitsspeicher | Vorgaben bis zum Hostspeicher, der aktuelle Wert, „Andere …“ (MB/GB, mindestens 64M) |
| vCPUs | 1 bis 64; Werte über der Zahl der Hostkerne sind markiert |
| SEV / SEV-ES | nur auf AMD-Hosts anwählbar |
| Festplatten | nur freie Images (auch aus Datastores), Reihenfolge per Pfeil, „Neues Image …“ legt es sofort an; das Format wird mit `format qcow2/raw` explizit geschrieben |
| CD-ROM | vorhandene ISOs; Boot von CD-ROM erst mit Medium wählbar |
| Boot-Gerät | Standard, Festplatte, CD-ROM, Netzwerk (`boot device cdrom|disk|net`) |
| Besitzer | Benutzer und Gruppe aus den Konten des Hosts |
| Start / lokale Schnittstelle | Schalter |

Alles andere im vm.conf-Block bleibt unverändert (Netzwerkadapter mit MAC, `boot <Kernel>`, `interfaces`, `allow instance`, …). Netzwerkadapter pflegst du weiter unter „Netzwerk“ (Portgruppe, MAC, feste Adresse). Der Server prüft alles noch einmal: Eine Disk, die einer anderen VM gehört, liegt außerhalb von `/var/vmd` oder fehlt, wird abgelehnt, ebenso unbekannte Konten oder mehr Speicher als der Host hat; dann bleibt vm.conf unberührt. Das Bearbeiten als Text bleibt für Experten erreichbar („vm.conf anzeigen“ im Reiter Konfigurieren).

## Rollen und Ereignisse

Die Gruppe (`-group`, Standard `wheel`) entscheidet, wer sich anmelden darf; die Rolle, was danach erlaubt ist. Konten ohne Eintrag erhalten die Standardrolle (anfangs Administrator).

| Rolle | Darf |
|---|---|
| Administrator | alles |
| Operator | lesen; zugewiesene VMs starten, herunterfahren, pausieren, per Konsole bedienen; Snapshots anlegen und zurücksetzen |
| Betrachter | nur lesen – ohne Konfigurationsdateien, Schlüssel, Konsolen (auch VM-Konsolen, weil interaktiv) und Rollenverwaltung |

Ist ein Konto auf VMs eingegrenzt, sieht es nur diese in der VM-Liste und kann nur auf sie zugreifen (die Übersicht nennt weiterhin alle Namen). Eine Änderung der eigenen Rolle, die den Administrator-Zugriff entzieht, wird abgelehnt. Jede Anmeldung, verändernde Aktion und verweigerte Anfrage landet im Ereignisprotokoll und im Syslog. Aufträge (Pakete, Backups, Netz) stehen mit ihrer Ausgabe in der Aufgabenleiste; ihr Ende erscheint als Ereignis „Auftrag beendet“ (mit Fehlertext, falls fehlgeschlagen). Unter „Leistung & Alarme“ lassen sich zusätzlich die Mail-Alarme „Zugriff verweigert“ und „Anmeldung fehlgeschlagen“ einschalten (standardmäßig aus; höchstens eine gleiche Mail je 30 Minuten).

## Mehrere Hosts

Jeder Host betreibt sein eigenes `maard` und wird lokal verwaltet. Ein beliebiger maard dient als Zentrale und fragt die anderen (Peers) lesend ab:

1. Auf dem Peer unter „Verwaltung → Zugriffssteuerung → API-Token“ ein Token anlegen (wird nur einmal angezeigt; gespeichert wird nur der SHA-256-Wert).
2. In der Zentrale unter „Hosts → Host verbinden“ Adresse, Name und Token eintragen und den **Fingerabdruck** des Zertifikats holen. Vergleiche ihn mit dem Zertifikat des Peers und bestätige ihn; ändert sich das Zertifikat später, meldet die Zentrale „Zertifikat geändert“, statt blind zu vertrauen.

Token dürfen nur `GET /api/federation/summary` und die Messwerte lesen (alles andere: 403) und zählen bei Fehlversuchen in die Anmeldesperre. Peers werden nur über https angesprochen (http nur auf Loopback), ohne Zugangsdaten in der URL. Die Zentrale speichert die Peer-Token in `/var/maar/peers.json` (0600) und zeigt sie nie wieder an. Abfragen laufen parallel mit 6 s Zeitlimit und 10 s Zwischenspeicher; ein ausgefallener Peer stört die übrigen nicht. Verwaltungsaktionen (VMs starten, Rollen, Konfiguration) gibt es über die Zentrale bewusst nicht – dafür ist jeder Host selbst zuständig.

In der Demo gibt es zwei simulierte Peers (einer erreichbar, einer aus).

## Geteilter Speicher

| Transport | Stand unter OpenBSD 8.0 | Was maar tut |
|---|---|---|
| iSCSI | Initiator `iscsid(8)`, gesteuert mit `iscsictl(8)` | Ziele als Block `# >>> maar` in `/etc/iscsi.conf` (Syntaxprüfung mit `iscsid -nf`), iscsid aktivieren und starten, `iscsictl reload`; von Hand eingetragene Ziele bleiben unangetastet |
| Fibre Channel | HBA-Treiber `qla`, `qlw`, `qle`; LUNs erscheinen als `sd`-Geräte | zeigt HBAs, WWPN (für Zoning und LUN-Masking) und die LUNs je HBA; eine Konfiguration auf dem Host gibt es nicht |
| NVMe over Fabrics | **nicht vorhanden** – `nvme(4)` bedient nur lokale PCIe-Laufwerke | zeigt das offen als Hinweis, statt etwas vorzutäuschen |

**Datastores:** Eine freie SAN-Disk wird mit GPT, Label und FFS initialisiert und unter `/var/vmd/<name>` eingehängt (fstab-Eintrag mit `noauto`, markiert mit `# maar datastore <name>`). VM-Disks dürfen dort liegen: Beim Anlegen eines Disk-Images wählst du den Speicherort, vorhandene Images eines eingehängten Datastores erscheinen in der VM-Übersicht.

Sicherungen gegen Datenverlust: Initialisiert werden nur Disks mit Transport iSCSI, FC oder Multipath – lokale Disks, die Systemdisk, eingehängte Disks und Disks in `/etc/fstab` nie. Eine Disk mit vorhandenem Label wird gesondert gewarnt, und die Bestätigung verlangt den Gerätenamen. Ein Datastore lässt sich nicht aushängen, solange VMs darauf laufen, und nicht entfernen, solange VMs seine Images verwenden (die Daten bleiben beim Entfernen erhalten). Ein iSCSI-Ziel lässt sich nicht löschen, solange ein Datastore auf einer iSCSI-Disk eingehängt ist.

Grenzen: FFS ist kein Cluster-Dateisystem – ein Datastore darf immer nur auf **einem** Host eingehängt sein; bei mehreren Hosts braucht jeder eigene LUNs (VM-Migration zwischen Hosts gibt es daher noch nicht). Datastores stehen nach einem Neustart ausgehängt da, weil iSCSI beim Booten noch fehlt. CHAP wird noch nicht gesetzt (die Syntax ist in `iscsi.conf(5)` nicht belegt und wird nicht geraten). Multipath: OpenBSD zeigt mehrere Pfade als getrennte Geräte; `mpath(4)` arbeitet nur mit bestimmten Arrays. Das Auslesen von `dmesg` (Gerätezeilen, WWPN) und das Format von `iscsictl show summary` sind nur in der Demo geprüft.

## Ausprobieren ohne OpenBSD

```bash
make demo
```

Öffnet einen simulierten Host auf <http://127.0.0.1:9443> (Anmeldung `demo` / `demo`). Alle Seiten
funktionieren mit realistischen OpenBSD-Ausgaben, Konfigurationsdateien liegen in einer temporären
Sandbox. Die VM-Konsolen sind simuliert; die „Host-Shell“ öffnet im Demo-Modus eine echte lokale
Shell – deshalb lauscht der Demo-Modus ausschließlich auf localhost.

## Installation auf OpenBSD

Entweder auf dem Mac/Linux cross-kompilieren:

```bash
make openbsd
```

```bash
scp build/maard-openbsd-amd64 root@host:/usr/local/sbin/maard
```

```bash
scp etc/rc.d/maard root@host:/etc/rc.d/maard
```

oder direkt auf dem Host bauen (`pkg_add go`, dann im Quellverzeichnis `make install`).

Auf dem Host als root:

```sh
chmod 0555 /usr/local/sbin/maard /etc/rc.d/maard
install -d -m 0700 /var/maar/backups
rcctl enable maard
rcctl set maard flags -listen 192.168.50.10:9443
rcctl start maard
```

Beim ersten Start wird ein selbst signiertes Zertifikat unter `/etc/ssl/maar.crt` bzw.
`/etc/ssl/private/maar.key` erzeugt. Ein eigenes Zertifikat (z. B. von `acme-client`) mit
`-cert` und `-key` angeben.

Anmelden können sich alle Konten der Gruppe `wheel` mit ihrem Systempasswort (BSD Auth,
`login_passwd`). Andere Gruppe: `-group gruppe`.

### Optionen

| Flag | Standard | Bedeutung |
|---|---|---|
| `-listen` | `:9443` | Adresse und Port |
| `-cert`, `-key` | `/etc/ssl/maar.crt`, `/etc/ssl/private/maar.key` | TLS; fehlende Dateien werden selbst signiert erzeugt |
| `-plain-http` | aus | ohne TLS, nur hinter `relayd` auf `127.0.0.1` sinnvoll |
| `-group` | `wheel` | Gruppe mit Anmelderecht |
| `-vmdir` | `/var/vmd` | Disk-Images; ISOs in `<vmdir>/iso` |
| `-no-pledge` | aus | `pledge(2)` abschalten (zur Fehlersuche) |
| `-demo` | aus | simulierter Host |

### pf

Den Zugang auf das Verwaltungsnetz beschränken, z. B.:

```
table <admin> const { 192.168.50.0/24 }
pass in on egress proto tcp from <admin> to (egress) port 9443
```

## Sicherheit

- Läuft als root (Systemverwaltung und BSD Auth brauchen das) und schränkt sich nach dem Start mit
  `pledge(2)` ein. Gestartete Werkzeuge laufen ohne zusätzliche Einschränkung.
- Alle Systemaufrufe gehen über Argumentlisten (`exec`), **nie über eine Shell**; jeder Name, Pfad
  und jede Größe wird vorher gegen eine Positivliste geprüft. Dateipfade für Disk-Images und ISOs
  müssen unter `-vmdir` liegen, Konfigurationsdateien müssen freigegeben sein.
- Sitzungen: zufälliges 256-Bit-Token im Cookie (`HttpOnly`, `Secure`, `SameSite=Strict`),
  30 Minuten Leerlauf, maximal 12 Stunden. Jede schreibende Anfrage verlangt zusätzlich ein
  CSRF-Token im Header. WebSockets prüfen den Origin.
- Anmeldung: 5 Fehlversuche je Quelladresse in 5 Minuten, danach Sperre.
- Strenge Content-Security-Policy (`script-src 'self'`), keine Inline-Skripte, Ausgaben des Systems
  werden nur als Text ins DOM gesetzt.
- Jede verändernde Aktion wird mit Benutzer und Quelladresse über syslog (`daemon.info`,
  Tag `maar`) protokolliert.
- Gefährliche Aktionen (VM löschen, pf aus, Neustart, Snapshot-Upgrade) verlangen das Abtippen eines
  Bestätigungsworts; Aktionen, die den eigenen Zugang kappen könnten (sshd stoppen, Uplink down),
  warnen ausdrücklich.
- Die Host-Shell läuft als angemeldeter Benutzer (`su -l`), nicht als root.

## Aufbau

```
cmd/maard/        Einstieg, TLS, pledge
internal/obsd/      alle Systemzugriffe und Parser (+ Demo-Simulation, Tests)
internal/auth/      BSD Auth, Sitzungen, CSRF, Login-Drosselung
internal/console/   geteilte VM-Konsolen (TTY roh, 115200 Baud), PTY-Shell
internal/jobs/      Hintergrundaufgaben mit Live-Ausgabe
internal/server/    HTTP-API, WebSockets, SSE
web/                Oberfläche im CTW Design System 2027 (ohne Build-Schritt)
etc/rc.d/maard    rc.d-Skript
```

Tests: `make test` (vet für macOS und OpenBSD, Unit- und Integrationstests mit Race-Detector).

## Grenzen dieser Version

- Getestet wurde gegen nachgebildete Ausgaben von OpenBSD 8.0 und per Cross-Kompilierung;
  ein Lauf auf echter Hardware steht noch aus. Erste Prüfpunkte dort: Anmeldung über
  `login_passwd`, `pledge`-Promises, Öffnen des VM-TTYs.
- Keine Partitionierung (`disklabel -E`, `newfs`) per Klick – dafür ist die Host-Shell da.
- Sitzungen liegen im Speicher; ein Neustart des Dienstes meldet alle ab.
