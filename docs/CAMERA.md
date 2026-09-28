# Camera: IPC_NT98566 (XMEye / Xiongmai)

Address: `192.168.1.20` (LAN). This file holds everything we know. Only
commands listed here may be sent by the DVRIP driver.

## 1. Known facts (from v1 Sentinela hardware work, 2026-07)

| Property | Value | Source |
|---|---|---|
| Model string | `IPC_NT98566_IPG-N4C-WQ2_S38` | DVRIP SystemInfo |
| SoC | Novatek NT98566 | model string |
| Main stream | **H.265 only**, 2560×1440 | ONVIF + ffprobe |
| Audio | G.711 A-law (PCMA) 8 kHz mono | ONVIF + ffprobe |
| ONVIF | Profile T media source; `SetUser` unsupported (password change fails) | v1 KNOWN_ISSUES #12 |
| Control | PTZ only via DVRIP TCP 34567 (ONVIF PTZ not used) | v1 IPC_NT98566_PTZ.md |
| Vendor client | VideoPlayTool 4.0.3.3 (unsigned exe; never executed) | v1 |

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
`SystemInfo`, `Camera.Param.[0]`, `Uart.PTZPreset` (list of `{Id, Name}`).

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
- **This unit's image is flipped + mirrored** (`PictureFlip=1`,
  `PictureMirror=1`), so pan and tilt are inverted: a UI "left" sends
  `DirectionRight`, "up" sends `DirectionDown`. Zoom/focus/iris are not inverted.
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
- The DVRIP `EncryptType: "MD5"` Sofia hash login works (`Ret=100`).
- `NetWork.NetCommon` holds a wired static config `192.168.1.10/24`, gateway
  `192.168.1.1`, which does not match the address the camera answers on
  (`192.168.1.20`). The camera is probably on Wi-Fi/DHCP; confirm, and set a
  DHCP reservation on the router.
- The PCMA audio track occasionally repeats a DTS (`non monotonically increasing
  dts`, ~70 warnings per 10 min). Harmless for the null muxer; the Phase 2
  recorder must check that the mp4 segment muxer tolerates it (copy-only).
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
- [ ] Set NTP to the server's IP (run chrony on the server) and correct TZ.
- [ ] Router: block all WAN traffic from the camera's IP/MAC.
- [ ] If telnet/9527 is open: note it; do not use; rely on network isolation.

## 5. Measurements (Phase 1)

| Metric | Value |
|---|---|
| Live latency LAN, WebRTC (Chrome) | not measured yet (stopwatch method needs an operator); WebRTC jitter buffer ≈ 560 ms |
| Live latency 4G via Tailscale | not measured (no Tailscale / server yet) |
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
