#!/usr/bin/env bash
# Host firewall for the Sentinela server (docs/ARCHITECTURE.md §4). Idempotent.
# Default deny inbound; LAN-only allowances. Nothing is exposed to the WAN.
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
# Tailscale (later): its interface is trusted as a whole.
ip link show tailscale0 >/dev/null 2>&1 && ufw allow in on tailscale0 >/dev/null
ufw --force enable >/dev/null
ufw status numbered
