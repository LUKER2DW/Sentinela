# ADR-0016: Server-side person detector — measured, not built yet

Status: Proposed · 2026-09-29 (Wave 3 spike done; building needs the owner's go-ahead)

## Context
Motion events fire on shadows, rain and headlights. The owner approved a
spike: a detector on the old CPU (no AVX, no HEVC hardware decode) with a
hard limit of **15 % of one core**, measured first. The camera may detect
people itself (`AlarmFunction.HumanDection`); that path is being probed in
parallel (DVRIP alarm channel, PLAN Wave 3) and costs zero server CPU.

## Measurement (server, 2026-09-29, read-only, throwaway containers)
| Step | Cost |
|---|---|
| Substream (800×448 H.265) **keyframes only** (`-skip_frame nokey`, one every 2 s) decoded + scaled to 320 px gray | 1.47 s CPU / 60 s = **2.5 %** of one core, 60 MB RSS |
| Same substream, every frame decoded (10 fps) — for comparison | 3.86 s / 58 s = 6.6 % |
| YOLOX-nano (Apache-2.0, 416×416, ONNX Runtime CPU, 1 thread, SSE4.2 only) | **90 ms** per image → 4.5 % at one image every 2 s, 81 MB RSS (Python) |
| **Total at 0.5 fps** | **≈ 7 %** of one core, ≈ 140 MB RSS |

The keyframe path pulls `cam1_sub` from go2rtc's loopback restream, so it
uses no extra camera RTSP session beyond the SD live one (docs/CAMERA.md §3).

## Options
1. **Camera-side human detection via DVRIP alarms** (preferred if it
   works): zero CPU, the camera already has a region around the gate.
2. **Sidecar detector**: ffmpeg keyframes → YOLOX-nano in ONNX Runtime,
   one process, `mem_limit: 192m`, `cpus: 0.3`; posts `human` events to the
   API. New container → this ADR must be accepted first (AGENTS.md §2.8).
3. **Inside the Go binary via cgo + ONNX Runtime**: no new container, but
   ends the pure-Go static build and ties the API's stability to a C++
   runtime — rejected.
4. **Coral USB**: owner's call; not needed at 7 %.

## Decision (proposed)
Wait for the DVRIP alarm probe. If the camera's person alarm is reliable
for a week, stop here. Otherwise build option 2 with these limits:
keyframes only, substream only, ≤ 0.5 fps, ≤ 15 % of one core and
≤ 192 MB (a watchdog stops it above either), detections become `human`
events (already Alerts in review and notifications). Recording never
depends on it (AGENTS.md §2.2).

## Consequences
+ Fewer false alerts at a measured ~7 % of one core.
− A second decode path on the server (after ADR-0015's snapshots) and a
  ~140 MB container on a 4 GB host; the 8 GB upgrade becomes advisable.
− Model weights are a new artifact to pin and verify (sha256 in the image).
