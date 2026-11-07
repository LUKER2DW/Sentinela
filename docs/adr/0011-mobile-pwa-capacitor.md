# ADR-0011: Mobile — PWA and Capacitor Android wrapper

Status: Accepted 2026-09-29; implemented, amended by ADR-0019 and ADR-0020.
Current APK: 1.12 (version code 13), 2026-09-30.

## Context and choice

The owner wants live view, playback, PTZ, talk, events and Nuvem on the
phone. Reusing the React UI avoids a second product and auth scheme.

| Option | UI reuse | Decision |
|---|---|---|
| PWA | Entire SPA | Browser installation, Web Push |
| Capacitor | Same SPA in Android WebView | Native app lock, downloads, notifications and updates |
| TWA | Same SPA, restricted native integration | Not selected |
| Flutter | Separate UI/player implementation | Not selected |

H.265 playback depends on the device decoder. The app does not solve an
unsupported device by transcoding on the server.

## Current decision

- Capacitor 8 wraps the server origin. `Connection` selects `server.url`
  at launch from the bundled start screen; cookies/CSRF remain same-origin.
- ADR-0019 selected the public HTTPS path. Tailscale is optional private
  administration; embedded tailnet code is parked and Funnel is off.
- The bundled page supports retry/change address and explains offline/TLS
  failures. MainActivity preserves lock state across process death and
  handles renderer failures.
- Native code covers biometric/device-lock protection after five minutes
  away, downloads with authenticated pinned HTTPS, notifications (ADR-0020)
  and sideloaded updates.
- The SPA uses bottom tabs, a grouped More (*Mais*) menu, visible subpage/folder back
  buttons and safe-area spacing. Camera configuration, users and audit
  remain desktop tasks.
- The APK is built locally and signed with the persistent household key.
  It is not distributed through the Play Store; iOS remains a PWA.

## Release configuration

Keep these outside Git under `~/.sentinela-android/`:

- `release.jks` and `keystore.pass`: Android signing identity.
- `google-services.json`: Firebase client matching `casa.sentinela.app`.
- `connection.json`: `domain_url` (HTTPS origin) and
  `public_cert_sha256` (64 hex characters, exact server certificate).

Explicit environment variables can override the connection values.
Gradle validates every release; `build-apk.sh` checks the generated pin
before copying the signed APK. Obtain the fingerprint through trusted SSH
and compare it to the served certificate. Normal system trust or the exact
pin is required; do not accept all TLS certificates.

The separate `.dev` app uses direct notifications and locally generated
debug Firebase configuration. It does not register in the release Firebase
application.

## Updates and recovery

`build-apk.sh` generates `mobile/build/sentinela.apk`.
`publish-apk.sh` publishes its version/size/hash through authenticated
`/api/app/latest` and `/api/app/sentinela.apk`. App 1.9+ downloads the APK,
checks its SHA-256 and opens Android's installer; Android enforces the same
signing key and installation-source permission.

APK 1.11 was published without its certificate fingerprint because a
temporary environment importer missed digits in the variable name. It
cannot connect to the household self-signed server, including its updater.
A manual same-key 1.12 install recovers it without uninstalling.
Permanent connection configuration and release rejection prevent recurrence.

## Historical changes

The first implementation used a fixed tailnet URL and ntfy/UnifiedPush.
ADR-0019 then tested public TLS, embedded tailnet and Funnel, selecting
public TLS. Round 4 replaced UnifiedPush with a direct foreground stream.
ADR-0020 makes Firebase the Android wake-up transport, with that direct
stream retained as a fallback.

## Consequences

+ Desktop, PWA and Android share the UI and session model.
+ Native notifications need no fixed foreground notice while FCM works.
- Remote operation needs the chosen server origin to be reachable.
- Toolchain, signing key and trusted TLS configuration belong to the owner.
- Real-device acceptance is tracked separately; a successful build is not an
  end-to-end delivery test.