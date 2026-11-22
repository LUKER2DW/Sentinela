# Changelog

All notable changes to Sentinela. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and versions follow
[Semantic Versioning](https://semver.org/). Releases are described in
[docs/RELEASING.md](docs/RELEASING.md).

## [Unreleased]

### Security
- The ONVIF device path accepts only letters, digits and `/ . _ ~ -`, and is
  always joined after the camera's own address, so a typed path can never
  send a request elsewhere (CodeQL go/request-forgery).
- The detector image installs its Python packages from hash-locked files
  (`detector/requirements*.txt`, `--require-hashes`), CPU PyTorch included;
  CI installs the same lock. `gomobile` is pinned to the module's version.
- Fuzz tests for everything parsed from a camera (ONVIF replies, preset
  numbering, stream addresses, DVRIP packets).
- SECURITY.md links the private vulnerability report form directly.

### Fixed
- A stream address reporting port 0 or above 65535 over ONVIF falls back to
  554 (found by the new fuzz tests).

## [1.7.0] - 2026-11-22

Any camera brand, set up camera by camera: ONVIF PTZ, an ONVIF switch and
address per camera, more brand presets, ONVIF fixes and a weekly CVE scan.
Update with the usual `./scripts/deploy.sh`; read **Changed** first.

### Added
- **PTZ for any brand over ONVIF.** A camera that is not XMEye moves, zooms,
  saves and visits positions and runs tours over ONVIF PTZ once *Has PTZ* is
  ticked (focus stays XMEye-only). The camera is asked to stop by itself
  within 2 s if Sentinela goes away mid-move.
- **Each camera's capabilities are set in System → Cameras:** PTZ for every
  brand, *Uses ONVIF* (off for an RTSP-only camera: no ONVIF errors, motion
  from the video), the ONVIF port and an optional ONVIF address
  (`onvif`, `onvif_path` in `config.yaml`). *Find the streams over ONVIF*
  also ticks *Has PTZ* when the camera reports PTZ.
- Stream presets for Reolink, TP-Link Tapo/VIGI, Uniview and Axis, with
  their usual ONVIF ports; a `{channel2}` (two-digit) path placeholder.
- CI: weekly CVE scan (govulncheck, npm audit, OSV-Scanner over every
  lockfile and the server image's Debian packages), OpenSSF Scorecard on the
  public repository and actionlint for the workflow files. Dependabot waits a
  week before proposing a new version and also covers the Android build.

### Fixed
- ONVIF motion and tamper events on brands whose event service is not at
  `/onvif/event_service` (Hikvision, Axis and others): Sentinela now asks
  the camera where its services are instead of assuming the XMEye/Dahua path.
- ONVIF requests to a camera whose clock is minutes off are signed in the
  camera's time, so they are no longer refused as stale.
- ONVIF to a camera at an IPv6 address (the URL lacked the brackets).
- A camera added from the dashboard as "Any camera" was saved with PTZ and
  light on; new cameras start without them, and changing the brand resets
  them to the brand's defaults.
- The notices no longer ask cameras without DVRIP for their cloud setting
  (one refused request per camera every few minutes).

### Security
- Go dependencies: `golang.org/x/crypto` 0.59.0 (drops the module-level
  GO-2026-5932 advisory), `golang.org/x/sys` 0.50.0, `modernc.org/sqlite`
  1.63.0; web and app packages updated within their ranges (no known
  vulnerabilities). The optional tailnet module (`mobile/tailnet`):
  `klauspost/compress` 1.20.2 (GO-2026-6865), `x/net` 0.62.0. CI analysis tools updated (staticcheck 2026.2.2,
  govulncheck 1.9.0).

### Changed
- A camera of another brand whose `config.yaml` says `features: {ptz: true}`
  (cameras added from the dashboard before this version were saved that
  way; it did nothing then) now shows the PTZ pad. For a fixed camera,
  untick *Has PTZ* in System → Cameras.

## [1.6.0] - 2026-11-19

Security release: the result of a full audit of the server, the remote-access
paths, the services agent and the host scripts. Update with the usual
`./scripts/deploy.sh`; read **Changed** first.

### Security
- **Login from outside can no longer be used to lock the owner out.** Failed
  outside logins count per account and per source (10 an hour), with a
  300-an-hour backstop across sources for a guess spread over many
  addresses. The pre-login limiters cap their memory and count an IPv6
  client per /64; password hashing has a short, bounded queue and answers
  "busy" (503) instead of piling up; failed-login audit rows are capped and
  pruned after 180 days.
- **A session is tied to the host it was created on.** A cookie that a
  service on another port of the same address received can no longer be
  replayed at the public address.
- A tunnel or reverse proxy that forwards a public client to the dashboard
  port (instead of `remote.proxy.listen`) is treated as the public zone, and
  the reachability check reports that mistake.
- Anonymous requests on public listeners have read and write deadlines, and
  the listeners facing the internet cap open connections. Secure cookies and
  HSTS in every public zone; logged request paths are one line and bounded;
  the build version is not sent to the internet.
- Live event, notification and video streams re-check the session every
  45 seconds; revoking a device, disabling an account or narrowing a user's
  cameras now reaches streams that are already open.
- Camera probes and the services' file, secret and folder views are
  LAN/tailnet only. `/api/system` shows only the cameras a user may see (and
  the share and disk details only to admins). Web Push subscriptions end
  with the session that created them.
- **Self-hosted services:** the helper that holds the Docker socket gets a
  fixed environment and runs `docker` by absolute path; the rendered Compose
  file lives where no service can write; source inspection, folder browsing
  and the disk sampler are bounded, so a hostile repository or folder cannot
  take the agent down. Builds that run outside the services firewall are
  flagged (limits documented in ADR-0021).
- **Host:** the recorder, which parses camera data, mounts only the
  recordings, the database folder and the volume marker, not all of `/data`.
  The root config applier re-checks every dashboard change
  (`sentinela check-config -against`), and the setup, deploy and backup
  scripts no longer follow symlinks under `/data` or read the database as
  root. `firewall.sh` always turns the firewall back on.
- Camera stream paths reject `${…}` (go2rtc would expand it from
  `secrets.env`); export and timelapse release their slot when the client
  stops reading; prepared timelapses are capped.
- Releases are made only from a plain `vX.Y.Z` tag whose commit is on `main`.
  Go 1.26.11 (GO-2026-6859, an HTTP/1 server crash).
- The Android app has no cleartext exception and its file provider exposes
  only the update APK.

### Changed
- Someone signed in at the home address who opens the public address must
  sign in again there (sessions are per host).
- From the internet, the Services page shows status and logs and can start,
  stop, restart or redeploy; files, secrets and folders need home or the
  tailnet.
- New Samba installs need a password of at least 8 characters and lock an
  account for 30 minutes after 5 wrong ones.
- Two-step login stays optional per account; INSTALL.md and the examples now
  say so, and add router and SSH checks for public access.

## [1.5.1] - 2026-11-08

### Fixed
- Live view converted in the browser could stay black ("H.265 → H.264
  here" with no picture): it said "live" before the first converted frame,
  so the stall watchdog restarted it every few seconds, before the first
  (slower) frames of a large main stream were ready. It now shows
  "connecting" until a converted frame is there, and when none arrives in
  15 seconds (or the browser's encoder refuses the size), it moves on to
  the substream.
- The H.265 fallback's console log no longer includes text from the video
  server (CodeQL). golang.org/x/net 0.62.0.

## [1.5.0] - 2026-11-08

### Added
- **H.265 in any browser** (ADR-0026): when the browser cannot decode
  H.265, the live view and the recordings are converted to H.264 in the
  browser itself (WebAssembly, hevc.js), only for viewing. The server keeps
  recording and serving H.265 exactly as before. Needs HTTPS (Tailscale,
  the public address or the app); a tag on the picture says when it is
  converted.
- When converting is not possible (plain `http://`, a slow device, too
  many cameras at once), the live view uses the camera's H.264 substream,
  and says what to change when there is no H.264 at all.

### Fixed
- The live view no longer refuses a camera in a browser without H.265
  before trying: cameras that send H.264 play there.

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

[Unreleased]: https://github.com/LUKER2DW/Sentinela/compare/v1.7.0...HEAD
[1.7.0]: https://github.com/LUKER2DW/Sentinela/compare/v1.6.0...v1.7.0
[1.6.0]: https://github.com/LUKER2DW/Sentinela/compare/v1.5.1...v1.6.0
[1.5.1]: https://github.com/LUKER2DW/Sentinela/compare/v1.5.0...v1.5.1
[1.5.0]: https://github.com/LUKER2DW/Sentinela/compare/v1.4.0...v1.5.0
[1.4.0]: https://github.com/LUKER2DW/Sentinela/compare/v1.3.0...v1.4.0
[1.3.0]: https://github.com/LUKER2DW/Sentinela/compare/v1.2.2...v1.3.0
[1.2.2]: https://github.com/LUKER2DW/Sentinela/compare/v1.2.1...v1.2.2
[1.2.1]: https://github.com/LUKER2DW/Sentinela/compare/v1.2.0...v1.2.1
[1.2.0]: https://github.com/LUKER2DW/Sentinela/compare/v1.1.1...v1.2.0
[1.1.1]: https://github.com/LUKER2DW/Sentinela/compare/v1.1.0...v1.1.1
[1.1.0]: https://github.com/LUKER2DW/Sentinela/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/LUKER2DW/Sentinela/releases/tag/v1.0.0
