# ADR-0017: Detection zone editor — plan only

Status: Proposed · 2026-09-29 (plan only; no camera write until the owner approves)

## Context
The camera's motion and human-detection regions are set in its own web UI.
The owner would like to draw them in Sentinela. That is a **persistent
camera config write**, which AGENTS.md §2.6 only allows for objects
documented in docs/CAMERA.md with a read → change one field → read back →
restore-on-failure cycle. The region object of this firmware is **not
documented yet**: SystemFunction only advertises `AlarmFunction.HumanDection`
and `MotionHumanDection`; the config names and the grid/polygon format are
unknown, and must not be guessed (CLAUDE.md).

## Plan
1. **Read-only probe** (owner approval needed for the probe itself, since
   the name is not documented): read the detection objects with message
   1042, save sanitized fixtures under `internal/camera/dvrip/testdata/`,
   and document names, grid size and value format in CAMERA.md §1.3.
2. Decode/encode the region format in pure Go with fixture tests
   (round-trip must be byte-identical for an unchanged grid).
3. UI (admin): the live substream snapshot with the grid on top; paint
   cells; "Salvar" shows a diff first.
4. Write path: the existing `setParam` discipline — read the full object,
   change only the region field, write, read back, compare, restore the
   original on any mismatch; audit log with before/after hashes.
5. A hardware test that writes the **unchanged** object back, run once with
   the owner watching, before any real edit.

## Consequences
+ Regions editable from the phone, with a safe rollback.
− New allow-listed write; one more firmware-specific format to maintain.
− If the firmware stores regions in a format we cannot round-trip exactly,
  the feature is dropped (the camera UI remains the way to edit them).
