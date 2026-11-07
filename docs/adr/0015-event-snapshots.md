# ADR-0015: Event snapshots — one substream keyframe decode per event

Status: Accepted · 2026-09-29 (owner approved, Wave 2)

## Context
Review items and notifications need a picture of what happened. The CPU
has no HEVC hardware decoder and AGENTS.md §2.1 forbids decoding video on
the server unless an ADR gives a measured budget.

## Decision
- When an event starts, `sentinela serve` asks go2rtc for
  `/api/frame.jpeg?src=<camera>_sub`: go2rtc decodes **one** keyframe of the
  800×448 substream with its bundled ffmpeg and returns a JPEG (~50 KB).
- At most one snapshot per camera every 10 s; later events of the same
  burst reuse the review item's earlier picture.
- Stored as `/data/snapshots/<camera>/<event id>.jpg`, deleted with the
  event (same age as the recordings retention).
- Served only through the API (`/api/events/{id}/snapshot`, logged-in
  users); never sent to a third party (ADR-0013).

## Measurement (server, 2026-09-29)
Three consecutive snapshots: 0.63 s of go2rtc + ffmpeg CPU in total
(≈ 0.2 s each). The first took 6.6 s wall time (substream RTSP open +
keyframe wait), the next two 0.5–0.6 s. Worst case with the rate limit:
0.2 s per 10 s ≈ 2 % of one core per camera, only while events keep
firing; zero otherwise. The substream uses the camera's third RTSP
session, the same one a viewer on SD uses.

## Consequences
+ Pictures for review and notifications with a bounded, measured cost.
− The snapshot lands ~1–7 s after the event starts.
− Additional video decoding needs a separate measured ADR. The later
  motion-gated detector exception is ADR-0016 and reuses its analyzed frame
  as the snapshot. Timeline hover previews are compressed keyframe copies
  decoded by the browser/app, not this JPEG snapshot pipeline.
