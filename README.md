# maar – OpenBSD-Verwaltung im CTW Design System 2027

Web-Oberfläche für einen OpenBSD-Host (getestet gegen die Ausgaben von OpenBSD 8.0): System,
virtuelle Maschinen (vmd) inklusive serieller Konsolen im Browser, Dienste, Pakete und Patches,
Benutzer, Netzwerk, Paketfilter, Konfigurationsdateien und Protokolle.

Ein einzelnes statisches Go-Binary; die Oberfläche (HTML, CSS, JS, xterm.js) ist eingebettet.
Auf dem Host wird außer dem Basissystem nichts benötigt.

## Funktionen

Das Menü folgt der Logik des vSphere Clients und ist nach Aufgaben geordnet: **Übersicht**; **Inventar** (Hosts, Virtuelle Maschinen, Templates, Konsolen); **Speicher** (Geteilter Speicher, Lokale Datenträger, Backup); **Netzwerk** (VM-Netze, Adapter & Routen, Paketfilter); **Überwachung** (Leistung & Alarme, Ereignisse, Prozesse, Protokolle); **Host** (Host & Betrieb, System-Einstellungen, Pakete & Updates, Benutzer & Gruppen); **Dienste** (Systemdienste, Web, Mail & Lastverteilung); **Verwaltung** (Zugriffssteuerung, Konfigurationsdateien). Snapshots liegen wie in vSphere an der einzelnen VM. Jeder Menüpunkt, Reiter, Knoten des Inventarbaums, Status und jede Standardaktion trägt ein Symbol aus einem eigenen Linien-Symbolsatz (`web/js/icons.js`, über 90 Symbole; erbt Farbe und Größe, hell und dunkel, ohne externe Dateien). Schaltflächen bekommen ihr Symbol automatisch nach ihrer Beschriftung (`web/js/decorate.js`); Status tragen zusätzlich zur Farbe ein Symbol (Haken, Warndreieck, Kreuz, Info, Strich). Die Gruppen lassen sich auf- und zuklappen; der Browser merkt sich den Zustand.

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

**System-Einstellungen** (Host → „System-Einstellungen“, nur Administratoren): Formulare für `/etc/ntpd.conf`, `/etc/resolv.conf`, `/etc/hosts` und `/etc/ssh/sshd_config`. Jedes Formular hat eine **Vorschau** der Datei, die entstehen würde, und schreibt erst bei „Speichern und anwenden“ – mit Syntaxprüfung (`ntpd -n`, `sshd -t`), Sicherung der alten Fassung und anschließendem Neustart bzw. Neuladen des Dienstes. Kommentare und alle Zeilen, die das Formular nicht kennt, bleiben unverändert und werden angezeigt.

| Datei | Auswahl |
|---|---|
| ntpd.conf | Server (einzeln oder Pool, Gewicht 1–10, vertrauenswürdig), nur tatsächlich vorhandene Zeit-Sensoren (`hw.sensors`), Constraints (HTTPS), „listen on“ mit den Adressen des Hosts; Status aus `ntpctl -s all` |
| resolv.conf | bis zu 5 Nameserver (Vorschläge: laufender Resolver, Gateway, öffentliche Resolver), Suchdomänen (max. 6), Reihenfolge `lookup`, `family`, `edns0`, `tcp`, `trust-ad`, `ndots`; Warnung, wenn `resolvd` die Datei umschreiben kann |
| hosts | Tabelle der Einträge (Adresse, Namen, Kommentar), VM-Adressen aus den VM-Netzen übernehmen; `localhost` bleibt immer erhalten |
| sshd_config | Root-Anmeldung, Passwort/Schlüssel/Tastatur-interaktiv, `AllowUsers`/`AllowGroups` aus den Konten (mit Anzahl hinterlegter Schlüssel), Ports, `ListenAddress` aus den Host-Adressen, Fehlversuche, Weiterleitungen, Protokollstufe |

Der SSH-Teil schützt vor dem Aussperren: Abgelehnt werden Einstellungen, bei denen keine Anmeldemethode übrig bliebe, du selbst nicht mehr in `AllowUsers`/`AllowGroups` stündest oder du die Passwort-Anmeldung abschaltest, ohne einen Schlüssel in `~/.ssh/authorized_keys` zu haben. Geändert wird nur der globale Teil vor dem ersten `Match`; Match-Blöcke bleiben, und ein Hinweis nennt sie. Bei geänderten Ports prüft maar, ob `pf.conf` sie erwähnt.

**Kernel-Parameter** (System-Einstellungen → „Kernel (sysctl)“): `/etc/sysctl.conf` als Tabelle. Zur Auswahl stehen die häufigen Parameter mit Beschreibung, Wertebereich und Sicherheitshinweis (nur, was der laufende Kernel kennt, z. B. Weiterleitung, `somaxconn`, `maxfiles`, Puffercache, SMT) sowie jeder andere Parameter dieses Kernels über die Suche. Werte werden nach ihrer Art geprüft (0/1, Zahlenbereich, feste Auswahl, Zahl oder Text wie der aktuelle Wert); ein Parameter muss schreibbar sein – das prüft maar vor dem Speichern mit einem wirkungslosen Setzen des aktuellen Werts, nur lesbare lehnt es ab. „Geänderte Werte sofort anwenden“ setzt sie zusätzlich im laufenden Kernel; entfernte Einträge wirken bis zum Neustart nach. Riskante Parameter (`kern.securelevel`, `kern.allowkmem`, `machdep.allowaperture`, Source-Routing) lösen eine Warnung aus.

**Netzwerk-Interfaces** (Adapter & Routen → „Einstellungen“ am Interface, sowie „VLAN anlegen“): `/etc/hostname.<if>` als Formular. IPv4 per DHCP, fest (Hauptadresse mit Netz-Auswahl, weitere Adressen als Aliase, Standard-Gateway in `/etc/mygate`) oder keine; IPv6 automatisch (SLAAC), fest oder keine; Beschreibung, MTU (Standard, 1280, 1492, 1500, Jumbo 9000) und aktiv/abgeschaltet; bei VLANs physisches Interface und ID. Zeilen, die das Formular nicht kennt (`lladdr`, `media`, `!befehl`, …), bleiben unverändert und werden angezeigt; Interfaces, die die VM-Netze erzeugen (veb, vport, vlan der Portgruppen), sind hier gesperrt und verweisen dorthin.

Wie beim Paketfilter gilt jede Änderung **auf Probe**: Nach dem Anwenden mit `netstart` hast du 60 Sekunden zum Bestätigen. Lädt die Seite danach noch, bist du durchgekommen; sonst stellt maar Datei, Gateway und Interface wieder her (bei einem neuen VLAN wird es wieder entfernt) – auch nach einem Neustart von maard. Geprüft werden Adressen und Netze (keine Netz- oder Broadcast-Adresse, keine doppelte oder auf einem anderen Interface schon vergebene Adresse), das Gateway im eigenen Netz, MTU, Beschreibung und VLAN-Angaben; für das Uplink-Interface gibt es eine eigene Warnung.

**Paketfilter-Regeln** (Netzwerk → Paketfilter → „Regeln bearbeiten“): Regeln und Tabellen als Formular. Aktion, Richtung, Interface (aus `ifconfig`, plus `egress`), Protokoll, Von/Nach (beliebig, `self`, vorhandene Tabellen, Adresse oder Netz), Zielports (Dienstliste oder Zahlen, Bereiche), `quick`, `log`, Reihenfolge per Pfeil und die Regel als Klartext direkt darunter. maar verwaltet einen Block in `/etc/pf.conf` vor dem Anker für die VM-Netze; die bestehenden Regeln werden angezeigt und bleiben unberührt. Neue Tabellen dürfen keine vorhandene überdecken.

Damit man sich nicht aussperrt, gelten neue Regeln **auf Probe**: Nach dem Laden hast du 60 Sekunden, sie zu bestätigen – wenn die Seite nach dem Laden noch antwortet, ist das der Beweis, dass du noch durchkommst. Ohne Bestätigung stellt maar den alten Regelsatz selbst wieder her; das gilt auch, wenn maard in der Zwischenzeit beendet wird (Wiederherstellung beim Start). Regeln, die die Oberfläche oder SSH sperren könnten, bekommen eine Warnung. Bei mehreren passenden Regeln gilt in pf die letzte, außer eine passende ist `quick`.

**doas** (System-Einstellungen → „Rechte (doas)“): Regeln mit Auswahl für Konto oder Gruppe (`:wheel`), Zielbenutzer, Befehl (Programme, die es auf dem Host gibt, oder eigener Pfad), Argumente und die Optionen `nopass`, `persist`, `keepenv`, `nolog`; unter jeder Regel steht sie als deutscher Satz. Die Reihenfolge bleibt erhalten, Beschreibungen (Kommentare) wandern mit der Regel, Regeln mit `setenv` bleiben als Rohtext unverändert. `doas -C` prüft vor dem Speichern. Warnungen gibt es für `nopass` ohne Befehl, relative Befehle (doas vergleicht sie buchstäblich) und wenn du dir selbst die Rechte entziehst.

**Web, Mail & Lastverteilung** (Dienste → „Web, Mail & Lastverteilung“, nur Administratoren):

| Dienst | Formular |
|---|---|
| httpd | Webseiten (`server`-Blöcke): Name, Aliase, Adressen mit Port und TLS, Wurzelverzeichnis (Ordner unter `/var/www/htdocs`), Zertifikat und Schlüssel aus `/etc/ssl`, Verzeichnisliste, Indexdatei, Protokollformat, HSTS, Weiterleitung (`block return 30x`), `prefork`. `location`-Blöcke, `fastcgi`, `authenticate` u. ä. bleiben wörtlich erhalten. Mit „Webseite mit HTTPS hinzufügen“ als Vorlage. |
| smtpd | Zertifikate (`pki`), Tabellen (`table`, Dateien aus `/etc/mail`), Annahme (`listen on` mit Schnittstelle, Port, STARTTLS/smtps, Zertifikat, Anmeldung), Zustellung (`action`: mbox, Maildir, LMTP, relay mit Smarthost, TLS, Anmeldedaten) und Regeln (`match`, erste Treffer gewinnt, jede als deutscher Satz). Zeilen mit Optionen, die das Formular nicht kennt, bleiben als Rohzeile in ihrem Abschnitt und an ihrer Stelle. |

**Mail-Adressen** (dritter Reiter): Die von smtpd verwendeten Dateitabellen als Tabelle zum Bearbeiten – Aliase (`name: ziel, ziel`) und virtuelle Adressen (`info@example.org ziel` oder `@example.org` für alle übrigen Adressen einer Domain). Ziele sind lokale Benutzer (Auswahl) oder Mail-Adressen. maar lehnt Schleifen unter Aliasen, unbekannte Ziele und das Entfernen von `root` ab; Zeilen mit Befehl (`|…`), Datei oder `:include:` bleiben als Rohzeile unverändert und lassen sich nicht neu anlegen. Nach dem Speichern liest `smtpctl update table` die Tabelle neu ein. Tabellen mit Anmeldedaten (Passwort-Hashes) werden bewusst weder gelesen noch angezeigt.

Gespeichert wird mit Vorschau, Syntaxprüfung (`httpd -n`, `smtpd -n`), Sicherung und Neuladen bzw. Neustart des Dienstes; ist der Dienst aus, bietet das Formular „aktivieren und starten“ an. Schutz vor Fehlern: smtpd-Regeln, die jedem Absender die Weiterleitung an beliebige Empfänger erlauben (offenes Relay), werden abgelehnt; Anmeldung braucht TLS; Tabellen, Zertifikate und Aktionen müssen es geben; Pfade und Namen werden geprüft. Hinweise gibt es, wenn `pf.conf` einen verwendeten Port nicht erwähnt.

**Lastverteilung** (Web, Mail & Lastverteilung → „Lastverteilung (relayd)“): `/etc/relayd.conf` als Formular. Zieltabellen mit Hosts (VM-Adressen aus den VM-Netzen per Auswahl, Wiederholungen bei Fehler), Relays (Schicht 7: Adresse, Port, TLS, Protokoll, Zieltabelle, Verteilverfahren, Zustandsprüfung per Ping, TCP, TLS oder HTTP-Abruf mit Pfad und Statuscode, Sitzungs-Timeout) und Weiterleitungen (Schicht 3, mit Portbereich und `sticky-address`). Neue Protokolle entstehen aus Vorlagen (HTTP mit `X-Forwarded-For`, HTTPS mit Schlüsselpaar aus `/etc/ssl`, TCP); vorhandene Protokolle, Makros und alles, was das Formular nicht versteht, bleiben unverändert (ganze Blöcke mit unbekannten Anweisungen sind schreibgeschützt). Geprüft werden Namen, Verweise auf Tabellen und Protokolle, Verteilverfahren je Art, Intervall gegen Timeout und Adressen des Hosts; Hinweise gibt es zu fehlendem `anchor "relayd/*"` in `pf.conf`, TLS ohne Schlüsselpaar und nicht erlaubten Ports.

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
