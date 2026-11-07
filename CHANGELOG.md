# Changelog

All notable changes to Sentinela. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and versions follow
[Semantic Versioning](https://semver.org/). Releases are described in
[docs/RELEASING.md](docs/RELEASING.md).

## [Unreleased]

The first public release. Highlights of what is in it:

### Cameras and live view
- Any number of cameras: XMEye/Xiongmai (with PTZ, presets, white light,
  day/night and two-way audio), Hikvision, Dahua, or any RTSP camera with
  its own stream paths. go2rtc's stream list is generated from `config.yaml`.
- Low-latency WebRTC live view with MSE fallback, grids and full screen.
- Preset tours: the camera visits chosen positions until stopped, until its
  time is up, or until someone takes manual control.

### Recording and review
- 24/7 copy-only recording in fragmented MP4, with a supervised recorder
  that keeps going if the rest of the stack stops.
- Recording pauses instead of filling the system disk when the recordings
  disk is not mounted.
- Timeline with previews, exports, timelapses, protected clips.
- Events from ONVIF and an optional object detector on sub-stream
  keyframes, with zones, a review queue and snapshots.

### Accounts, access and the app
- First-run setup guide in the browser.
- Roles, per-camera visibility, invites, optional TOTP two-step login,
  revocable sessions, audit log, closed-door public listener.
- One Android APK for every house, paired by a QR code that carries the
  server's address and certificate pin.

### Files and services
- Nuvem: SMB share and web file browser with uploads, previews and a
  recycle bin.
- Optional self-hosted services from GitHub or a Nuvem folder, capped and
  isolated; Compose files are checked before they run.

### Operations
- Nightly database and config backups, optional restic copy and a restore
  drill.
- Releases publish a server image with SBOM and provenance, a Linux binary
  and the signed APK; servers pick up new app versions from the latest
  release and offer them to the phones.

[Unreleased]: https://github.com/LUKER2DW/Sentinela/commits/main
