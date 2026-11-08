# Changelog

All notable changes to Sentinela. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and versions follow
[Semantic Versioning](https://semver.org/). Releases are described in
[docs/RELEASING.md](docs/RELEASING.md).

## [Unreleased]

## [1.4.0] - 2026-11-08

### Added
- **Tailscale public link (Funnel)**: the Tailscale address also opens in
  any browser or phone without the Tailscale app, turned on from System →
  Remote access. Choose the port: 8443 (the address without a port stays
  private to your tailnet), 443 (the address itself becomes public) or
  10000. It gets the same protection as the other public ways in, is
  offered to the app's pairing codes, and *Test from the internet* checks
  it. If your tailnet policy does not allow Funnel yet, the page shows the
  rule to add.

### Changed
- The Tailscale address moved from Server and disks to the Tailscale card
  in Remote access.
- System pages adapt to the room they really have (the side menus take
  part of the screen): one wide column on mid-size screens, two side by
  side on large ones. Long options wrap instead of being cut.

## [1.3.0] - 2026-11-08

### Added
- **System → Remote access**: every way in from outside, set up and checked
  from the browser (also linked from the welcome guide).
  - **Tailscale**: step by step with links (account, MagicDNS and HTTPS
    certificates, auth key). Paste the auth key and the server installs
    Tailscale, joins your tailnet and publishes the panel over HTTPS; a
    live checklist shows what is missing, and one click saves the address.
  - **Public address**: domain or the home's fixed IP, the server's port,
    the router's port and the certificate, with the router and DNS steps
    for your values. The firewall opens exactly the chosen ports and closes
    the old ones.
  - **Tunnel or reverse proxy** (ADR-0025): your own domain through
    Cloudflare Tunnel (paste the tunnel's install command; the server
    installs and runs cloudflared) or any proxy on the server (Caddy, nginx,
    ngrok...). Everything that arrives through it gets the same protection
    as the public address. Works behind CGNAT, without opening ports.
  - *Test from the internet* checks that an address really reaches this
    server and explains DNS, router, certificate or "another server"
    problems, with the home's public IP.
- The app pairing codes and invite links also offer the tunnel's address.

### Changed
- System pages use the screen's width: cards side by side on wide screens,
  one column on narrow ones.
- "Check for updates" requests are watched with `PathExists`, which does
  not lose a request (1.2.2's 15-second safety-net timer is removed).

## [1.2.2] - 2026-11-08

### Added
- System → Updates shows a progress bar while an update installs (download,
  checks, image, install, restart), follows it every second, survives the
  server restart and offers to reload the page when it is done.

### Fixed
- *Check for updates* no longer stays on "Checking…" for minutes: a request
  the host missed is picked up within seconds (safety-net timer), a request
  older than two minutes stops counting as pending, and the check is about
  3 s faster (the server writes its own version instead of the updater
  starting a container to ask for it).
- The copy buttons (pairing code, addresses, invite link) did nothing on
  `http://` pages and inside the Android app, where the browser clipboard is
  not available; they now fall back to the older copy command and say when
  copying failed.

## [1.2.1] - 2026-11-08

### Added
- The app connects with just the home's address, no pairing link needed. For
  a home with its own certificate, the app reads the certificate the address
  presents (sending nothing) and shows its *home code*; once the person
  confirms it is the code shown in System → App, the certificate is saved
  with the address. Publicly trusted addresses connect straight away.
- Scanning the home's QR code in the app now shows the address and pairs on one
  tap (it was filled into the field first).
- System → App shows each address's home code next to its QR code.

## [1.2.0] - 2026-11-08

### Added
- System → Updates: *Check for updates* shows this server's version, the
  latest published one and the app offered to phones; *Update now* installs
  the latest release right away (also over a version deployed from a
  computer or a new major version: the person asked for it).
- The app for phones is checked every day at the chosen update time, and
  right away with *Check for updates* (it was every six hours).
- The Android app reads the home's pairing QR code from its start screen
  (Google Play services code scanner; no camera permission).

## [1.1.1] - 2026-11-08

### Fixed
- Updating from a household build (with the home's address and certificate
  built in) to the release APK could lose the certificate pin, so the app
  called the home's own certificate invalid. The app now saves the pin in use
  with its address on every launch, keeps it when an address is typed again,
  and falls back to the pin the home itself listed for that address.
- The no-connection screen explains a certificate error (pair again with the
  code from System → App) instead of suggesting a network problem.

### Added
- System → App also offers the home-network address (the HTTPS listener on
  the server's LAN address, with the same pin), for when the internet
  address does not open at home. A typed LAN address gets its pin from the
  home's list.

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

[Unreleased]: https://github.com/LUKER2DW/Sentinela/compare/v1.4.0...HEAD
[1.4.0]: https://github.com/LUKER2DW/Sentinela/compare/v1.3.0...v1.4.0
[1.3.0]: https://github.com/LUKER2DW/Sentinela/compare/v1.2.2...v1.3.0
[1.2.2]: https://github.com/LUKER2DW/Sentinela/compare/v1.2.1...v1.2.2
[1.2.1]: https://github.com/LUKER2DW/Sentinela/compare/v1.2.0...v1.2.1
[1.2.0]: https://github.com/LUKER2DW/Sentinela/compare/v1.1.1...v1.2.0
[1.1.1]: https://github.com/LUKER2DW/Sentinela/compare/v1.1.0...v1.1.1
[1.1.0]: https://github.com/LUKER2DW/Sentinela/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/LUKER2DW/Sentinela/releases/tag/v1.0.0
