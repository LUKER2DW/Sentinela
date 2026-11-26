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
# shellcheck source=scripts/lib/safe.sh
source "$DIR/scripts/lib/safe.sh"

install_units() {
  install -m 0644 "$DIR"/deploy/host/sentinela-update.service "$DIR"/deploy/host/sentinela-update.timer \
    "$DIR"/deploy/host/sentinela-update-request.service "$DIR"/deploy/host/sentinela-update-request.path \
    /etc/systemd/system/
  rm -rf /etc/systemd/system/sentinela-update.timer.d   # old per-hour drop-in
  # 1.2.2's safety-net timer: the path unit now uses PathExists instead.
  if [[ -f /etc/systemd/system/sentinela-update-request.timer ]]; then
    systemctl disable -q --now sentinela-update-request.timer || true
    rm -f /etc/systemd/system/sentinela-update-request.timer
  fi
  safe_install_d "$DATA" 0755 "$UPDATES"
  install -d -m 0755 "$STATE"
  touch "$STATE/update-units"   # tells the dashboard the units exist
  systemctl daemon-reload
  systemctl enable -q --now sentinela-update.timer sentinela-update-request.path
  systemctl restart sentinela-update-request.path   # picks up PathExists
}

settings() { # settings ENABLED AT (keeps the other preferences)
  # <data>/updates belongs to uid 1000: settings.json is read and replaced
  # only through safe.sh (no link followed, plain file, owned 1000:1000).
  local doc
  doc="$({ safe_read "$DATA" "$UPDATES/settings.json" 4096 2>/dev/null || true; } |
    python3 -I -c '
import json, sys
enabled, at = sys.argv[1] == "true", sys.argv[2]
try:
    s = json.loads(sys.stdin.read())
except Exception:
    s = {}
if not isinstance(s, dict):
    s = {}
s["enabled"] = enabled
if at:
    s["at"] = at
s.setdefault("at", "04:00")
json.dump(s, sys.stdout)
' "$1" "$2")" || return 1
  printf '%s' "$doc" | safe_write "$DATA" "$UPDATES/settings.json" 0644
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
    safe_read "$DATA" "$UPDATES/settings.json" 4096 2>/dev/null || echo "no settings (off)"
    echo
    systemctl list-timers sentinela-update.timer --no-pager 2>/dev/null | head -2 || true
    cat "$STATE/update.json" 2>/dev/null || true
    ;;
  *) sed -n '2,15p' "$0"; exit 2 ;;
esac
