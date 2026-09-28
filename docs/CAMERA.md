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
| Day/night | `Camera.Param.[0].DayNightColor` | map firmware ints ↔ `auto/color/black_white/intelligent` after audit |
| Talk | `OPTalk` claim → start → PCMA frames → close | one owner per camera, 60 s hard limit |

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

## 3. Audit results (fill in after Phase 0 audit)

| Check | Result |
|---|---|
| Firmware version / build date | |
| Open TCP ports | |
| Telnet (23) / debug (9527) open? | |
| Web UI (80) auth type | |
| ONVIF version / profiles | |
| Main stream codec / res / fps / bitrate / GOP | |
| Sub stream codec / res / fps / bitrate | |
| Audio codec in RTSP | |
| ONVIF events (motion/human) available | |
| ONVIF PTZ service present | |
| Camera NTP / timezone | |
| Cloud (XMEye P2P) enabled | |
| Two concurrent RTSP pulls stable (10 min) | |

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
| Live latency LAN, WebRTC (Chrome) | |
| Live latency 4G via Tailscale | |
| go2rtc CPU / RSS | |
| Recorder CPU / RSS | |
