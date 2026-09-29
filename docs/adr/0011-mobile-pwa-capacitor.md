# ADR-0011: Mobile — PWA first, then a Capacitor Android app

Status: Accepted · 2026-09-29 (owner decision D4; replaces PLAN Phase 6 "Flutter") · B1 and B2 built 2026-09-29

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

## Implementation (2026-09-29)
- `mobile/`: Capacitor 8, Android only. `server.url` is fixed at build time
  (`SENTINELA_URL`, default the owner's tailnet name) instead of a first-run
  prompt: one household, one server; a different server means a rebuild.
  `server.errorPath` shows the bundled "Sem conexão com a casa" page with a
  button that opens the Tailscale app.
- Native code only where the WebView falls short (`MainActivity`): the app
  lock (BiometricPrompt: fingerprint/face or the screen lock, after 5 min
  in the background; skipped on phones without a screen lock) and downloads
  through DownloadManager, sending the session cookie only to the server's
  own host. The WebView's Web Share API is absent, so the share buttons
  hide themselves and "Baixar" is used instead.
- No Firebase: the WebView has no Push API, so the app gets notifications
  through UnifiedPush and a self-hosted ntfy (ADR-0013); the ntfy app must be
  installed on the phone. Without it, the PWA still notifies.
- `scripts/build-apk.sh` builds and signs on the dev PC (JDK 21 + Android
  SDK command-line tools, no Android Studio). The signing key and its
  random password live in `~/.sentinela-android/` on the dev PC, never in
  git; losing them means uninstalling the app to install a newer build.
- Device sessions are listed on the Mais page ("Aparelhos conectados") and
  can be signed out one by one.
