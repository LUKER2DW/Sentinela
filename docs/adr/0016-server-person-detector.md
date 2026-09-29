# ADR-0016: Motion-gated object detector (YOLO26n sidecar)

Status: Accepted · 2026-09-29 (owner approved; model choice revised the same day)

## Context
Camera motion events fire on trees, shadows, rain, headlights and cars on
the street. The owner wants an alert only when motion is something that
matters — a person walking in, a vehicle entering, an animal — inside areas
drawn in Sentinela (the camera's own region editor is poor). The server is
an old CPU: no AVX, no HEVC hardware decode (AGENTS.md §2.1), so any decode
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
  `cpus: 0.15`, `mem_limit: 192m`, read-only FS, no capabilities, uid 1000.
- **Motion-gated**: Sentinela exposes the camera's ONVIF motion state; the
  detector polls it every second. Idle it only demuxes `<cam>_sub` from
  go2rtc's loopback RTSP; while motion is active (+10 s) every keyframe is
  decoded and analysed, including the keyframe just before motion began.
- **YOLO26n at 416×416**, standard `(1, 84, N)` head with our own NMS
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
+ ~0 CPU when nothing moves; capped at 15 % of a core when it does.
− ~500 MB image (ONNX Runtime + PyAV); shipped by `deploy.sh` only when it
  changes.
− Keyframe cadence (2 s) means a person crossing in < 2 s may be missed;
  the substream (800×448) limits far/small objects.
− The camera's own person alarm (DVRIP) is still being probed; if it proves
  reliable it can feed the same event type.
