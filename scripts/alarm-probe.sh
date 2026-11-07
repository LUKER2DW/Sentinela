#!/usr/bin/env bash
# Long-running, read-only DVRIP alarm probe (PLAN Phase 9, Wave 3). Runs as a
# transient systemd unit, so it survives SSH logout and deploys, and writes
# every alarm push the camera sends to a log file.
#
#   sudo /opt/sentinela/scripts/alarm-probe.sh start [hours]   # default 72
#   sudo /opt/sentinela/scripts/alarm-probe.sh status          # running? + alarms seen
#   sudo /opt/sentinela/scripts/alarm-probe.sh stop
#
# The probe binary is built on the dev PC (the server has no Go):
#   GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c -tags hardware \
#     -o dvrip-probe ./internal/camera/dvrip
#   scp dvrip-probe <server>:/tmp/ && ssh <server> sudo install -D -m 0755 /tmp/dvrip-probe /opt/sentinela/probe/dvrip-probe
set -euo pipefail

BIN=/opt/sentinela/probe/dvrip-probe
LOGDIR=/var/log/sentinela
UNIT=sentinela-alarm-probe
[[ $EUID -eq 0 ]] || { echo "run with sudo" >&2; exit 1; }

case "${1:-status}" in
  start)
    hours="${2:-72}"
    [[ "$hours" =~ ^[0-9]+$ ]] || { echo "hours must be a number" >&2; exit 1; }
    [[ -x "$BIN" ]] || { echo "missing $BIN (see the header for how to build it)" >&2; exit 1; }
    if systemctl is-active --quiet "$UNIT"; then echo "already running; use status or stop" >&2; exit 1; fi
    systemctl reset-failed "$UNIT" 2>/dev/null || true
    install -d -m 0750 "$LOGDIR"
    log="$LOGDIR/alarm-probe-$(date -u +%Y%m%dT%H%MZ).log"
    systemd-run --unit="$UNIT" --collect --property=EnvironmentFile=/etc/sentinela/secrets.env \
      --setenv=SENTINELA_PROBE_SECONDS=$((hours * 3600)) --property=MemoryMax=64M \
      /bin/sh -c "exec $BIN -test.run TestAlarmProbe -test.v -test.timeout 0 > $log 2>&1"
    echo "started for ${hours} h, log: $log"
    ;;
  status)
    if systemctl is-active --quiet "$UNIT"; then echo "running"; else echo "not running (finished or stopped)"; fi
    for f in "$LOGDIR"/alarm-probe-*.log; do
      [[ -e "$f" ]] || { echo "no probe logs in $LOGDIR"; break; }
      n="$(grep -c ' msg=' "$f" || true)"
      echo "$f: $n packet(s)"
      grep ' msg=' "$f" | tail -5
      grep -E '^(--- (PASS|FAIL)|FAIL|ok|PASS)' "$f" | tail -1
    done
    ;;
  stop)
    systemctl stop "$UNIT" && echo stopped
    ;;
  *)
    sed -n '2,14p' "$0"; exit 1 ;;
esac
