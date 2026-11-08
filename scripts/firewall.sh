#!/usr/bin/env bash
# Host firewall for the Sentinela server (docs/ARCHITECTURE.md §4). Idempotent.
# Default deny inbound; LAN and tailnet allowances. Nothing is exposed to the
# WAN, except the ADR-0019 P1 trial ports while /etc/sentinela/public-access exists.
#   sudo ./scripts/firewall.sh [LAN_CIDR]   (default: the default route's network)
set -euo pipefail
[[ $EUID -eq 0 ]] || { echo "run with sudo" >&2; exit 1; }
# shellcheck source=scripts/lib/lan.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib/lan.sh"
LAN="${1:-$(lan_cidr)}"
[[ -n "$LAN" ]] || { echo "could not find the LAN range; pass it: $0 192.168.1.0/24" >&2; exit 1; }
command -v ufw >/dev/null || DEBIAN_FRONTEND=noninteractive apt-get -o DPkg::Lock::Timeout=600 install -y -qq ufw

ufw --force reset >/dev/null
ufw default deny incoming >/dev/null
ufw default allow outgoing >/dev/null
# SSH first, so enabling never locks us out.
ufw allow from "$LAN" to any port 22 proto tcp comment 'ssh lan' >/dev/null
ufw allow from "$LAN" to any port 8080 proto tcp comment 'sentinela web' >/dev/null
ufw allow from "$LAN" to any port 8555 proto tcp comment 'webrtc' >/dev/null
ufw allow from "$LAN" to any port 8555 proto udp comment 'webrtc' >/dev/null
ufw allow from "$LAN" to any port 445 proto tcp comment 'smb (nuvem)' >/dev/null
ufw allow from "$LAN" to any port 139 proto tcp comment 'netbios' >/dev/null
ufw allow from "$LAN" to any port 137,138 proto udp comment 'netbios' >/dev/null
ufw allow from "$LAN" to any port 3702 proto udp comment 'ws-discovery' >/dev/null
ufw allow from "$LAN" to any port 5357 proto tcp comment 'ws-discovery' >/dev/null
ufw allow from "$LAN" to any port 5353 proto udp comment 'mdns' >/dev/null
ufw allow from "$LAN" to any port 123 proto udp comment 'ntp for the camera' >/dev/null
# Tailscale (ADR-0004, SECURITY.md rules 8-9): explicit ports only, never the
# whole interface. 443 = `tailscale serve` HTTPS (proxies to 127.0.0.1:8080),
# 8555 = WebRTC ICE. 8080 stays LAN-only: remote users always get HTTPS.
# The rules match the interface name, so they are safe to add before
# Tailscale is installed. The tailnet ACL is the primary control.
ufw allow in on tailscale0 to any port 443 proto tcp comment 'tailnet https (serve)' >/dev/null
ufw allow in on tailscale0 to any port 8555 proto tcp comment 'tailnet webrtc' >/dev/null
ufw allow in on tailscale0 to any port 8555 proto udp comment 'tailnet webrtc' >/dev/null
ufw allow in on tailscale0 to any port 22 proto tcp comment 'tailnet ssh' >/dev/null
# SMB over the tailnet only after per-user passwords >= 12 chars and the
# owner's approval (PLAN Phase 9):
#   ufw allow in on tailscale0 to any port 445 proto tcp comment 'tailnet smb'
# Public address (ADR-0019): only while the marker exists (setup-public.sh,
# or the dashboard's settings via apply-config.sh). The ports come from the
# config (`sentinela public-ports`): the port-forwarded TLS listener, the
# HTTP-01 answer when remote.public.http01_listen is set, and 8555 for WebRTC
# media. Any source: internet-facing. Earlier public rules are removed first,
# so a changed port or a switched-off address closes the old one. A tunnel or
# reverse proxy (ADR-0025) needs no rule: it connects to loopback.
mapfile -t old < <(ufw status numbered | grep 'ADR-0019 P1' | sed -E 's/^\[ *([0-9]+)\].*/\1/' | sort -rn)
for n in "${old[@]}"; do ufw --force delete "$n" >/dev/null; done
if [[ -f /etc/sentinela/public-access ]]; then
  ports="$(docker run --rm --network none -v /etc/sentinela/config.yaml:/etc/sentinela/config.yaml:ro \
    --entrypoint /usr/local/bin/sentinela sentinela:local public-ports 2>/dev/null || true)"
  [[ -n "$ports" ]] || ports=$'7443/tcp\n8555/tcp\n8555/udp'   # older image: the defaults
  while read -r p; do
    [[ "$p" =~ ^[0-9]{1,5}/(tcp|udp)$ ]] && ufw allow "$p" comment 'public (ADR-0019 P1)' >/dev/null
  done <<< "$ports"
fi
# Quiet unmatched multicast discovery after the explicit LAN allowances.
# Drops remain in place; these packets simply stop filling the kernel log.
AFTER=/etc/ufw/after.rules
if ! grep -q 'sentinela-igmp' "$AFTER"; then
  # Before the first COMMIT (the filter table). iptables-restore syntax: no
  # trailing "#" comments, so the marker is an iptables comment.
  sed -i '0,/^COMMIT$/s//-A ufw-after-input -p igmp -m comment --comment sentinela-igmp -j ufw-skip-to-policy-input\nCOMMIT/' "$AFTER"
fi
if ! grep -q 'sentinela-discovery' "$AFTER"; then
  sed -i '0,/^COMMIT$/s//-A ufw-after-input -d 224.0.0.0\/4 -p udp -m multiport --dports 1900,5353 -m comment --comment sentinela-discovery -j ufw-skip-to-policy-input\nCOMMIT/' "$AFTER"
fi
ufw --force enable >/dev/null
ufw status numbered
