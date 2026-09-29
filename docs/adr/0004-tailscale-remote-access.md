# ADR-0004: Remote access only through Tailscale

Status: **Implemented** 2026-09-29 (owner decision D3) · Accepted 2026-09-28

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

## Setup (2026-09-29)
- Tailscale on the host from the official apt repo (`scripts/setup-tailscale.sh`),
  `--hostname=sentinela`, no Tailscale SSH, no subnet router, no exit node.
- HTTPS: `tailscale serve --bg --https=443 http://127.0.0.1:8080`; serve
  renews the certificate. The app trusts forwarded headers only from loopback.
- WebRTC: go2rtc ICE on `100.x.y.z:8555` (TCP+UDP).
- ACL and host firewall: docs/SECURITY.md rules 8–9.
- Tailnet `tail0.ts.net` (random name, fine for CT logs); server
  tagged `tag:nvr`. Measured from 4G: direct path, live ~0.8 s (CAMERA §5).
- tailscaled accepts all `tailscale0` traffic ahead of ufw: the ACL is the
  effective filter.
