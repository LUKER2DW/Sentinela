# ADR-0013: Notifications — Web Push + ntfy, images never through a third party

Status: Proposed · 2026-09-29 (Wave 2; the ntfy part needs the owner's OK
for a new container or host service)

## Context
Event alerts must reach the phone quickly (≤ 5 s) without exposing the
server. Web Push works for the PWA but routes through the browser vendor's
push service (FCM/APNs); its payload is encrypted end to end. The ntfy
Android app does not use Firebase with self-hosted servers and can act as a
UnifiedPush distributor for the Capacitor app.

## Decision
- The server emits each notification to (1) Web Push (VAPID keys generated
  on the server, stored 0600) and (2) an ntfy-compatible topic on the
  tailnet (self-hosted ntfy, or a tiny built-in publisher if simpler —
  decided when Wave 2 starts).
- Payload: title, camera, time and a snapshot **URL on the tailnet**. The
  image itself never goes through a third party.
- Which event types notify depends on the mode (Casa / Fora / Noite).

## Consequences
+ No cloud account; works whenever the phone has Tailscale up.
− ntfy would be one more small service (≈ 20 MB RSS) if chosen.
