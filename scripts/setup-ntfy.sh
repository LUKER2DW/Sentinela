#!/usr/bin/env bash
# Notifications in the Android app (ADR-0013): publish the ntfy relay on the
# tailnet and point Sentinela at it. Idempotent. Run on the server after a
# deploy that includes the ntfy service:
#   sudo /opt/sentinela/scripts/setup-ntfy.sh
# Needs scripts/setup-tailscale.sh done first. The tailnet ACL must allow
# port 8443 to tag:nvr (docs/SECURITY.md rule 9). Nothing opens on the router.
set -euo pipefail
[[ $EUID -eq 0 ]] || { echo "run with sudo" >&2; exit 1; }
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONFIG=/etc/sentinela/config.yaml

# Message cache, owned by the container user (docker would create it as root).
install -d -o 1000 -g 1000 -m 0750 /data/ntfy

"$HERE/scripts/firewall.sh" >/dev/null
tailscale serve --bg --https=8443 http://127.0.0.1:2586

name="$(tailscale status --json | python3 -c 'import json,sys; print(json.load(sys.stdin)["Self"]["DNSName"].rstrip("."))')"
url="https://${name}:8443"
if ! grep -q '^ntfy:' "$CONFIG"; then
  cp -p "$CONFIG" "$CONFIG.bak-$(date +%Y%m%d%H%M%S)"
  printf '\n# Android app notifications (ADR-0013), added by setup-ntfy.sh\nntfy:\n  public_url: %s\n' "$url" >>"$CONFIG"
elif ! grep -q "public_url: ${url}\$" "$CONFIG"; then
  echo "!! $CONFIG has an ntfy block: check that public_url is ${url}" >&2
fi

cd "$HERE/deploy"
docker compose up -d ntfy
docker compose restart sentinela >/dev/null
for _ in $(seq 1 15); do
  curl -fsS -o /dev/null http://127.0.0.1:2586/v1/health && break
  sleep 1
done
curl -fsS http://127.0.0.1:2586/v1/health && echo
echo "ntfy relay: ${url}"
echo "On the phone: install ntfy, Settings -> Default server = ${url}"
tailscale serve status
