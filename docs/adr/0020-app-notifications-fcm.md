# ADR-0020: Android app notifications through Firebase Cloud Messaging

Status: accepted (2026-09-30, owner)
Amends: ADR-0013 (round 4: the app's own foreground service)

## Context
Round 4 replaced ntfy/UnifiedPush with a foreground service in the app that
holds `GET /api/notify/stream` open, so no third party sees anything. Android
requires every foreground service to show a notification; the owner does not
want that fixed notice ("WhatsApp and TikTok do not need it") and chose
Firebase Cloud Messaging (FCM), the push channel Android itself keeps open
through Google Play services.

## Decision
- The server sends FCM **data-only, high-priority** messages through the
  FCM HTTP v1 API, signed with the project's service account (JWT RS256 →
  OAuth2 access token, stdlib only, no Firebase SDK on the server).
- **Nothing about the event goes through Google.** The message carries only
  an opaque random id. The app wakes, fetches the notification (title, text,
  link, snapshot) from the house over its pinned HTTPS connection with its
  own session (`GET /api/notify/pending/{id}`, kept 15 min in memory, only
  for that user), and shows it. Google sees that "this phone got a message
  from project sentinela-d43c3", and when.
- The app opts in after notification permission. Before stopping its direct
  connection, the server obtains an OAuth token and checks the device token
  and API permission with FCM's `validate_only` send request. Tokens are tied
  to the user's login session (`fcm_tokens`, migration 0009): ending the
  session removes the registration, and expired sessions are excluded.
  FCM's explicit UNREGISTERED response also removes a token.
- The runtime service account key lives on the server
  (`<data_dir>/fcm/service-account.json`, 0600, never in git, logs or the
  API). The app's Firebase client values (`google-services.json`) stay
  outside the repo (`~/.sentinela-android/`) and are compiled in by
  `build-apk.sh`. The separately installed `.dev` app uses the direct
  fallback; the build creates a local debug JSON so Gradle can compile it.
- **Fallback:** without a usable key or Cloud Messaging API on the server,
  or on a phone without Google Play services, the app keeps the round-4
  foreground service. It retries when opened, and switches after successful
  registration. If the house cannot be reached when a Google signal arrives,
  the app shows a generic notice that opens Eventos.
- Browsers keep Web Push (VAPID) unchanged.

## Consequences
+ No fixed notice, less battery (no own socket), delivery in Doze like any
  messaging app.
- A new external dependency (Google FCM + Play services). The house keeps
  recording and events stay on the Eventos page during a push outage.
- The Firebase project and its key are owned by the owner; rotating the key
  is replacing one file on the server.

## Build and verification

The owner's source key is retained outside Git in the protected developer
signing directory. Its runtime copy is 0600/UID 1000 on the server. The
client JSON and service-account JSON must belong to the same project.

Go tests cover token/session cleanup, user-isolated/expired pending messages
and data-only requests. Credential OAuth and validation-only API access were
checked; release/debug Android builds succeeded. Real phone delivery is
still an acceptance check, not implied by those builds.

Release 1.11's missing TLS pin prevented both the WebView and native event
fetches from trusting the house. Release 1.12 uses permanent connection
configuration and refuses builds with no valid pin (ADR-0011/0019).
