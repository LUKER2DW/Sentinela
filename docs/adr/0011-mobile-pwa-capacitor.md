# ADR-0011: Mobile — PWA first, then a Capacitor Android app

Status: Accepted · 2026-09-29 (owner decision D4; replaces PLAN Phase 6 "Flutter")

## Context
The owner wants the whole system (live, playback, PTZ, talk, events, Nuvem)
on the phone now. Remote access is Tailscale only (ADR-0004), so the only
HTTPS origin is `https://sentinela.<tailnet>.ts.net`, which is not public.

| Option | Reuse of the React UI | Native push / biometrics | H.265 live | Verdict |
|---|---|---|---|---|
| PWA installed from Chrome | 100 % | Web Push (via FCM); no biometrics | Chrome WebRTC/MSE | **Stage B1** |
| Capacitor wrapper of the SPA | ~95 % | Yes (plugins) | Android System WebView = Chromium, same WebRTC/MSE | **Stage B2** |
| TWA (Bubblewrap) | 100 % | Web Push only | Chrome | Rejected: Digital Asset Links need a public origin |
| Flutter + `flutter_webrtc` | 0 % | Yes | Depends on the native WebRTC build having H.265 | Rejected: rewrites the UI |

Chrome ships H.265 in WebRTC since version 136, but only with a hardware
decoder, so the MSE fallback and a per-device capability probe stay.

## Decision
- **B1:** make the SPA mobile-first (bottom tabs, touch PTZ, data saver on
  cellular, capability probe, "connect Tailscale" screen). Same service
  worker rule: never cache `/api` or `/stream`.
- **B2:** `mobile/` holds a Capacitor (7+) Android project. The WebView loads
  the tailnet origin (`server.url`, set at first run with a "test
  connection" step): same origin, so the existing cookie/CSRF auth works
  unchanged. A bundled fallback page explains how to connect when Tailscale
  is down. Bundled assets + bearer tokens were rejected: a second auth
  scheme for no gain, since nothing works offline anyway.
- Plugins: push (ADR-0013), local notifications, biometric app lock after
  5 min in background, share, filesystem (clips and Nuvem files).
- APK built locally (`scripts/build-apk.sh`), signing keystore outside the
  repo, side-loaded (no Play Store). iOS gets the PWA only.

## Consequences
+ One UI for desktop, PWA and app.
− The app shows nothing without the Tailscale VPN up (by design).
− Android toolchain (JDK + SDK) needed on the dev PC for B2.
