# Camera: IPC_NT98566 (XMEye / Xiongmai)

Address: `192.168.1.20` (LAN). This file holds everything we know. Only
commands listed here may be sent by the DVRIP driver.

Measurements and firmware/config observations are dated evidence. They do
not imply a fresh hardware check or completion of the hardening checklist.
Current deployment uses go2rtc 1.9.14; older transport tables retain the
version measured at their original date.

## 1. Known facts (from v1 Sentinela hardware work, 2026-07)

| Property | Value | Source |
|---|---|---|
| Model string | `IPC_NT98566_IPG-N4C-WQ2_S38` | DVRIP SystemInfo |
| SoC | Novatek NT98566 | model string |
| Main stream | **H.265 only**, 2560×1440 | ONVIF + ffprobe |
| Audio | G.711 A-law (PCMA) 8 kHz mono | ONVIF + ffprobe |
| ONVIF | Profile T media source; `SetUser` unsupported (password change fails) | v1 KNOWN_ISSUES #12 |
| Control | PTZ only via DVRIP TCP 34567 (ONVIF PTZ not used) | v1 IPC_NT98566_PTZ.md |
| Vendor client | VideoPlayTool desktop 4.0.3.3 (`Bin\VideoPlayToolVersion.ini`), web component 5.1.7.5 (`Web\WebVersion.txt`, modified 2026-11-06); `Bin\VideoPlayTool.exe` not Authenticode-signed; never executed by this project. Hashes and bundled components in §7.10 | v1; PC inventory 2026-11-06 |

### 1.1 RTSP URLs (XMEye convention)

```
rtsp://<host>:554/user=<u>&password=<p>&channel=1&stream=0.sdp?real_stream   # main
rtsp://<host>:554/user=<u>&password=<p>&channel=1&stream=1.sdp?real_stream   # sub
```

The ONVIF `GetStreamUri` result is authoritative; prefer it when it works.

### 1.2 DVRIP protocol essentials

- TCP 34567, 20-byte header: `0xFF` magic, version, session id (LE u32),
  sequence (LE u32), total/cur packet, message id (LE u16), payload length
  (LE u32); JSON payload terminated by `\n\0`.
- Message ids: `1000` login, `1006` keepalive, `1020` SystemInfo,
  `1042` get config, `1040` set config, `1360` ability, `1400` PTZ,
  `1430`–`1434` talk (`OPTalk` claim/start/data/stop) — verify in audit.
- Login hash: XMEye "Sofia" MD5 (8-char folded MD5 of password).
  Newer firmwares may accept `EncryptType: "MD5"` only.

### 1.3 Allow-listed DVRIP operations

| Operation | Command / field | Notes |
|---|---|---|
| Move | `OPPTZControl` `Command: Direction{Up,Down,Left,Right,LeftUp,LeftDown,RightUp,RightDown}` | `Preset: 65535` to start, same command with `Preset: -1` to stop; `Step` = speed 1–8; max 1000 ms |
| Zoom/focus/iris | `ZoomWide/ZoomTile`, `FocusFar/FocusNear`, `IrisLarge/IrisSmall` | same start/stop pattern |
| Presets | `SetPreset`, `ClearPreset`, `GotoPreset` (1–255) | set/clear need confirmation |
| White light | `Camera.Param.[0].WhiteLight` (0/1) | read full object → modify → read back → restore on failure |
| Day/night | `Camera.Param.[0].DayNightColor` | `"0x00000000"`=auto, `"0x00000001"`=color, `"0x00000002"`=black_white, `"0x00000003"`=intelligent (v1 physical canary, 2026-07) |
| Talk | go2rtc `dvrip://…?backchannel=1` | Phase 3 uses go2rtc's backchannel; the browser mic needs a secure context (HTTPS) |

Message ids used by the driver: `1000` login, `1006` keepalive, `1020`
SystemInfo, `1042` get config, `1040` set config, `1400` PTZ. Reads used:
`SystemInfo`, `Camera.Param.[0]`, `Uart.PTZPreset` (list of `{Id, Name}`),
`NetWork.Nat` (only `NatEnable`, for the System page's cloud/P2P warning;
never written — network config stays forbidden).

Alarm channel (read-only, hardware probe only so far): message `1500`
`{"Name": "", "SessionID": …}` subscribes this session to alarm pushes and
answers `Ret 100` (verified 2026-09-29); keepalive `1006`
`{"Name": "KeepAlive", …}` every 10 s keeps it open. Pushes are expected as
message `1504` (`AlarmInfo`: `Channel`, `Event`, `StartTime`, `Status`, per
python-dvr) — **event names not observed yet**; do not map them in code
until they are recorded here.

PTZ request envelope (message `1400`), verified by v1 on this model:

```json
{"Name": "OPPTZControl", "SessionID": "0x0000000A",
 "OPPTZControl": {"Command": "DirectionLeft", "Parameter": {
   "AUX": {"Number": 0, "Status": "On"}, "Channel": 0, "MenuOpts": "Enter",
   "POINT": {"bottom": 0, "left": 0, "right": 0, "top": 0},
   "Pattern": "SetBegin", "Preset": 65535, "Step": 4, "Tour": 0}}}
```

- Held movement: repeat the start command every ≤ 400 ms; stop with the same
  command and speed and `Preset: -1`. The driver stops automatically if no
  keepalive arrives for 1 s.
- Presets: `SetPreset`/`ClearPreset`/`GotoPreset` with `Preset: <id>`, `Step: 0`.
- **Historical mount/config observation: flipped + mirrored** (`PictureFlip=1`,
  `PictureMirror=1`), so pan and tilt are inverted: a UI "left" sends
  `DirectionRight`, "up" sends `DirectionDown`. Zoom/focus/iris are not inverted.
  Each axis is configurable (`invert_pan`, `invert_tilt`, both default to
  `invert_pan_tilt`): on 2026-09-30 the owner reported "up" moving down,
  so the household mapping now has `invert_tilt: false` while retaining
  pan inversion. The feedback establishes the needed control mapping; the
  firmware's current flip/mirror flags were not re-read to confirm a change.
- `Ret` 100 = OK; 515 = OK (upgrade-available variant) for login.

**Forbidden:** firmware upgrade, `Alarm.AlarmOut`, network config, user
management, factory reset, native tour writes, `OPMachine` (reboot) unless an
explicit admin action with confirmation is added later.

## 2. Audit procedure

Run from any Linux/macOS machine on the camera LAN (or WSL on Windows):

```bash
./scripts/camera-audit.sh 192.168.1.20 [username]
```

It is **read-only**: port scan of common XMEye ports, HTTP headers, ONVIF
`GetSystemDateAndTime` (unauthenticated) + `GetDeviceInformation`,
`GetProfiles`, `GetStreamUri`, `GetCapabilities` (authenticated),
`ffprobe` of main and sub streams, and DVRIP `SystemInfo`/`SystemFunction`
reads. Output goes to `audit-out/` (git-ignored). Review it, remove serial
numbers/MACs, then paste the summary into §3.

Tools needed: `curl`, `ffprobe` (ffmpeg), `nmap` (optional), `python3`.

## 3. Audit results (2026-09-28, `scripts/camera-audit.sh` from Windows/Git Bash)

Serial number, HardwareId and MAC redacted. All probes were read-only.

| Check | Result |
|---|---|
| Firmware version / build date | `V5.00.R02.000639N0.10010.140017.0000000` (ONVIF reports `…S.ONVIF 21.06`), built 2023-07-13 |
| Open TCP ports | 80 (HTTP), 554 (RTSP, `H264DVR rtspd 1.0`), 8899 (ONVIF), 34567 (DVRIP). Filtered/closed: 23, 443, 8000, 8080, 9527, 9530, 34568, 34599 |
| Telnet (23) / debug (9527) open? | **No**, both filtered |
| Web UI (80) auth type | No HTTP auth on `/` (`HTTP/1.0 200`, no `WWW-Authenticate`); login happens in the page/plugin |
| ONVIF version / profiles | Port **8899**, service version 16.12 (imaging 17.12), media1 + media2. Profiles `000` (main), `001` (sub), `002` |
| Main stream codec / res / fps / bitrate / GOP | H.265 Main 2560×1440. Configured: 15 fps, VBR 1920 kbit/s, GOP 2 s, quality 6. Measured over 10 s (daytime): **12 fps, ~1550 kbit/s** video, keyframe every **2.00 s** |
| Sub stream codec / res / fps / bitrate | H.265 Main **800×448** (config says "D1"). Configured: 10 fps, VBR 320 kbit/s. Measured: 10 fps, ~495 kbit/s, keyframe every 2.00 s |
| Audio codec in RTSP | PCMA (G.711 A-law) 8 kHz mono, 64 kbit/s, on both streams |
| ONVIF events (motion/human) available | Yes: PullPoint + subscription. Topics include `RuleEngine/CellMotionDetector/Motion`, `VideoSource/MotionAlarm`, `RuleEngine/TamperDetector/Tamper`, `RuleEngine/LineDetector/Crossed`, `RuleEngine/FieldDetector/ObjectsInside`. Human detection only through DVRIP (`AlarmFunction.HumanDection`), not a dedicated ONVIF topic |
| ONVIF PTZ service present | Yes (`ptz_service`, ver20). Not used, PTZ stays on DVRIP (§1.3) |
| Camera NTP / timezone | NTP enabled, TZ `BRT3` (UTC−3, no DST); clock correct at audit time |
| Cloud (XMEye P2P) enabled | **Yes**: `NetWork.Nat.NatEnable=true`, server `secu100.net:8765` |
| Two concurrent RTSP pulls stable (10 min) | **Yes**: 2× `ffmpeg -c copy` on main for 600 s, 7201 video frames each (12.0 fps), no drops, no reconnects |

Other findings:
- ONVIF `GetStreamUri` returns the XMEye form
  `rtsp://<host>:554/user=<u>_password=<p>_channel=0_stream=<0|1>&onvif=0.sdp?real_stream`.
  The §1.1 form (`&`-separated, `channel=1`) also works and is what go2rtc uses.
- The DVRIP `EncryptType: "MD5"` Sofia hash login works (`Ret=100`); a wrong
  password answers `Ret=203`.
- ONVIF `GetDeviceInformation` answers **without valid credentials** (seen
  2026-09-29 with a wrong password): it does not prove the password; the
  wizard relies on the DVRIP login and the RTSP pulls for that.
- ONVIF `PullMessages` **ignores `Timeout`**: with nothing to report it
  answers an empty response at once (seen 2026-09-30). Pulled back-to-back
  (~45 requests/s) the camera dropped ~20 % of its video frames on every
  RTSP stream, keyframes included (smeared/black live view and recordings).
  The event watcher waits ≥ 1 s between pulls.
- WS-Discovery: the camera answers a Probe unicast to the sender's port and
  repeats the answer to `239.255.255.250:3702` (the server's ufw drops the
  unicast copy; Sentinela listens on the group).
- `NetWork.NetCommon` holds a wired static config `192.168.1.10/24`, gateway
  `192.168.1.1`, which does not match the address the camera answers on
  (`192.168.1.20`). The camera is probably on Wi-Fi/DHCP; confirm, and set a
  DHCP reservation on the router.
- The PCMA audio track occasionally repeats a DTS (`non monotonically increasing
  dts`, ~70 warnings per 10 min). Harmless for the null muxer; the Phase 2
  recorder must check that the mp4 segment muxer tolerates it (copy-only).
- **RTSP session limit: 3.** A 4th concurrent RTSP client is refused with
  `Operation not permitted` (seen 2026-09-28). Budget: recorder + go2rtc main +
  go2rtc sub. Never point other tools (VLC, NVR apps) at the camera while
  Sentinela runs; use go2rtc's restream instead.
- Timestamps: besides repeated PCMA DTS, the video DTS occasionally jumps
  back ~1 s. ffmpeg corrects it; the recorder uses `-af aresample=async=1`
  on the (already re-encoded) audio to keep A/V sync.
- Motion/human detection regions (read 2026-09-28): motion is enabled, human
  detection is enabled with one region around the gate only, so people on
  the street do not raise events. Adjust regions in the camera's own UI.
- `NetWork.NetCommon.TCPMaxConn = 10` is the upper bound for concurrent DVRIP/RTSP clients.
- `Camera.Param.DayNightColor = 0x00000000` in the current (auto) mode; the
  remaining int ↔ mode mapping is still to be done in Phase 3.
- `SupportCloudUpgrade`/`SupportCfgCloudupgrade` = true: the firmware can
  self-update from the cloud. Blocking WAN also blocks this, which is intended.
- UPnP is supported (`NetUPNP`); its state was not read (not in the audit's
  read list), so check it in the web UI.

## 4. Hardening checklist for the camera

- [ ] Change default `admin` password (via vendor web UI; ONVIF `SetUser` fails).
- [ ] Create a separate low-privilege user for Sentinela if firmware allows.
- [ ] Disable cloud/P2P ("Cloud"/"XMEye" in network settings).
- [ ] Disable UPnP.
- [ ] Set NTP to the server's IP `192.168.1.10` (chrony already serves the
      LAN) and keep TZ `BRT3`.
- [ ] Router: block all WAN traffic from the camera's IP/MAC.
- [ ] Advanced → Upgrade: untick "Automatic firmware upgrade" (and "Prompt for
      the latest updates"). Both were ticked in the web UI on 2026-11-06
      (owner screenshot); the camera can replace its own firmware from the
      vendor cloud. Not changed by the agent.
- [ ] If telnet/9527 is open: note it; do not use; rely on network isolation.

## 5. Measurements (Phase 1)

| Metric | Value |
|---|---|
| Live latency LAN, WebRTC (Chrome) | not measured yet (stopwatch method needs an operator); WebRTC jitter buffer ≈ 560 ms |
| Live latency 4G via Tailscale | **~0.8 s**, WebRTC, delay meter "sem atraso" (2026-09-29, Android Chrome on a Poco X7, Wi-Fi off). Tailscale path **direct** (no DERP relay) from the carrier IP; HTTPS via `tailscale serve` |
| go2rtc CPU / RSS | Dev PC (Ryzen 5 5600G, Docker Desktop), 1 viewer on `cam1` WebRTC: 4–6 % CPU, 24 MiB. Idle/sub only: 1–2 %, 19 MiB. **Re-measure on the i5 server.** |
| Recorder CPU / RSS | Phase 2 |

### 5.1 Browser transports (go2rtc 1.9.10, 2026-09-28, dev PC on LAN)

| Browser | Stream | Transport | Codecs negotiated | Result |
|---|---|---|---|---|
| Chromium 152 (Claude desktop pane, Windows) | `cam1` | WebRTC | H265 (profile 1, level 180) + PCMA | OK, 2560×1440, 12 fps, 0 dropped |
| Chromium 152 | `cam1_sub` | WebRTC | H265 + PCMA | OK, 800×448, 10 fps |
| Chromium 152 | `cam1` | MSE | `hvc1.1.6.L153.B0` + **flac** | OK, ~0.9 s behind buffer end |
| Chromium 152 | `cam1_sub` | MSE | `hvc1` + flac | OK, ~1.0 s behind buffer end |
| Edge / Chrome desktop, Firefox, Safari, Android Chrome | — | — | — | to test |

MSE audio: go2rtc converts PCMA → FLAC in-process for MSE (browsers cannot
play PCMA in MP4). Video is untouched. This is a tiny audio re-encode inside
go2rtc, not a video transcode, but it is not copy-only; see PLAN Phase 1.

**Live latency (server, 2026-09-28):** camera → go2rtc → Chrome WebRTC is
~0.8 s end to end; Chrome's jitter buffer holds ~0.4 s. The camera's OSD
clock is **not** a latency reference: the camera ran 4.5 s behind the
NTP-synced server (camera NTP not set) and the owner's PC 15.7 s ahead, which
looked like "20 s of delay" next to a PC-time clock. The UI clock now follows
the server's. Opening the view waits ≤ 2 s for a keyframe; go2rtc `preload`
keeps `cam1` connected so the RTSP handshake is not added on top.
The live view's delay meter reads the camera clock with ONVIF
`GetSystemDateAndTime` (device service, port 8899, read-only; whole seconds)
and shows what the OSD should read with zero delay.

**Clock check (2026-10-10):** the owner's screenshot showed 23:11:03 on
Windows, 23:10:56 in the server-synchronized UI and 23:10:53 burned into
the picture. Initial SSH/ONVIF measurements confirmed Windows ~5.8 s ahead
of the NTP server and the camera ~3 s behind; later Windows samples were
aligned without agent changes, while camera skew remained ~2.6 s (±0.5 s).
The active LAN player used MSE with ~0.6–1.6 s buffered, and a 20 s copy-only
RTP sample via go2rtc had no missing packets, fixed 12 fps and 2 s keyframes,
with no accumulating arrival delay. Compare the OSD with the corrected
delay-meter clock, not Windows or the uncorrected header. This sample does
not establish long-term artifact-free operation or current 4G latency.

## 6. Later operational corrections (2026-09-30)

- This camera ignores the ONVIF PullMessages timeout. The earlier watcher
  could pull about 45 times/second, with frame loss observed during the
  affected 2026-09-29 recording window. The watcher now spaces pulls at
  least one second apart. Existing recorded gaps are not repaired by that
  change; the recorded investigation is in PLAN round 5.
- **ONVIF subscription address (2026-11-06):** since 2026-11-03 23:27 UTC
  `CreatePullPointSubscription` answers `SubscriptionReference/Address`
  with the unused wired static address (`http://192.168.1.10:8899/event_service/N`)
  instead of the address it answers on. Every PullMessages to it timed out,
  so no motion event (and no person detection, which waits for motion) was
  recorded for three days. The client now keeps only the path of a
  device-reported service address and uses the configured host/port. Read-only;
  no camera setting was changed.
- The camera answers `Renew` with `ResourceUnknownFault` (seen 2026-11-06).
  The watcher keeps pulling until 15 s before the 120 s subscription expires,
  then subscribes again at once, so events have no backoff gaps.
- While the camera moves under PTZ (and 3 s after; 15 s after a preset
  goto) its own motion opens no event, snapshot, detection or notification.
- PTZ direction correction uses separate pan/tilt config overrides. No
  movement or persistent camera write is part of the APK TLS/doc fix.
- Timeline preview frames are copied from recording keyframes and decoded
  on the viewing device. The measured substream JPEG/detector exceptions are
  ADR-0015/0016. A preview's local response time is not a fresh 4G latency
  measurement.
- Phone acceptance still covers current playback, previews, PTZ, talk and
  notifications. Do not replace that check with an old transport benchmark.
