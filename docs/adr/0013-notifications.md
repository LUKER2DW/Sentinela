# ADR-0013: Browser Web Push and event notification policy

Status: Accepted 2026-09-29. Android transport is superseded by ADR-0020
(2026-09-30); browser Web Push and event policy remain active.

## Current decision

- Browsers/PWA use VAPID and RFC 8291 encrypted Web Push. Private VAPID keys
  are generated under `/data/push/vapid.json`, mode 0600.
- Subscriptions are user/device-bound and accepted only for allow-listed
  browser push services or the retained legacy relay. They cannot make the
  server POST to arbitrary LAN addresses. Expired 404/410 subscriptions
  are removed.
- The encrypted browser payload contains text and a server snapshot path.
  The phone fetches the picture from the authenticated house origin.
- Android uses an opaque FCM id and fetches all event details from the
  house (ADR-0020), with the direct stream as fallback.
- Modes Casa/Noite/Fora and each user's type/follow-mode preferences decide
  which events notify. Casa means tampering; Noite means alerts; Fora
  includes other detections. Automatic night uses the configured local
  timezone. At most one notice/user/camera/minute.
- When the server detector is healthy, raw motion does not generate a push;
  detector events in the selected zones do.
- The UI bell separates today's camera events and system notices; the
  phone displays a bottom sheet. Tapping a camera notice opens its recording.

## Implementation

`internal/push` implements VAPID/RFC 8291 and FCM HTTP v1 using Go's
standard library. `push_subscriptions` stores browser registrations.
`fcm_tokens` stores Android registrations tied to login sessions.
The image is never relayed through the third-party push channel.

## Historical Android choices

The first Capacitor implementation used ntfy as a UnifiedPush distributor
with a self-hosted relay on loopback 2586/tailnet HTTPS 8443. Round 4 removed
that app integration and used an authenticated foreground SSE connection
to the public household address. That connection needs a fixed Android
notice, so the owner selected Firebase under ADR-0020.

The legacy ntfy container/configuration remains present, but installing ntfy
or opening its tailnet port is not a requirement of the current Android app.

## Consequences

+ Browser support needs no separate installed notification app.
+ Android event text/images stay at home; Google sees the wake-up timing/id.
- Browser delivery depends on the browser vendor; Android FCM depends on
  Google Play services and the household Firebase project.
- Delivery on the owner's phone still needs the acceptance checks in PLAN.