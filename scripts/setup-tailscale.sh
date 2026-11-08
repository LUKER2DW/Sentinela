#!/usr/bin/env bash
# Tailscale remote access (ADR-0004). Idempotent. Run on the server:
#   sudo /opt/sentinela/scripts/setup-tailscale.sh
# 1. Installs tailscale from the official apt repository.
# 2. Joins the tailnet as "sentinela" (prints a login URL the first time).
#    No Tailscale SSH, no subnet routes, no exit node, host DNS untouched.
# 3. Publishes the web UI: https://sentinela.<tailnet>.ts.net -> 127.0.0.1:8080.
#    Needs HTTPS enabled once in the admin console (DNS -> HTTPS Certificates).
# The tailnet ACL and key-expiry settings are done in the admin console
# (docs/SECURITY.md rule 9). Nothing here opens a port on the router.
set -euo pipefail
[[ $EUID -eq 0 ]] || { echo "run with sudo" >&2; exit 1; }
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# `setup-tailscale.sh units` (install-server.sh, deploy.sh): only the host
# units that let the dashboard set Tailscale up and show its status
# (scripts/tailscale-agent.sh). Installs nothing else and joins nothing.
install_units() {
  install -m 0644 "$HERE"/deploy/host/sentinela-tailscale.service "$HERE"/deploy/host/sentinela-tailscale.path \
    "$HERE"/deploy/host/sentinela-tailscale.timer /etc/systemd/system/
  install -d -o 1000 -g 1000 -m 0700 "${SENTINELA_DATA:-/data}/tailscale"
  install -d -m 0755 /var/lib/sentinela
  touch /var/lib/sentinela/tailscale-units   # tells the dashboard the agent exists
  systemctl daemon-reload
  systemctl enable -q --now sentinela-tailscale.path sentinela-tailscale.timer
  systemctl start --no-block sentinela-tailscale.service || true   # first status
}
if [[ "${1:-}" == units ]]; then
  install_units
  exit 0
fi

if ! command -v tailscale >/dev/null; then
  . /etc/os-release
  base="https://pkgs.tailscale.com/stable/${ID}/${VERSION_CODENAME}"
  curl -fsSL "${base}.noarmor.gpg" -o /usr/share/keyrings/tailscale-archive-keyring.gpg
  curl -fsSL "${base}.tailscale-keyring.list" -o /etc/apt/sources.list.d/tailscale.list
  apt-get -o DPkg::Lock::Timeout=600 update -qq
  DEBIAN_FRONTEND=noninteractive apt-get -o DPkg::Lock::Timeout=600 install -y -qq tailscale >/dev/null
fi
systemctl enable -q --now tailscaled

# Firewall first: only 443/8555/22 on tailscale0 (scripts/firewall.sh).
"$HERE/scripts/firewall.sh" >/dev/null

state="$(tailscale status --json 2>/dev/null | python3 -c 'import json,sys; print(json.load(sys.stdin).get("BackendState",""))' || true)"
if [[ "$state" != "Running" ]]; then
  echo "Open the login URL below and approve the server (then this script continues)."
  tailscale up --hostname=sentinela --ssh=false --accept-dns=false --accept-routes=false \
    --advertise-routes= --advertise-exit-node=false
fi
# Tag the server so the ACL (docs/SECURITY.md rule 9) applies to it; needs
# the ACL's tagOwners saved first. Re-asserts the other settings too.
if ! tailscale up --advertise-tags=tag:nvr --hostname=sentinela --ssh=false --accept-dns=false \
    --accept-routes=false --advertise-routes= --advertise-exit-node=false --timeout=60s; then
  echo "!! tag:nvr refused: paste the ACL from docs/SECURITY.md in the admin console, then re-run" >&2
  exit 1
fi

# HTTPS for the UI. serve renews the certificate by itself.
if ! tailscale serve --bg --https=443 http://127.0.0.1:8080; then
  echo "!! enable HTTPS certificates in the admin console (DNS page), then re-run" >&2
  exit 1
fi

name="$(tailscale status --json | python3 -c 'import json,sys; print(json.load(sys.stdin)["Self"]["DNSName"].rstrip("."))')"
echo "Tailscale IPv4: $(tailscale ip -4)"
echo "Web UI:         https://${name}"
tailscale serve status
install_units   # the dashboard shows this status from now on
