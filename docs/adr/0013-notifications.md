# ADR-0013: Notifications — Web Push + ntfy, images never through a third party

Status: Accepted for Web Push · 2026-09-29. The ntfy part is deferred to
Stage B2 (owner decision: no new service for now).

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

## Implementation (2026-09-29)
- `internal/push`: VAPID (ES256 JWT) and RFC 8291 aes128gcm with the Go
  standard library only; the RFC 8291 Appendix A vector is a unit test.
- VAPID keys: `/data/push/vapid.json` (0600), created on first start.
- Subscriptions (`push_subscriptions`, per user and device) are accepted
  only for the push services of current browsers (FCM, Mozilla, Apple,
  WNS; https, default port): a user cannot make the server POST to a LAN
  address. 404/410 from the service deletes the subscription.
- Modes (`settings.notify_mode`): Casa = tampering only, Noite = alerts,
  Fora = everything; Casa switches to Noite automatically between two
  local times (config `timezone`, default America/Sao_Paulo). At most one
  notification per camera per minute.
- Payload: title, time, and the snapshot *path* on the server; the phone
  fetches the picture itself over the tailnet.

## Consequences
+ No cloud account; works whenever the phone has Tailscale up.
− ntfy would be one more small service (≈ 20 MB RSS) if chosen.
