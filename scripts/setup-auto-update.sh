#!/usr/bin/env bash
# Turn automatic updates from the project's releases on or off (ADR-0023).
#
#   sudo scripts/setup-auto-update.sh enable [HH:MM]   # daily, default 04:00
#   sudo scripts/setup-auto-update.sh disable
#   sudo scripts/setup-auto-update.sh status
#   sudo scripts/setup-auto-update.sh now              # check and update right away
#
# Options live in /etc/sentinela/auto-update.env:
#   SENTINELA_UPDATE_REPO=LUKER2DW/Sentinela   where releases come from
#   SENTINELA_UPDATE_MANUAL=keep|replace        replace a build deployed from a computer?
#   SENTINELA_UPDATE_MAJOR=no|yes               also install new major versions?
set -euo pipefail
[[ $EUID -eq 0 ]] || { echo "run with sudo" >&2; exit 1; }
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE=/etc/sentinela/auto-update.env
DROPIN=/etc/systemd/system/sentinela-update.timer.d

# What the System page shows (read-only mount /var/lib/sentinela -> /host).
schedule() { # schedule ENABLED AT
  install -d -m 0755 /var/lib/sentinela
  repo="$(sed -n 's/^SENTINELA_UPDATE_REPO=//p' "$ENV_FILE" 2>/dev/null)"
  printf '{"enabled":%s,"at":"%s","repo":"%s"}\n' "$1" "$2" "${repo:-LUKER2DW/Sentinela}" > /var/lib/sentinela/update-schedule.json
  chmod 0644 /var/lib/sentinela/update-schedule.json
}

set_var() { # set_var KEY VALUE
  touch "$ENV_FILE" && chmod 0644 "$ENV_FILE"
  if grep -q "^$1=" "$ENV_FILE"; then sed -i "s|^$1=.*|$1=$2|" "$ENV_FILE"; else echo "$1=$2" >> "$ENV_FILE"; fi
}

case "${1:-status}" in
  enable)
    at="${2:-04:00}"
    [[ "$at" =~ ^([01][0-9]|2[0-3]):[0-5][0-9]$ ]] || { echo "time must be HH:MM (24 h)" >&2; exit 1; }
    install -m 0644 "$DIR/deploy/host/sentinela-update.service" "$DIR/deploy/host/sentinela-update.timer" /etc/systemd/system/
    install -d "$DROPIN"
    printf '[Timer]\nOnCalendar=\nOnCalendar=*-*-* %s:00\n' "$at" > "$DROPIN/hour.conf"
    set_var SENTINELA_UPDATE_ENABLED yes
    grep -q '^SENTINELA_UPDATE_REPO=' "$ENV_FILE" || set_var SENTINELA_UPDATE_REPO LUKER2DW/Sentinela
    systemctl daemon-reload
    systemctl enable -q --now sentinela-update.timer
    schedule true "$at"
    echo "automatic updates on: every day around $at (server time $(timedatectl show -p Timezone --value 2>/dev/null || date +%Z))"
    ;;
  disable)
    set_var SENTINELA_UPDATE_ENABLED no
    systemctl disable -q --now sentinela-update.timer 2>/dev/null || true
    schedule false ""
    echo "automatic updates off"
    ;;
  now)
    exec "$DIR/scripts/auto-update.sh" --now
    ;;
  status)
    [[ -f "$ENV_FILE" ]] && cat "$ENV_FILE" || echo "no $ENV_FILE (off)"
    systemctl list-timers sentinela-update.timer --no-pager 2>/dev/null | head -2 || true
    [[ -f /var/lib/sentinela/update.json ]] && cat /var/lib/sentinela/update.json
    ;;
  *) sed -n '2,13p' "$0"; exit 2 ;;
esac
