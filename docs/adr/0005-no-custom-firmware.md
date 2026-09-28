# ADR-0005: Do not replace the camera firmware (for now)

Status: Accepted · 2026-09-28

SoC Novatek NT98566 is "work in progress" in OpenIPC. Flashing risks a brick
and loss of PTZ/IR/audio drivers. We isolate the camera on the network instead
and control it via DVRIP/ONVIF. Revisit with a spare unit + UART adapter.
