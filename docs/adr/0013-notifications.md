# ADR-0013: Notifications — Web Push + ntfy, images never through a third party

Status: Accepted · 2026-09-29. Web Push built with Wave 2; the ntfy relay
approved by the owner and built after Stage B2 (same day).

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

### ntfy relay for the Android app
- The Capacitor WebView has no Push API, and Firebase is not wanted, so
  the app uses **UnifiedPush** with the ntfy Android app as distributor and
  a **self-hosted ntfy** (`binwiederhier/ntfy`, pinned) as the push server.
  The built-in publisher option was dropped: the phone side needs a
  distributor app anyway, and ntfy's is the maintained one.
- ntfy listens on `127.0.0.1:2586` only; `tailscale serve --https=8443`
  publishes it on the tailnet (ntfy cannot live under a sub-path of :443).
  No ntfy accounts: the tailnet ACL decides who connects, UnifiedPush topics
  are random names, and the web app is disabled. 64 MB memory cap.
- The app's `PushBridge` plugin registers with ntfy (`connector` 3.x) and
  returns the endpoint plus RFC 8291 keys; the page subscribes it through
  the same `/api/push/subscribe`. The server accepts only `up*` topics on
  its own `ntfy.public_url` and posts them to `ntfy.url` on loopback,
  encrypted like Web Push, without VAPID (ntfy ignores it). The connector
  decrypts; `SentinelaPushService` shows the notification and then adds the
  snapshot, fetched from the server with the app's session cookie.
- Phone setup: install ntfy, set its default server to
  `https://sentinela.<tailnet>.ts.net:8443`, then "Ativar" in the app.

## Consequences
+ No cloud account; works whenever the phone has Tailscale up.
+ ntfy only ever sees ciphertext, and nothing leaves the tailnet.
− One more small container (≈ 20 MB RSS) and one more tailnet port (8443).
− The ntfy app keeps a background connection, which uses some battery.
