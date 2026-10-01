# ADR-0005: Do not replace the camera firmware (for now)

Status: Accepted · 2026-09-28

At the recorded 2026-09-28 research, Novatek NT98566 was "work in progress"
in OpenIPC. This is a dated finding, not a current compatibility claim. Flashing risks a brick
and loss of PTZ/IR/audio drivers. We isolate the camera on the network instead
and control it via DVRIP/ONVIF. Revisit with a spare unit + UART adapter.
No custom camera firmware has been deployed by this project.
