# ADR-0002: go2rtc as the only media server

Status: Accepted · 2026-09-28

## Decision
go2rtc (MIT) handles all live media and two-way audio. MediaMTX is not used.
Recording is done by ffmpeg `-c copy`, not by the media server.

## Why
Zero-transcode WebRTC incl. H.265, MSE/HLS fallback, native `dvrip://` source
with talk backchannel, tiny footprint (runs on Pi Zero). Running two media
servers (v1) doubled config and failure modes.

## Consequences
Pin image by digest; generate `go2rtc.yaml` from the DB; API loopback only.
