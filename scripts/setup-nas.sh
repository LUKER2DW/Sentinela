#!/usr/bin/env bash
# Configure the LAN file server (ADR-0008). Idempotent. Run as root on the
# server from the repo checkout:  sudo ./scripts/setup-nas.sh
# The Samba password is never set here: run `sudo smbpasswd -a ana` yourself.
set -euo pipefail
REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
[[ $EUID -eq 0 ]] || { echo "run with sudo" >&2; exit 1; }

for m in /srv/nas /data; do
  mountpoint -q "$m" || { echo "$m is not mounted; refusing to share the root disk" >&2; exit 1; }
done

DEBIAN_FRONTEND=noninteractive apt-get install -y -qq samba wsdd >/dev/null

install -m 0644 "${REPO_DIR}/deploy/nas/smb.conf" /etc/samba/smb.conf
rm -rf /etc/samba/smb.conf.d
testparm -s /etc/samba/smb.conf >/dev/null

# wsdd: makes \SENTINELA appear under "Network" on Windows 10/11.
if [[ -f /etc/default/wsdd ]]; then
  sed -i 's/^WSDD_PARAMS=.*/WSDD_PARAMS="-i eno1 -n SENTINELA"/' /etc/default/wsdd
fi

systemctl enable -q --now smbd nmbd wsdd
systemctl restart smbd nmbd wsdd

if command -v ufw >/dev/null && ufw status | grep -q active; then
  LAN=192.168.1.0/24
  ufw allow from $LAN to any port 445 proto tcp comment 'smb lan' >/dev/null
  ufw allow from $LAN to any port 137,138 proto udp comment 'netbios lan' >/dev/null
  ufw allow from $LAN to any port 139 proto tcp comment 'netbios lan' >/dev/null
  ufw allow from $LAN to any port 3702 proto udp comment 'ws-discovery' >/dev/null
  ufw allow from $LAN to any port 5357 proto tcp comment 'ws-discovery' >/dev/null
fi

echo "Shares: $(testparm -s 2>/dev/null | grep -E '^\[' | grep -v global | tr '\n' ' ')"
pdbedit -L 2>/dev/null | grep -q '^ana:' || echo "Now set the share password: sudo smbpasswd -a ana"
