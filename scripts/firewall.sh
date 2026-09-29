#!/usr/bin/env bash
# Host firewall for the Sentinela server (docs/ARCHITECTURE.md §4). Idempotent.
# Default deny inbound; LAN and tailnet allowances. Nothing is exposed to the
# WAN, except the ADR-0019 P1 trial ports while /etc/sentinela/public-access exists.
#   sudo ./scripts/firewall.sh [LAN_CIDR]
set -euo pipefail
[[ $EUID -eq 0 ]] || { echo "run with sudo" >&2; exit 1; }
LAN="${1:-192.168.1.0/24}"
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
# 8443 = `tailscale serve` HTTPS for the ntfy relay (ADR-0013),
# 8555 = WebRTC ICE. 8080 stays LAN-only: remote users always get HTTPS.
# The rules match the interface name, so they are safe to add before
# Tailscale is installed. The tailnet ACL is the primary control.
ufw allow in on tailscale0 to any port 443 proto tcp comment 'tailnet https (serve)' >/dev/null
ufw allow in on tailscale0 to any port 8443 proto tcp comment 'tailnet ntfy (serve)' >/dev/null
ufw allow in on tailscale0 to any port 8555 proto tcp comment 'tailnet webrtc' >/dev/null
ufw allow in on tailscale0 to any port 8555 proto udp comment 'tailnet webrtc' >/dev/null
ufw allow in on tailscale0 to any port 22 proto tcp comment 'tailnet ssh' >/dev/null
# SMB over the tailnet only after per-user passwords >= 12 chars and the
# owner's approval (PLAN Phase 9):
#   ufw allow in on tailscale0 to any port 445 proto tcp comment 'tailnet smb'
# Remote access trial P1 (ADR-0019): only while scripts/setup-public.sh has
# left its marker. 7443 = the port-forwarded TLS listener (any router WAN
# port), 7080 = the HTTP-01 answer for the IP certificate, only when
# remote.public.http01_listen is set (404 for anything else), 8555 = WebRTC
# media. Any source: internet-facing.
if [[ -f /etc/sentinela/public-access ]]; then
  ufw allow 7443/tcp comment 'public https (ADR-0019 P1)' >/dev/null
  if grep -q '^[[:space:]]*http01_listen:' /etc/sentinela/config.yaml 2>/dev/null; then
    ufw allow 7080/tcp comment 'public acme http-01 (ADR-0019 P1)' >/dev/null
  else
    ufw delete allow 7080/tcp >/dev/null 2>&1 || true
  fi
  ufw allow 8555/tcp comment 'public webrtc (ADR-0019 P1)' >/dev/null
  ufw allow 8555/udp comment 'public webrtc (ADR-0019 P1)' >/dev/null
fi
ufw --force enable >/dev/null
ufw status numbered
