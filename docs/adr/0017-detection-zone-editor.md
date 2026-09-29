# ADR-0017: Detection zones drawn in Sentinela, not in the camera

Status: Accepted · 2026-09-29 (owner: "better to build our own; the camera's regions are poor")

## Context
The first plan was an editor for the camera's own motion/human regions:
a persistent camera config write through DVRIP, with an undocumented
region object (only `AlarmFunction.HumanDection` is advertised). The owner
finds the camera's regions poor (a coarse grid) and prefers zones of our
own. With the object detector (ADR-0016) Sentinela can apply any polygon to
what it detects, so no camera write is needed.

## Decision
- Zones are **Sentinela data**, not camera config: per camera, up to 10
  polygons (3–32 points, normalised 0..1), each with a name and the kinds
  it watches (people, vehicles, animals). Stored with the detection
  settings (on/off, sensitivity) in the `settings` table as
  `detection.<camera>`.
- Rule: an object counts when the bottom-centre of its box (feet/wheels)
  is inside a zone watching its kind — a person behind the gate on the
  street does not count even if their head overlaps the zone.
- Editor on the System page (operators): a current substream picture
  (`/api/cameras/{id}/frame.jpeg`, one go2rtc keyframe decode, cached 5 s),
  tap to add points, drag to move, per-zone kinds, sensitivity presets.
- The camera's motion detection stays as it is: it is the cheap trigger
  that wakes the detector. Its regions are not touched (no DVRIP write).

## Consequences
+ Free-form polygons, per-kind zones, editable from the phone; no camera
  write path, nothing to allow-list in CAMERA.md.
− Zones only apply to the detector's events; raw camera `motion` events
  still cover the camera's own region (they only feed the timeline and the
  detector trigger).
