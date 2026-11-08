#!/usr/bin/env bash
# Cloudflare Tunnel (ADR-0025): installs the host units that let the
# dashboard (System -> Remote access) install cloudflared, run the tunnel and
# show its status. Run by install-server.sh and deploy.sh; idempotent:
#   sudo /opt/sentinela/scripts/setup-cloudflared.sh units
# Nothing is installed from Cloudflare and no tunnel runs until the owner
# pastes a tunnel token in the dashboard.
set -euo pipefail
[[ $EUID -eq 0 ]] || { echo "run with sudo" >&2; exit 1; }
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
case "${1:-units}" in
  units)
    install -m 0644 "$HERE"/deploy/host/sentinela-cloudflared.service "$HERE"/deploy/host/sentinela-cloudflared-agent.service \
      "$HERE"/deploy/host/sentinela-cloudflared-agent.path "$HERE"/deploy/host/sentinela-cloudflared-agent.timer /etc/systemd/system/
    install -d -o 1000 -g 1000 -m 0700 "${SENTINELA_DATA:-/data}/cloudflared"
    install -d -m 0755 /var/lib/sentinela
    touch /var/lib/sentinela/cloudflared-units   # tells the dashboard the agent exists
    systemctl daemon-reload
    systemctl enable -q --now sentinela-cloudflared-agent.path sentinela-cloudflared-agent.timer
    systemctl start --no-block sentinela-cloudflared-agent.service || true   # first status
    ;;
  *) echo "usage: $0 units" >&2; exit 2 ;;
esac
