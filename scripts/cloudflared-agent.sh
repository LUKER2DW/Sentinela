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
# The tunnel runs as sentinela-cloudflared.service (its own system user,
# read-only system). Its routes are set in the Cloudflare account: the public
# hostname must point at remote.proxy.listen, where Sentinela treats every
# request as coming from the internet. The unit confines the connector to
# this host's loopback and the IPv4 internet (no LAN, cameras, router or
# tailnet), and on loopback an nftables table lets it reach that listener
# alone (scripts/lib/cloudflared.sh). This agent keeps that table and a
# drop-in that also allows the host's own name servers.
set -euo pipefail
[[ $EUID -eq 0 ]] || { echo "run with sudo" >&2; exit 1; }
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/lib/safe.sh
source "$DIR/scripts/lib/safe.sh"
# shellcheck source=scripts/lib/cloudflared.sh
source "$DIR/scripts/lib/cloudflared.sh"
DATA="${SENTINELA_DATA:-/data}"
REQ="$DATA/cloudflared"
STATE=/var/lib/sentinela
OUT="$STATE/cloudflared.json"
ENVF="$CF_ENV"
UNIT="$CF_UNIT"

exec 9>"$CF_LOCK"
flock -w 120 9 || exit 0
install -d -m 0755 "$STATE"

# $REQ belongs to uid 1000: the request and the token are taken (read,
# then removed) through safe.sh, which follows no link and reads only a
# small plain file.
action=status
if req="$(safe_take "$DATA" "$REQ/request" 256)"; then
  read -r action _ <<< "$req" || true
fi
token="$(safe_take "$DATA" "$REQ/token" 8192 | tr -d '[:space:]')" || token=""
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

if [[ "$action" != disconnect && -f "$ENVF" ]]; then
  # The loopback table follows the settings (the proxy port) and comes back
  # if something flushed it.
  cf_firewall_kept || echo "warning: the tunnel's loopback firewall could not be loaded" >&2
  # A running tunnel follows the host's name servers (a DHCP change).
  if cf_dns_dropin; then
    systemctl try-restart "$UNIT" || true
  fi
fi

case "$action" in
  connect)
    if [[ ! "$token" =~ ^eyJ[A-Za-z0-9+/=_-]{50,4000}$ ]]; then
      message="bad_token"
    elif ! install_cloudflared || ! cf_firewall; then
      # cloudflared, or nftables for its loopback table, could not be installed.
      message="install_failed"
    else
      umask 077
      install -d -m 0755 /etc/sentinela
      printf 'TUNNEL_TOKEN=%s\n' "$token" > "$ENVF.tmp"
      chmod 0600 "$ENVF.tmp" && mv -f "$ENVF.tmp" "$ENVF"
      cf_dns_dropin || true
      systemctl daemon-reload
      systemctl enable -q "$UNIT"
      # A start that fails (its loopback table did not load) is reported
      # below as no connection, not left as "starting".
      systemctl restart "$UNIT" || true
      message="starting"
      status true
      # Connected when Cloudflare's edge accepts the tunnel (a few seconds).
      message="no_connection"
      cf_wait_ready 40 && message="ok"
    fi
    ;;
  disconnect)
    systemctl disable -q --now "$UNIT" 2>/dev/null || true
    rm -f "$ENVF" "$CF_DNS_DROPIN"
    cf_firewall_remove
    systemctl daemon-reload
    message="disconnected"
    ;;
  *) action=status ;;
esac
token=""
status false
