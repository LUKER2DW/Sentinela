#!/usr/bin/env bash
# Remote access trial P3 — Tailscale Funnel (ADR-0019). Run on the server:
#   sudo /opt/sentinela/scripts/setup-funnel.sh enable|disable|status
# Publishes https://sentinela.<tailnet>.ts.net:10000 on the internet through
# Tailscale, proxied to remote.funnel_listen (127.0.0.1:8081), a listener in
# the public zone (two-step login, closed door). Port 443 stays tailnet-only.
# Needs, once, in the admin console ACL:
#   "nodeAttrs": [{"target": ["tag:nvr"], "attr": ["funnel"]}]
# and `remote: {funnel_listen: 127.0.0.1:8081}` in /etc/sentinela/config.yaml.
set -euo pipefail
[[ $EUID -eq 0 ]] || { echo "run with sudo" >&2; exit 1; }
PORT=10000
TARGET=http://127.0.0.1:8081

case "${1:-status}" in
  enable)
    ss -tln | grep -q '127.0.0.1:8081 ' || echo "!! nothing listens on 127.0.0.1:8081 yet: set remote.funnel_listen and redeploy" >&2
    if ! tailscale funnel --bg --https=$PORT "$TARGET"; then
      echo "!! Funnel refused: add the funnel nodeAttr for tag:nvr in the ACL, then re-run" >&2
      exit 1
    fi
    ;;
  disable)
    tailscale funnel --https=$PORT off || true
    ;;
  status) ;;
  *) echo "usage: $0 enable|disable|status" >&2; exit 2 ;;
esac

name="$(tailscale status --json | python3 -c 'import json,sys; print(json.load(sys.stdin)["Self"]["DNSName"].rstrip("."))')"
tailscale funnel status
echo "Funnel URL (when on): https://${name}:${PORT}"
