#!/usr/bin/env bash
# Remote access trial P1 — port-forward + domain (ADR-0019). Run on the server:
#   sudo /opt/sentinela/scripts/setup-public.sh enable|disable|status
# enable: opens 7443/tcp and 8555/tcp+udp (plus 7080/tcp with HTTP-01) to any source in ufw (marker
#   /etc/sentinela/public-access, kept by scripts/firewall.sh).
# The owner does the rest: remote.public.domain in /etc/sentinela/config.yaml
# (then redeploy) — a domain with an A record pointing at home, or the fixed
# public IP itself (6-day IP certificate, internal/publiccert) — and on the
# router WAN 443/tcp -> <server>:7443 and WAN 8555/tcp+udp -> <server>:8555.
# If the ISP blocks inbound 443: remote.public.http01_listen: ":7080",
# router WAN 80/tcp -> <server>:7080 (certificate) and another WAN port,
# e.g. 8443/tcp -> <server>:7443, for the site. If 80 is blocked too:
# remote.public.self_signed: true (no Let's Encrypt; the app pins the
# certificate, whose SHA-256 "status" prints; browsers warn).
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
    echo "ufw: 7443/tcp and 8555/tcp+udp open to the internet (7080/tcp too with HTTP-01)."
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
if ss -tln | grep -q ':7080 '; then echo "listener :7080 (HTTP-01): up"; else echo "listener :7080 (HTTP-01): off"; fi
SELF=/data/acme/self/cert.pem
if grep -q '^[[:space:]]*self_signed:[[:space:]]*true' "$CFG" && [[ -f "$SELF" ]]; then
  fp="$(openssl x509 -in "$SELF" -noout -fingerprint -sha256 | cut -d= -f2 | tr -d ':' | tr 'A-F' 'a-f')"
  echo "self-signed certificate SHA-256 (SENTINELA_PUBLIC_CERT_SHA256 for build-apk.sh): $fp"
fi
# CGNAT check: the public IP seen from outside must be the router's WAN IP.
pub="$(curl -fsS --max-time 5 https://api.ipify.org || true)"
echo "public IP seen from outside: ${pub:-unknown} (compare with the router's WAN IP; different = CGNAT)"
if [[ -n "$domain" && ! "$domain" =~ ^[0-9.]+$ && "$domain" != *:* ]]; then
  echo "DNS $domain -> $(getent ahostsv4 "$domain" | awk 'NR==1{print $1}')"
fi
