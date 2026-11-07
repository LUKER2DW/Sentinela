#!/usr/bin/env bash
# Configure the LAN file server (ADR-0008). Idempotent. Run as root on the
# server from the repo checkout:  sudo ./scripts/setup-nas.sh
# The share account is the user running sudo (or SENTINELA_NAS_USER). The
# Samba password is never set here: run `sudo smbpasswd -a <user>` yourself.
set -euo pipefail
REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
[[ $EUID -eq 0 ]] || { echo "run with sudo" >&2; exit 1; }
# shellcheck source=scripts/lib/lan.sh
source "${REPO_DIR}/scripts/lib/lan.sh"
NAS_USER="${SENTINELA_NAS_USER:-${SUDO_USER:-}}"
[[ -n "$NAS_USER" && "$NAS_USER" != root ]] || { echo "run with sudo from your account, or set SENTINELA_NAS_USER" >&2; exit 1; }
id "$NAS_USER" >/dev/null 2>&1 || { echo "no such user: $NAS_USER" >&2; exit 1; }
# One name everywhere: hostname, NetBIOS, WS-Discovery and mDNS
# (SENTINELA_HOSTNAME, default "sentinela"; NetBIOS takes up to 15 characters).
NAME="${SENTINELA_HOSTNAME:-sentinela}"
[[ "$NAME" =~ ^[a-z0-9][a-z0-9-]{0,14}$ ]] || { echo "SENTINELA_HOSTNAME: 1-15 lowercase letters, digits or -" >&2; exit 1; }
NETBIOS="${NAME^^}"
IFACE="$(lan_iface)"
LAN="$(lan_cidr)"
[[ -n "$IFACE" && -n "$LAN" ]] || { echo "could not find the LAN; set SENTINELA_LAN_IFACE and SENTINELA_LAN" >&2; exit 1; }

for m in /srv/nas /data; do
  mountpoint -q "$m" || { echo "$m is not mounted; refusing to share the root disk" >&2; exit 1; }
done

DEBIAN_FRONTEND=noninteractive apt-get -o DPkg::Lock::Timeout=600 install -y -qq samba wsdd avahi-daemon >/dev/null

sed -e "s|@NAS_USER@|${NAS_USER}|g" -e "s|@NETBIOS@|${NETBIOS}|g" -e "s|@LAN_IFACE@|${IFACE}|g" -e "s|@LAN@|${LAN}|g" \
  "${REPO_DIR}/deploy/nas/smb.conf" > /etc/samba/smb.conf
chmod 0644 /etc/samba/smb.conf
rm -rf /etc/samba/smb.conf.d
testparm -s /etc/samba/smb.conf >/dev/null

# wsdd: makes \NAME appear under "Network" on Windows 10/11.
if [[ -f /etc/default/wsdd ]]; then
  sed -i "s/^WSDD_PARAMS=.*/WSDD_PARAMS=\"-i ${IFACE} -n ${NETBIOS}\"/" /etc/default/wsdd
fi

if [[ "$(hostname)" != "$NAME" ]]; then
  old="$(hostname)"
  hostnamectl set-hostname "$NAME"
  sed -i "s/\b${old}\b/${NAME}/g" /etc/hosts
  grep -qw "$NAME" /etc/hosts || echo "127.0.1.1 ${NAME}" >> /etc/hosts
fi
install -m 0644 "${REPO_DIR}/deploy/nas/avahi-smb.service" /etc/avahi/services/smb.service
rm -f /etc/avahi/services/sentinela*.service

# Recycle bin purge (files deleted more than 30 days ago).
install -m 0644 "${REPO_DIR}/deploy/host/sentinela-trash.service" "${REPO_DIR}/deploy/host/sentinela-trash.timer" /etc/systemd/system/
systemctl daemon-reload
systemctl enable -q --now sentinela-trash.timer

systemctl enable -q --now smbd nmbd wsdd avahi-daemon
systemctl restart smbd nmbd wsdd avahi-daemon
# Owner's choice: 4-digit PINs allowed for the share (Samba's default minimum
# is 5). LAN-only share behind the firewall; see ADR-0008.
pdbedit -P "min password length" -C 4 >/dev/null

if command -v ufw >/dev/null && ufw status | grep -q active; then
  ufw allow from "$LAN" to any port 445 proto tcp comment 'smb lan' >/dev/null
  ufw allow from "$LAN" to any port 137,138 proto udp comment 'netbios lan' >/dev/null
  ufw allow from "$LAN" to any port 139 proto tcp comment 'netbios lan' >/dev/null
  ufw allow from "$LAN" to any port 3702 proto udp comment 'ws-discovery' >/dev/null
  ufw allow from "$LAN" to any port 5357 proto tcp comment 'ws-discovery' >/dev/null
fi

echo "Shares: $(testparm -s 2>/dev/null | grep -E '^\[' | grep -v global | tr '\n' ' ')"
pdbedit -L 2>/dev/null | grep -q "^${NAS_USER}:" || echo "Now set the share password: sudo smbpasswd -a ${NAS_USER}"
