#!/bin/sh
#
# maar auf OpenBSD installieren oder aktualisieren (als root):
#
#   ftp -o - https://github.com/thorin-eifel/maar/releases/latest/download/install.sh | sh
#
# Umgebung (optional):
#   MAAR_VERSION=1.0.0       bestimmte Version statt der neuesten
#   MAAR_FLAGS="-listen 192.168.50.10:9443"   Startoptionen (nur bei Erstinstallation)
#
set -eu

REPO="thorin-eifel/maar"
VERSION="${MAAR_VERSION:-latest}"

die() { echo "maar-install: $*" >&2; exit 1; }

[ "$(uname -s)" = "OpenBSD" ] || die "nur für OpenBSD"
[ "$(id -u)" -eq 0 ] || die "bitte als root ausführen"

case "$(uname -m)" in
	amd64) ARCH=amd64 ;;
	arm64|aarch64) ARCH=arm64 ;;
	*) die "nicht unterstützte Architektur: $(uname -m)" ;;
esac

if [ "$VERSION" = "latest" ]; then
	BASE="https://github.com/$REPO/releases/latest/download"
else
	BASE="https://github.com/$REPO/releases/download/v${VERSION#v}"
fi

TMP="$(mktemp -d /tmp/maar.XXXXXXXXXX)"
trap 'rm -rf "$TMP"' EXIT INT TERM
BIN="maard-openbsd-$ARCH"

echo "==> Lade $BIN ($VERSION)"
ftp -V -o "$TMP/$BIN" "$BASE/$BIN" || die "Download fehlgeschlagen: $BASE/$BIN"
ftp -V -o "$TMP/SHA256" "$BASE/SHA256" || die "Prüfsummen nicht abrufbar"

echo "==> Prüfe SHA256"
(cd "$TMP" && sha256 -C SHA256 "$BIN") || die "Prüfsumme stimmt nicht – Abbruch"

chmod 0555 "$TMP/$BIN"
NEW="$("$TMP/$BIN" -version 2>&1)" || die "Programm lässt sich nicht starten: $NEW"

UPDATE=no
[ -x /usr/local/sbin/maard ] && UPDATE=yes
RUNNING=no
if [ "$UPDATE" = yes ] && rcctl check maard >/dev/null 2>&1; then
	RUNNING=yes
	echo "==> Stoppe laufenden Dienst"
	rcctl stop maard
fi

echo "==> Installiere $NEW"
install -m 0555 -o root -g bin "$TMP/$BIN" /usr/local/sbin/maard

cat > "$TMP/maard.rc" <<'RC'
#!/bin/ksh
#
# rc.d-Skript für maar (von install.sh installiert).

daemon="/usr/local/sbin/maard"
daemon_flags="-listen :9443"
daemon_logger="daemon.info"

. /etc/rc.d/rc.subr

rc_bg=YES
rc_reload=NO

rc_cmd $1
RC
install -m 0555 -o root -g bin "$TMP/maard.rc" /etc/rc.d/maard
install -d -m 0700 /var/maar /var/maar/backups

if [ "$UPDATE" = no ]; then
	rcctl enable maard
	if [ -n "${MAAR_FLAGS:-}" ]; then
		# shellcheck disable=SC2086
		rcctl set maard flags $MAAR_FLAGS
	fi
fi

if [ "$UPDATE" = no ] || [ "$RUNNING" = yes ]; then
	echo "==> Starte Dienst"
	rcctl start maard || die "Start fehlgeschlagen – siehe /var/log/daemon"
fi

HOST="$(hostname)"
echo
echo "maar ist installiert: $NEW"
echo "  Oberfläche:  https://$HOST:9443   (selbst signiertes Zertifikat)"
echo "  Anmeldung:   Systemkonto der Gruppe wheel"
echo "  Optionen:    rcctl set maard flags -listen 192.168.50.10:9443 && rcctl restart maard"
echo "  Hinweis:     VMs benötigen vmd:  rcctl enable vmd && rcctl start vmd"
