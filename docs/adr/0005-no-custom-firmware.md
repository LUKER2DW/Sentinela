# ADR-0005: Do not replace the camera firmware (for now)

Status: Accepted · 2026-09-28 · Amended 2026-11-06

At the recorded 2026-09-28 research, Novatek NT98566 was "work in progress"
in OpenIPC. This is a dated finding, not a current compatibility claim. Flashing risks a brick
and loss of PTZ/IR/audio drivers. We isolate the camera on the network instead
and control it via DVRIP/ONVIF. Revisit with a spare unit + UART adapter.
No custom camera firmware has been deployed by this project.

## Amendment 2026-11-06

The owner wants to explore an own, open-source camera firmware. The decision
for the **production camera** stands: never flash it. Exploration is allowed
only on a **spare, identical unit** (`IPC_NT98566_IPG-N4C-WQ2_S38`), outside
the Sentinela runtime, and the driver's camera-write allow-list is unchanged.

- Status at that date: OpenIPC NT98566 is still "work in progress / experts
  only". UART recovery does not work on these boards; recovery is the mask
  ROM's Ethernet boot (`defib --nic`), verified only on NT98562.
- Order of work on the spare unit: photograph the board (flash chip, sensor,
  Wi-Fi, UART pads), back up the whole flash, boot a kernel from RAM over
  TFTP without writing flash, and only then consider a flashed image.
- Stock-upgrade route: the main program `App` checks an RSA `Mx8Q` value on
  each upgrade package, using a key read from
  `/usr/bin/Squirrel/rs485/TransparentBase`. If that key cannot be loaded the
  check is skipped (fail-open). A modified package on the production camera
  is therefore expected to be rejected while the key file is present, but this
  is not yet tested; test it only on a spare unit.
  Production upgrades stay forbidden (`docs/CAMERA.md` §1.3).
- Novatek's SDK blobs are binary-only and their redistribution rights are
  unknown; kernel/U-Boot are GPL and belong in a separate repository, not in
  this MIT one.
- 2026-11-06: static reading of the vendor package (no execution, no network,
  no camera contact) is done; the detailed results are kept in the
  maintainers' notes. Any further analysis stays static until a spare unit
  is backed up.

Sources: the maintainers' research notes (not part of the public repository).
