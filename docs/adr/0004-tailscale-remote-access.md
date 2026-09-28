# ADR-0004: Remote access only through Tailscale

Status: Accepted · 2026-09-28

## Decision
No port forwarding, DDNS or public reverse proxy. Server joins a Tailscale
tailnet; phones use the Tailscale app. HTTPS via `tailscale cert` for the
MagicDNS name. WebRTC works directly over the tailnet (UDP, low latency).

## Alternatives
WireGuard self-hosted (needs one open UDP port, more ops), Cloudflare Tunnel
(adds latency, media via third party, v1 did this), port forwarding (rejected).

## Consequences
Every client device needs Tailscale. If CGNAT blocks direct paths, traffic
goes via DERP relays (higher latency) — measure in Phase 1.
