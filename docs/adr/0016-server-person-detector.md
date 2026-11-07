# ADR-0016: Motion-gated object detector (YOLO26n sidecar)

Status: Accepted · 2026-09-29 (owner approved; model choice revised the same day)

Implementation status (2026-09-30): deployed and real person events observed
in the round-3/5 work recorded in PLAN.md. Measurements below retain their
original dates; they are not a new continuous hardware benchmark.

## Context
Camera motion events fire on trees, shadows, rain, headlights and cars on
the street. The owner wants an alert only when motion is something that
matters — a person walking in, a vehicle entering, an animal — inside areas
drawn in Sentinela (the camera's own region editor is poor). The server is
may be an old CPU without AVX or HEVC hardware decode (AGENTS.md §2.1), so any decode
needs a measured budget. The owner's earlier project (Local-Cloud) ran
YOLO26n at 320 px behind a motion stage with polygon zones and worked well.

## Research (2026-09-29)
- **YOLO26** (Ultralytics, Jan 2026) is the newest released YOLO; YOLO27 is
  announced, not released. YOLO26n: 2.4 M params, 40.9 mAP (COCO, 640),
  NMS-free option, up to 43 % faster CPU ONNX than YOLO11n. YOLOv12/v13 use
  attention (slow on CPUs without AVX). RF-DETR (Apache-2.0) is more
  accurate but ~30 M params, GPU-oriented. YOLOX-nano (Apache-2.0) is
  permissive but 25.8 mAP.
- **Frigate** runs detectors through ONNX/OpenVINO with the `yolo-generic`
  model type, recommends 320×320 inputs, and does not ship Ultralytics
  models (AGPL); users supply them.

## Measurements (server, real frames of this camera)
| Model | ms / image (1 thread) | person at the gate (20.jpg) | far person (9.jpg) |
|---|---|---|---|
| YOLOX-nano 416 | ~146 | 0.64 | 0.57 |
| YOLO26n 320 | 90 | 0.54 (+ a false "bench") | — |
| **YOLO26n 416** | **157** | **0.77** | 0.36 |

Pipeline cost at one keyframe every 2 s (substream 800×448, decode of
keyframes only, swscale to 416, inference): **13 % of one core while
analysing**, ~0.4 % idle (the stream is only demuxed). RSS 65–105 MB.

## Decision
- A `detector` container (Python 3.12, ONNX Runtime CPU, PyAV, numpy) with
  low CPU priority (`cpu_shares: 128`, ceiling `cpus: 1.0`; was `cpus:
  0.15`, see Revision), `mem_limit: 192m`, read-only FS, no capabilities, uid 1000.
- **Motion-gated**: Sentinela exposes the camera's ONVIF motion state; the
  detector polls it every second. Idle it only demuxes `<cam>_sub` from
  go2rtc's loopback RTSP; while motion is active (+10 s) every keyframe is
  decoded and analysed, including the keyframe just before motion began.
- **YOLO26n at 640×352** (was 416×416, see Revision), standard `(1, 84, N)` head with our own NMS
  (40.9 mAP; the NMS-free head is 40.1). Kinds reported: person → event
  `human` (Alert), vehicle (bicycle/car/motorcycle/bus/truck) → `vehicle`,
  cat/dog → `animal` (Detections).
- **Zones** drawn in Sentinela (polygons, normalised): an object counts when
  the bottom-centre of its box — feet or wheels — is inside a zone that
  watches its kind. No zones = the whole picture watches people.
- Events: start on the first positive keyframe (motion gate + zones already
  filter), end after 12 s without; the analysed frame becomes the event
  snapshot (no second decode). While the detector is healthy, plain
  `motion` stays on the timeline but no longer notifies.
- Integration: loopback-only `/internal/detector/*` with a bearer token in
  `/data/detector/token` (created by `sentinela serve`, 0640, same uid).
  Events left open by a vanished detector are closed within ~2 min.
  Recording and the API never depend on the detector.
- **Licence**: YOLO26 weights are AGPL-3.0. The repository stays MIT: no
  weights and no Ultralytics code are committed; the image build downloads
  `yolo26n.pt` pinned by sha256 and exports ONNX in a throwaway stage. The
  runtime image contains the exported model and is not redistributed. A
  permissive fallback (YOLOX-nano, Apache-2.0) was measured above.

## Consequences
+ Alerts for people/vehicles/animals in chosen areas; trees and passing cars
  outside the zones stay silent.
+ ~0 CPU when nothing moves; ~15 % of one core, at low priority, when it does.
− ~500 MB image (ONNX Runtime + PyAV); shipped by `deploy.sh` only when it
  changes.
− Keyframe cadence (2 s) means a person crossing in < 2 s may be missed;
  the substream (800×448) limits far/small objects.
− The camera's own person alarm (DVRIP) is still being probed; if it proves
  reliable it can feed the same event type.

## Revision (2026-10-01): input size, priority, zones, debouncing

Measured on the server with live substream keyframes (800×448) and on 382
recorded keyframes with people and cars (substream size, offline):

| Input | inference / keyframe (server) | person scores, same frames |
|---|---|---|
| 416×416 (before) | 154 ms | 0.37–0.64 |
| 416×224 | 77 ms | about the same as 416×416 |
| **640×352** | **196 ms** | **0.53–0.81** (+0.1–0.25 per person) |

A square input wasted 44 % of the picture on padding. 640×352 matches the
substream's aspect; it costs +25 % CPU per analysis (~290 ms including the
43 ms HEVC keyframe decode) and gives clearly higher person confidence,
which matters most for far people. int8 (backbone quantised, head float)
runs at the old 416 cost with most of the gain, but its calibration only
had daytime frames: revisit with night frames (street-lit, no IR).

The 15 % quota was the wrong tool: an analysis is a ~0.3 s burst every 2 s,
and the quota stretched it to 1.5 s (peaks 3.8 s, keyframes dropped) while
spending the same or more CPU (cold caches between 15 ms slices).
`cpu_shares: 128` (lowest weight) with a one-core ceiling: 0.27 s per
analysis, ~14.5 % of one core while analysing, recorder and go2rtc win any
contention. ONNX Runtime's memory arena is off (−40 MB peak, same speed).

Detection logic:
- **Zones crop**: with zones, only their bounding box (plus 40 % of the
  picture height above, for bodies) is analysed, at the model's aspect and
  enlarged up to 2×, unless that is ≥ 60 % of the picture anyway.
- **Hysteresis**: from the chosen sensitivity `s`: one frame at `s + 0.15`
  or two of the last three at `s − 0.1` start an event; `s − 0.1` keeps it.
  Weak frames are forgotten when motion ends. The snapshot is the whole
  picture of the best frame among those that started it.
- **Stationary objects**: a box that stays in place (IoU ≥ 0.6 with where
  it was first seen) stops counting after 30 s for vehicles, 60 s for
  animals, 5 min for people; forgotten after 8 analyses without it. What is
  in the picture when the detector starts counts as already there.

## Revision (2026-10-01): live overlay and more settings

- **Overlay**: after each analysis the detector posts every object it saw
  (box, score, whether it counts, parked) to the loopback-only
  `/internal/detector/objects`; the API relays it on the existing SSE
  stream as an `objects` event, filtered by camera permission, and stores
  nothing. The live view draws the boxes over the picture (a dot marks the
  feet); they fade after ~4 s without a new analysis. What is drawn
  (boxes, names, confidence, also what does not warn, zone outlines) is a
  preference of each device.
- **Probe**: the zone editor asks `POST /api/cameras/{id}/detection/probe`
  (operator); for 20 s the detector analyses that camera without motion
  and publishes boxes, so the editor shows whether each object would
  count. The live view asks too (any viewer of the camera, every 15 s
  while the page is visible and boxes are on), because the camera's own
  motion detection fires rarely (1 motion event in 2 h with cars passing,
  2026-10-01): ~15 % of one core per watched camera, at low priority. A
  probe never opens events or changes the trackers.
- **Settings** per camera: kinds watched when there are no zones
  (`objects`, default people), `ignore_parked` (default on) and `min_size`
  (objects shorter than this share of the picture height are ignored).
  Fields an older app does not send keep their stored values.
