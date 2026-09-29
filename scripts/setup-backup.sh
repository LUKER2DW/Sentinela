#!/usr/bin/env bash
# One-time/idempotent backup setup (ADR-0014). Run on the server:
#   sudo /opt/sentinela/scripts/setup-backup.sh
# - Moves sda3 (label nuvem-extra) from /srv/nas/Extra to /srv/backup
#   (owner decision D7). Refuses if it holds anything but lost+found.
# - Installs sqlite3 + restic and the nightly sentinela-backup.timer.
# restic stays idle until /etc/sentinela/restic.env exists (see backup.sh).
set -euo pipefail
[[ $EUID -eq 0 ]] || { echo "run with sudo" >&2; exit 1; }
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OLD=/srv/nas/Extra
NEW=/srv/backup

if grep -qE "[[:space:]]$OLD[[:space:]]" /etc/fstab; then
  if mountpoint -q "$OLD"; then
    left="$(find "$OLD" -mindepth 1 -maxdepth 1 ! -name lost+found | head -n 3)"
    [[ -z "$left" ]] || { echo "$OLD is not empty; move its files first:" >&2; echo "$left" >&2; exit 1; }
    umount "$OLD"
  fi
  cp -a /etc/fstab "/etc/fstab.bak-$(date +%Y%m%d%H%M%S)"
  sed -i -E "s#[[:space:]]$OLD[[:space:]]+ext4[[:space:]]+defaults,noatime,nofail,x-systemd.requires-mounts-for=/srv/nas#\t$NEW ext4 defaults,noatime,nofail,x-systemd.device-timeout=15s#" /etc/fstab
  grep -qE "[[:space:]]$NEW[[:space:]]" /etc/fstab || { echo "fstab edit failed; restore the .bak" >&2; exit 1; }
  # The old mount point lives on the Nuvem disk (immutable while empty).
  chattr -i "$OLD" 2>/dev/null || true
  rmdir "$OLD" 2>/dev/null || true
fi

if ! mountpoint -q "$NEW"; then
  install -d -m 0755 "$NEW"
  chattr +i "$NEW" 2>/dev/null || true # nothing lands on / if the disk is missing
  systemctl daemon-reload
  mount "$NEW"
fi
chown root:root "$NEW" && chmod 0755 "$NEW"
install -d -m 0700 "$NEW/db" "$NEW/etc"

DEBIAN_FRONTEND=noninteractive apt-get -o DPkg::Lock::Timeout=600 install -y -qq sqlite3 restic >/dev/null
install -d -m 0755 /var/lib/sentinela
install -m 0644 "$HERE/deploy/host/sentinela-backup.service" "$HERE/deploy/host/sentinela-backup.timer" /etc/systemd/system/
systemctl daemon-reload
systemctl enable -q --now sentinela-backup.timer
echo "Backups on: $(systemctl list-timers sentinela-backup.timer --no-pager | sed -n 2p)"
echo "Run one now: sudo systemctl start sentinela-backup.service"
