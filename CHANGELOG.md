# Changelog

All notable changes to Sentinela. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and versions follow
[Semantic Versioning](https://semver.org/). Releases are described in
[docs/RELEASING.md](docs/RELEASING.md).

## [Unreleased]

## [1.1.0] - 2026-11-08

### Added
- Motion from the video for cameras that send no motion events (RTSP only,
  or ONVIF events that stopped): the detector compares sub-stream keyframes,
  opens motion events and wakes object detection, about 2.3 % of one core per
  camera. Choose per camera: Motion detection Automatic, From the camera or
  From the video (`motion` in config.yaml).
- README: how motion and object detection work, the YOLO26n model and the
  exact versions used.

### Fixed
- A camera that stops answering control requests (for example after a burst of
  PTZ) is no longer hit by every queued request: the server pauses its logins
  for a few seconds (growing up to 30 s) and answers 503 meanwhile, and the
  live page sends one hold request at a time and stops with a message when it
  fails. Stopping a movement is always tried.
- Folder and message names left in Portuguese in the services agent and the
  Tailscale pairing are now in English.

### Security
- The app installs an update only if it is this app, signed with its own key,
  and not older than the installed version.
- Logs hide whole tokens and stream tickets (they could keep the part after an
  underscore).
- The app's WebView refuses content:// and file:// URLs, and addresses from
  pairing links are trimmed without a regular expression.
- App and snapshot paths can no longer leave their folders, whatever id
  reaches them.
- CI runs race tests, shellcheck, the image and APK builds, CodeQL (with a
  real Android build) and dependency review on every change.

## [1.0.0] - 2026-11-07

The first public release. Highlights of what is in it:

### Cameras and live view
- Any number of cameras: XMEye/Xiongmai (with PTZ, presets, white light,
  day/night and two-way audio), Hikvision, Dahua, or any RTSP camera with
  its own stream paths. go2rtc's stream list is generated from `config.yaml`.
- Low-latency WebRTC live view with MSE fallback, grids and full screen.
- Preset tours: the camera visits chosen positions until stopped, until its
  time is up, or until someone takes manual control.
- Each camera says what it has (PTZ, white light, speaker); the live page
  shows only those controls. Streams of ONVIF cameras are found in the editor.

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
- Settings edited from System: cameras (including credentials), recording,
  time zone, network names and disks, validated, backed up and rolled back
  on failure. My account now lives inside System on computers.
- Roles, per-camera visibility, invites, optional TOTP two-step login,
  revocable sessions, audit log, closed-door public listener.
- One Android APK for every house, paired by a QR code that carries the
  server's address and certificate pin. It has no Firebase project built in:
  each house's server hands it its own.
- English and Brazilian Portuguese, picked from the device.

### Files and services
- Cloud: SMB share and web file browser with uploads, previews and a
  recycle bin.
- Optional self-hosted services from GitHub or a Cloud folder, capped and
  isolated; Compose files are checked before they run.

### Operations
- Nightly database and config backups, optional restic copy and a restore
  drill.
- Releases publish a server image with SBOM and provenance, a Linux binary
  and the signed APK; servers pick up new app versions from the latest
  release and offer them to the phones.
- Optional automatic server updates from releases, daily in a chosen hour,
  with checksum checks and automatic rollback; System → Updates shows
  the schedule and these notes.

[Unreleased]: https://github.com/LUKER2DW/Sentinela/compare/v1.1.0...HEAD
[1.1.0]: https://github.com/LUKER2DW/Sentinela/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/LUKER2DW/Sentinela/releases/tag/v1.0.0
