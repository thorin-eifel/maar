# 9 · Betrieb und Fehlersuche

[← Zugriff und Sicherheit](08-zugriff-und-sicherheit.md) · [Dokumentation](README.md) · [Weiter: Referenz →](10-referenz.md)

## maar aktualisieren

Neue Versionen erscheinen als [Release auf GitHub](https://github.com/thorin-eifel/maar/releases). Als root:

```sh
ftp -o - https://github.com/thorin-eifel/maar/releases/latest/download/install.sh | sh
```

Der Dienst stoppt kurz, die neue Datei wird installiert, der Dienst startet wieder. Startoptionen
(`rcctl get maard flags`) und alle Daten unter `/var/maar` bleiben. Laufende **VMs sind nicht betroffen** –
sie gehören `vmd`, nicht `maard`. Offene Browsersitzungen müssen sich nach dem Neustart neu anmelden
(Sitzungen liegen im Speicher). Die installierte Version zeigt der Klick auf die Versionsnummer unten
links oder `maard -version`.

Zurück auf eine ältere Version: `MAAR_VERSION=1.0.0` vor `sh` (siehe [Erste Schritte](01-erste-schritte.md#installation)).

## Was maar speichert

Alles liegt unter `/var/maar` (Modus 0700):

| Datei | Inhalt |
|---|---|
| `access.json` | Rollen und VM-Eingrenzungen |
| `tokens.json` | API-Token (nur SHA-256-Werte) |
| `peers.json` | Peers und deren Token (Modus 0600) |
| `network.json` | Absicht der VM-Netze (Portgruppen, Anschlüsse) |
| `backup.json` | Backup-Ziele, -Jobs und -Verlauf |
| `backup_ed25519` | SFTP-Schlüssel für Backups |
| `alerts.json` | Alarmregeln, Mail-Einstellungen, Verlauf |
| `metrics.json.gz` | Messwerte (alle 5 Minuten gesichert) |
| `events.jsonl` | Ereignisprotokoll (letzte 2000 Einträge) |
| `pf-pending.json`, `if-pending.json` | laufende Änderungen *auf Probe* (nur kurz) |
| `backups/` | Sicherungen **der geänderten Konfigurationsdateien** (vor jedem Schreiben) |
| `mnt/` | Einhängepunkte für NFS-Backupziele |

Diese Daten sind klein. **Sichere `/var/maar`** mit deinem üblichen Backup (z. B. `tar czf` nachts oder
`rsync` auf einen anderen Host) – ohne sie gehen Rollen, Token, Netz-Absicht und Backup-Jobs verloren. Die
VM-Disks selbst liegen unter `/var/vmd`.

**Neuer Host:** maar installieren, `/var/maar` zurückkopieren, `rcctl restart maard`. Die erzeugten
Konfigurationsdateien (`/etc/vm.conf`, `hostname.*`, `dhcpd.conf` …) sichert dein normales
Systembackup; maar erzeugt sie aus `network.json` bei Bedarf neu.

## Dienst steuern

```sh
rcctl check maard          # läuft er?
rcctl restart maard        # neu starten
rcctl get maard flags      # Startoptionen
rcctl set maard flags -listen 192.168.50.10:9443   # ändern
tail -f /var/log/daemon    # Protokoll des Dienstes
```

## Fehlersuche

### Die Oberfläche lässt sich nicht öffnen

1. `rcctl check maard` – läuft der Dienst? Sonst `rcctl start maard` und `tail /var/log/daemon`.
2. Lauscht er? `netstat -an -f inet | grep 9443`. Bei `-listen 127.0.0.1:…` ist er nur lokal erreichbar.
3. Lässt `pf` den Port durch? Prüfe `pfctl -sr | grep 9443` und die Regel in [pf](04-netzwerk-und-firewall.md#firewall-absichern-ein-beispiel).
4. Browser: Warnung zum selbst signierten Zertifikat? Ausnahme bestätigen oder
   [eigenes Zertifikat](08-zugriff-und-sicherheit.md#tls-zertifikat) einrichten.

### Die Anmeldung wird abgelehnt

- Das Konto muss in der Gruppe **`wheel`** sein (`id benutzer`), oder du startest `maard` mit `-group <gruppe>`.
- Das Passwort ist das Systempasswort (BSD Auth). Prüfe es mit `su - benutzer`.
- **Gesperrt?** Nach 5 Fehlversuchen in 5 Minuten sperrt maar die Quelladresse für kurze Zeit; warte oder
  starte den Dienst neu.
- Hat dein Konto nur die Rolle *Betrachter*, fehlen dir viele Menüpunkte – das ist kein Fehler.

### Eine VM startet nicht

- Läuft `vmd`? **Dienste → Systemdienste**, sonst `rcctl enable vmd && rcctl start vmd`.
- **Letzte Ereignisse** an der VM zeigen die Fehlermeldung von `vmctl`.
- `vmd -n` prüft `vm.conf`; **Protokolle → daemon** zeigt Meldungen von `vmd`.
- Disk-Image fehlt oder liegt nicht unter `/var/vmd`? **Konfigurieren → Festplatten** prüfen.
- Zu wenig freier Speicher auf dem Host, oder die CPU kennt keine Virtualisierung
  (`dmesg | grep -i vmm`).

### Die VM hat kein Netz

- Hängt der Anschluss an der richtigen **Portgruppe**? **Netzwerk → VM-Netze** → Portgruppe → „Verwendet von“.
- Ist **DHCP** für die Portgruppe aktiv und die Reservierung korrekt? `rcctl check dhcpd`.
- **NAT** an? Und ist `net.inet.ip.forwarding=1` gesetzt ([sysctl](06-host-und-dienste.md#kernel-sysctl))?
- Blockt die **Firewall der VM** oder `pf.conf` den Verkehr? `pfctl -sr`, **Paketfilter → Geladene Regeln**.

### Eine Änderung „auf Probe“ wurde zurückgenommen

Das ist gewollt: Du hast nicht innerhalb von 60 Sekunden bestätigt (oder die Seite war nach dem Anwenden nicht
mehr erreichbar). maar hat den alten Zustand wiederhergestellt – prüfe, ob die neue Regel oder Adresse dich
selbst ausgesperrt hätte, und versuche es erneut. Die Wiederherstellung läuft auch nach einem Neustart von `maard`.

### Ein Backup schlägt fehl

- **Ziel erreichbar?** Bei NFS `showmount -e server`, bei SFTP den öffentlichen Schlüssel auf dem Server prüfen.
- **Platz auf dem Ziel?** Eine Sicherung ist eine vollständige Kopie.
- Der Snapshot-Modus braucht `qemu-img` (`pkg_add qemu`); ohne fällt maar auf *offline* zurück.
- Details: **Backup → Verlauf** und **Überwachung → Ereignisse**.

### Die Oberfläche zeigt veraltete Werte

Die Seiten aktualisieren sich regelmäßig (Inventar alle 15 s, VM-Seiten alle 20 s). Mit **Aktualisieren** oben
rechts erzwingst du es. Läuft ein Job, siehst du ihn unter **Aufgaben**.

### Ein Dienst-Formular lehnt meine Eingabe ab

Das Formular hat die Datei mit dem Werkzeug geprüft (z. B. `sshd -t`) und zeigt dessen Meldung. Lies sie –
sie nennt Zeile und Grund. Deine Datei blieb dabei unverändert. Die **Vorschau** zeigt, was maar geschrieben hätte.

## Konfiguration zurückholen

Vor jedem Schreiben legt maar eine Sicherung der alten Datei unter `/var/maar/backups` ab (mit Zeitstempel).
Zum Zurückholen: Datei kopieren, `-n`-Prüfung des Dienstes, Dienst neu laden. Beispiel:

```sh
ls -lt /var/maar/backups | head
cp /var/maar/backups/<datei> /etc/sshd_config
sshd -t && rcctl reload sshd
```

## Deinstallieren

```sh
rcctl stop maard
rcctl disable maard
rm /etc/rc.d/maard /usr/local/sbin/maard
# optional, löscht Rollen, Token und Jobs:
rm -rf /var/maar
```

VMs, `vm.conf` und alle von maar erzeugten Konfigurationsdateien bleiben bestehen und funktionieren
weiter. Die Blöcke von maar sind in den Dateien markiert (`# >>> maar … # <<< maar`) und lassen sich
von Hand entfernen.

## Dokumentation und Screenshots pflegen

Die Bilder in `docs/img/` entstehen aus der Demo-Instanz und lassen sich neu erzeugen, wenn sich die
Oberfläche ändert:

```sh
make demo &                       # simulierter Host auf 127.0.0.1:9443
node docs/tools/screenshots.mjs   # braucht Google Chrome und Node ≥ 22
```

Das Skript meldet sich als `demo`/`demo` an, besucht jede Seite (und einige Dialoge) mit 1600 × 1000 Pixeln
bei 1,25-facher Dichte und schreibt die JPEGs (2000 px breit) nach `docs/img/`. Die Liste der Aufnahmen steht
am Anfang des Skripts.

Weiter: [Referenz](10-referenz.md).
