#!/usr/bin/env bash
# Remote access trial P1 — port-forward + domain (ADR-0019). Run on the server:
#   sudo /opt/sentinela/scripts/setup-public.sh enable|disable|status
# enable: opens 7443/tcp and 8555/tcp+udp to any source in ufw (marker
#   /etc/sentinela/public-access, kept by scripts/firewall.sh).
# The owner does the rest: remote.public.domain in /etc/sentinela/config.yaml
# (then redeploy), an A record for that domain pointing at home, and on the
# router WAN 443/tcp -> <server>:7443 and WAN 8555/tcp+udp -> <server>:8555.
# Nobody can log in from outside before enrolling two-step login at home.
set -euo pipefail
[[ $EUID -eq 0 ]] || { echo "run with sudo" >&2; exit 1; }
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MARK=/etc/sentinela/public-access
CFG=/etc/sentinela/config.yaml

case "${1:-status}" in
  enable)
    touch "$MARK"
    "$HERE/scripts/firewall.sh" >/dev/null
    echo "ufw: 7443/tcp and 8555/tcp+udp open to the internet."
    ;;
  disable)
    rm -f "$MARK"
    "$HERE/scripts/firewall.sh" >/dev/null
    echo "ufw: public ports closed. Remove the router forwards too."
    ;;
  status) ;;
  *) echo "usage: $0 enable|disable|status" >&2; exit 2 ;;
esac

[[ -f "$MARK" ]] && echo "firewall: open" || echo "firewall: closed"
domain="$(sed -n 's/^[[:space:]]*domain:[[:space:]]*\([^#[:space:]]*\).*/\1/p' "$CFG" | head -1)"
echo "config remote.public.domain: ${domain:-<not set>}"
if ss -tln | grep -q ':7443 '; then echo "listener :7443: up"; else echo "listener :7443: down (set the domain and redeploy)"; fi
# CGNAT check: the public IP seen from outside must be the router's WAN IP.
pub="$(curl -fsS --max-time 5 https://api.ipify.org || true)"
echo "public IP seen from outside: ${pub:-unknown} (compare with the router's WAN IP; different = CGNAT)"
if [[ -n "$domain" ]]; then
  echo "DNS $domain -> $(getent ahostsv4 "$domain" | awk 'NR==1{print $1}')"
fi
