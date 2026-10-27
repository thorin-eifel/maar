#!/bin/sh
#
# maar-guest – Erststart-Anpassung für OpenBSD-Gäste, die aus einem
# maar-Template bereitgestellt werden.
#
# Installation im Template (als root):
#   install -m 0555 maar-guest.sh /usr/local/libexec/maar-guest
#   echo /usr/local/libexec/maar-guest >> /etc/rc.local
#
# Beim Start sucht das Skript eine CD mit maar.cfg (Seed-ISO, Label CIDATA).
# Findet es eine neue Instanz-ID, setzt es Hostname, Netz, Nameserver,
# Benutzer, Passwort-Hash und SSH-Schlüssel und erzeugt neue SSH-Hostschlüssel,
# damit Klone sich nicht dieselbe Identität teilen. Danach passiert bis zur
# nächsten neuen Instanz-ID nichts mehr.

set -u
PATH=/bin:/sbin:/usr/bin:/usr/sbin
STATE=/var/db/maar-instance

log() { logger -t maar-guest "$*"; echo "maar-guest: $*"; }

MNT=$(mktemp -d /tmp/maar-guest.XXXXXXXX) || exit 1
trap 'umount "$MNT" 2>/dev/null; rmdir "$MNT"' EXIT

CFG=
for dev in cd0 cd1 cd2; do
	mount -r -t cd9660 /dev/${dev}a "$MNT" 2>/dev/null || continue
	for f in maar.cfg MAAR.CFG; do
		[ -f "$MNT/$f" ] && CFG="$MNT/$f"
	done
	[ -n "$CFG" ] && break
	umount "$MNT"
done
[ -n "$CFG" ] || exit 0

INSTANCE= HOST= NET=dhcp ADDR= GW= DNS= USER=root PWHASH= SSHKEY=
while read -r key val; do
	case $key in
	instance) INSTANCE=$val ;;
	hostname) HOST=$val ;;
	net)      NET=$val ;;
	address)  ADDR=$val ;;
	gateway)  GW=$val ;;
	dns)      DNS="$DNS $val" ;;
	user)     USER=$val ;;
	pwhash)   PWHASH=$val ;;
	sshkey)   SSHKEY=$val ;;
	esac
done < "$CFG"

[ -n "$INSTANCE" ] || exit 0
[ "$(cat $STATE 2>/dev/null)" = "$INSTANCE" ] && exit 0
log "neue Instanz $INSTANCE – passe System an"

# Neue Identität: SSH-Hostschlüssel und IPv6-SOII-Schlüssel.
rm -f /etc/ssh/ssh_host_*
ssh-keygen -A >/dev/null
openssl rand -hex 16 > /etc/soii.key 2>/dev/null && chmod 600 /etc/soii.key

if [ -n "$HOST" ]; then
	echo "$HOST" > /etc/myname
	hostname "$HOST"
fi

# Erste virtio-Netzwerkkarte konfigurieren.
IF=$(ifconfig vio0 >/dev/null 2>&1 && echo vio0)
if [ -n "$IF" ] && [ "$NET" != none ]; then
	case $NET in
	dhcp)
		printf 'inet autoconf\ninet6 autoconf\n' > /etc/hostname.$IF
		;;
	static)
		printf 'inet %s\n' "$ADDR" > /etc/hostname.$IF
		if [ -n "$GW" ]; then echo "$GW" > /etc/mygate; else rm -f /etc/mygate; fi
		;;
	esac
	chmod 640 /etc/hostname.$IF
	if [ -n "$DNS" ]; then
		: > /etc/resolv.conf
		for d in $DNS; do echo "nameserver $d" >> /etc/resolv.conf; done
	fi
	sh /etc/netstart $IF
	[ "$NET" = static ] && [ -n "$GW" ] && route -qn add -inet default "$GW" >/dev/null 2>&1
fi

if [ "$USER" != root ] && ! id "$USER" >/dev/null 2>&1; then
	useradd -m -G wheel -s /bin/ksh "$USER"
	[ -f /etc/doas.conf ] || echo 'permit persist :wheel' > /etc/doas.conf
fi
[ -n "$PWHASH" ] && usermod -p "$PWHASH" "$USER"

if [ -n "$SSHKEY" ]; then
	HOME_DIR=$(awk -F: -v u="$USER" '$1 == u { print $6 }' /etc/passwd)
	install -d -m 700 -o "$USER" "$HOME_DIR/.ssh"
	grep -qxF "$SSHKEY" "$HOME_DIR/.ssh/authorized_keys" 2>/dev/null ||
		echo "$SSHKEY" >> "$HOME_DIR/.ssh/authorized_keys"
	chown "$USER" "$HOME_DIR/.ssh/authorized_keys"
	chmod 600 "$HOME_DIR/.ssh/authorized_keys"
fi

rcctl restart sshd >/dev/null 2>&1
echo "$INSTANCE" > $STATE
log "fertig: $HOST"
