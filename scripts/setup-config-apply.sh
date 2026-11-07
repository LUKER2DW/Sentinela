#!/usr/bin/env bash
# Install the host units that apply settings changed in the dashboard
# (System; ADR-0024). Run by install-server.sh and deploy.sh; idempotent.
set -euo pipefail
[[ $EUID -eq 0 ]] || { echo "run with sudo" >&2; exit 1; }
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DATA="${SENTINELA_DATA:-/data}"
install -m 0644 "$DIR/deploy/host/sentinela-config.service" "$DIR/deploy/host/sentinela-config.path" /etc/systemd/system/
install -d -o 1000 -g 1000 -m 0700 "$DATA/config"
install -d -m 0755 /var/lib/sentinela
touch /var/lib/sentinela/config-units   # tells the dashboard it can apply settings
systemctl daemon-reload
systemctl enable -q --now sentinela-config.path
