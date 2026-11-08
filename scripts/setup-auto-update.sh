#!/usr/bin/env bash
# Automatic updates from the project's releases (ADR-0023). The normal way
# is the dashboard (System -> Atualizações); this script does the same from
# a shell and installs the host units the dashboard relies on.
#
#   sudo scripts/setup-auto-update.sh install          # units only (installer/deploy)
#   sudo scripts/setup-auto-update.sh enable [HH:MM]   # daily, default 04:00
#   sudo scripts/setup-auto-update.sh disable
#   sudo scripts/setup-auto-update.sh status
#   sudo scripts/setup-auto-update.sh now              # check and update right away
#
# Where releases come from (root only): SENTINELA_UPDATE_REPO in
# /etc/sentinela/auto-update.env (default LUKER2DW/Sentinela).
set -euo pipefail
[[ $EUID -eq 0 ]] || { echo "run with sudo" >&2; exit 1; }
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DATA="${SENTINELA_DATA:-/data}"
UPDATES="$DATA/updates"
STATE=/var/lib/sentinela

install_units() {
  install -m 0644 "$DIR"/deploy/host/sentinela-update.service "$DIR"/deploy/host/sentinela-update.timer \
    "$DIR"/deploy/host/sentinela-update-request.service "$DIR"/deploy/host/sentinela-update-request.path \
    "$DIR"/deploy/host/sentinela-update-request.timer /etc/systemd/system/
  rm -rf /etc/systemd/system/sentinela-update.timer.d   # old per-hour drop-in
  install -d -o 1000 -g 1000 -m 0755 "$UPDATES"
  install -d -m 0755 "$STATE"
  touch "$STATE/update-units"   # tells the dashboard the units exist
  systemctl daemon-reload
  systemctl enable -q --now sentinela-update.timer sentinela-update-request.path sentinela-update-request.timer
}

settings() { # settings ENABLED AT (keeps the other preferences)
  python3 - "$UPDATES/settings.json" "$1" "$2" <<'PY'
import json, sys
p, enabled, at = sys.argv[1], sys.argv[2] == "true", sys.argv[3]
try:
    s = json.load(open(p))
except Exception:
    s = {}
s["enabled"] = enabled
if at:
    s["at"] = at
s.setdefault("at", "04:00")
json.dump(s, open(p, "w"))
PY
  chown 1000:1000 "$UPDATES/settings.json"
  chmod 0644 "$UPDATES/settings.json"
}

case "${1:-status}" in
  install)
    install_units
    ;;
  enable)
    at="${2:-04:00}"
    [[ "$at" =~ ^([01][0-9]|2[0-3]):[0-5][0-9]$ ]] || { echo "time must be HH:MM (24 h)" >&2; exit 1; }
    install_units
    settings true "$at"
    echo "automatic updates on: every day around $at (server time $(timedatectl show -p Timezone --value 2>/dev/null || date +%Z))"
    ;;
  disable)
    install_units
    settings false ""
    echo "automatic updates off"
    ;;
  now)
    exec "$DIR/scripts/auto-update.sh" --now
    ;;
  status)
    cat "$UPDATES/settings.json" 2>/dev/null || echo "no settings (off)"
    echo
    systemctl list-timers sentinela-update.timer --no-pager 2>/dev/null | head -2 || true
    cat "$STATE/update.json" 2>/dev/null || true
    ;;
  *) sed -n '2,15p' "$0"; exit 2 ;;
esac
