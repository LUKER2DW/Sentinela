#!/usr/bin/env bash
# Cloudflare Tunnel from the dashboard (ADR-0025). Run as root by
# sentinela-cloudflared-agent.path when the dashboard leaves a request in
# $DATA/cloudflared, and by its timer to refresh the status the dashboard
# shows (/var/lib/sentinela/cloudflared.json).
#
# Requests (first word of $DATA/cloudflared/request):
#   status      only refresh the status
#   connect     install cloudflared if missing (Cloudflare's apt repository)
#               and run the tunnel whose token the person pasted
#               ($DATA/cloudflared/token: moved to /etc/sentinela/cloudflared.env,
#               root only, read by systemd; never logged or on a command line)
#   disconnect  stop the tunnel and forget the token
#
# The tunnel runs as sentinela-cloudflared.service (no root, read-only
# system). It only reaches the loopback port set in remote.proxy.listen,
# where Sentinela treats every request as coming from the internet.
set -euo pipefail
[[ $EUID -eq 0 ]] || { echo "run with sudo" >&2; exit 1; }
DATA="${SENTINELA_DATA:-/data}"
REQ="$DATA/cloudflared"
STATE=/var/lib/sentinela
OUT="$STATE/cloudflared.json"
ENVF=/etc/sentinela/cloudflared.env
UNIT=sentinela-cloudflared.service

exec 9>/run/sentinela-cloudflared.lock
flock -w 120 9 || exit 0
install -d -m 0755 "$STATE"

action=status
if [[ -f "$REQ/request" ]]; then
  read -r action _ < "$REQ/request" || true
  rm -f "$REQ/request"
fi
token=""
if [[ -f "$REQ/token" ]]; then
  token="$(head -c 8192 "$REQ/token" | tr -d '[:space:]')"
  rm -f "$REQ/token"
fi
message=""

status() { # status [BUSY]
  local installed=false version="" active ready="{}"
  if command -v cloudflared >/dev/null; then
    installed=true
    version="$(cloudflared --version 2>/dev/null | head -1 | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1 || true)"
  fi
  active="$(systemctl is-active "$UNIT" 2>/dev/null || true)"
  if [[ "$active" == active ]]; then
    ready="$(curl -fsS --max-time 3 http://127.0.0.1:20241/ready 2>/dev/null || echo '{}')"
  fi
  READY="$ready" python3 - "$OUT" "$installed" "$version" "$active" "$([[ -f $ENVF ]] && echo true || echo false)" "${1:-false}" "$action" "$message" <<'PY'
import json, os, sys, datetime
out, installed, version, active, configured, busy, action, message = sys.argv[1:9]
try:
    ready = json.loads(os.environ.get("READY") or "{}") or {}
except Exception:
    ready = {}
doc = {
    "checked": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
    "installed": installed == "true",
    "version": version,
    "configured": configured == "true",
    "running": active == "active",
    "connections": int(ready.get("readyConnections") or 0),
    "busy": busy == "true",
    "action": action,
    "message": message,
}
if action == "status":  # a refresh keeps the last action's result
    try:
        old = json.load(open(out))
        doc["action"], doc["message"] = old.get("action", ""), old.get("message", "")
    except Exception:
        pass
tmp = out + ".tmp"
with open(tmp, "w") as f:
    json.dump(doc, f)
os.chmod(tmp, 0o644)
os.replace(tmp, out)
PY
}

install_cloudflared() {
  command -v cloudflared >/dev/null && return 0
  message="installing cloudflared"
  status true
  install -d -m 0755 /usr/share/keyrings
  curl -fsSL --max-time 60 https://pkg.cloudflare.com/cloudflare-main.gpg -o /usr/share/keyrings/cloudflare-main.gpg &&
    echo "deb [signed-by=/usr/share/keyrings/cloudflare-main.gpg] https://pkg.cloudflare.com/cloudflared any main" \
      > /etc/apt/sources.list.d/cloudflared.list &&
    apt-get -o DPkg::Lock::Timeout=600 update -qq &&
    DEBIAN_FRONTEND=noninteractive apt-get -o DPkg::Lock::Timeout=600 install -y -qq cloudflared >/dev/null
}

case "$action" in
  connect)
    if [[ ! "$token" =~ ^eyJ[A-Za-z0-9+/=_-]{50,4000}$ ]]; then
      message="bad_token"
    elif ! install_cloudflared; then
      message="install_failed"
    else
      umask 077
      install -d -m 0755 /etc/sentinela
      printf 'TUNNEL_TOKEN=%s\n' "$token" > "$ENVF.tmp"
      chmod 0600 "$ENVF.tmp" && mv -f "$ENVF.tmp" "$ENVF"
      systemctl daemon-reload
      systemctl enable -q "$UNIT"
      systemctl restart "$UNIT"
      message="starting"
      status true
      # Connected when Cloudflare's edge accepts the tunnel (a few seconds).
      message="no_connection"
      for _ in $(seq 1 20); do
        if curl -fsS --max-time 3 http://127.0.0.1:20241/ready 2>/dev/null | grep -q '"readyConnections":[1-9]'; then
          message="ok"
          break
        fi
        sleep 2
      done
    fi
    ;;
  disconnect)
    systemctl disable -q --now "$UNIT" 2>/dev/null || true
    rm -f "$ENVF"
    message="disconnected"
    ;;
  *) action=status ;;
esac
token=""
status false
