# ADR-0026: H.265 converted in the browser for viewing

Status: accepted (2026-11-08).

## Context

Cameras send H.265 and the server records it as it comes; the minimum host
cannot decode video, so the server never transcodes (AGENTS.md §2.1).
Browsers without H.265 (Firefox on many PCs, Chrome or Edge without a
hardware decoder) refused every live view and recording with "this browser
cannot decode H.265", even before trying a camera that sends H.264.

## Decision

- **The server does not change.** It records and serves H.265 exactly as
  before; nothing here decodes or encodes on the server.
- **Live view (`player.ts`).** The player asks go2rtc only for the codecs
  this browser plays by itself. When the camera's video does not match
  (go2rtc answers "codecs not matched", or sends audio only), it falls back,
  in order, without repeating a step:
  1. convert the requested stream in this browser;
  2. the camera's substream as it is (often H.264);
  3. the substream converted (lighter than the main stream);
  4. a message that says what to change.
- **Conversion** uses `@hevcjs/core` (MIT, lid-labs/hevc.js): H.265 decoded
  in WebAssembly in a Web Worker, re-encoded to H.264 with the browser's
  WebCodecs encoder, behind MediaSource. It is loaded only on demand (a
  lazy chunk plus the worker and WASM under `/hevc/`, ~115 KB gzipped WASM).
  It needs a secure context (HTTPS: Tailscale, the public address, the
  app); on plain `http://` at home the substream fallback applies. A
  converted live stream carries video only (the camera's audio through
  go2rtc would make a muxed stream the converter cannot rebuild).
- **Limits.** Each device converts at most 1–3 streams at once (by cores;
  one on phones); a stream whose converter falls behind repeatedly moves to
  the next fallback. After a failed conversion the converter is reinstalled
  so the next try gets fresh workers.
- **Recordings** (already fragmented MP4) are read box by box and fed to
  MediaSource GOP by GOP through the same converter, at most 20 s ahead of
  the playhead.
- The CSP gains `'wasm-unsafe-eval'` (compiling WebAssembly only, not
  `eval`).

## Consequences

+ Every recent browser can watch every camera; the server and recordings
  stay H.265 (half the disk of H.264).
+ No server CPU is spent; the cost is on the viewing device, only when it
  lacks H.265.
− Conversion costs CPU and battery on the viewer, adds about a second to
  start, and depends on the browser's H.264 encoder: a software encoder may
  refuse large frames (a 2560×1440 main stream), and then the substream is
  shown.
− Converted live video has no sound.
