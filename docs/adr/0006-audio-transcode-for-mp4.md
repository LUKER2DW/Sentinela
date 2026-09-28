# ADR-0006: Audio (only) may be transcoded; video never

Status: Accepted · 2026-09-28

## Context
The camera sends G.711 A-law (PCMA) 8 kHz mono audio. With `-c copy`, the MP4
muxer rejects it (`Could not find tag for codec pcm_alaw … not currently
supported in container`; checked with ffmpeg 9.0 on 2026-09-28), and browsers
cannot play PCMA inside MP4 anyway. go2rtc does the same for MSE: it converts
PCMA → FLAC in-process, because MSE cannot carry PCMA either.

## Decision
- **Video is never decoded or re-encoded** (hard constraint 1 still holds).
- The recorder may encode **audio only**: `-c:v copy -c:a aac -b:a 32k`
  (8 kHz mono). Recording audio can be turned off per camera
  (`record_audio: false`), which makes recording 100 % copy.
- go2rtc's built-in PCMA → FLAC/Opus conversion for MSE/WebRTC viewers is
  accepted.

## Budget
8 kHz mono AAC is a few hundred kilobits per second of work: well below 1 %
of one core even on an old CPU. The recorder budget (≤ 3 % CPU per camera)
covers it. Re-measure on the server in the Phase 2 72 h run.

## Alternatives
- Matroska/MOV with copied PCMA: copy-only, but browsers can't play it,
  so every playback would need a server-side remux + audio transcode.
- Drop audio from recordings: loses evidence; kept as the per-camera option.
