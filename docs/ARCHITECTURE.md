# Architecture

## 1. Components

| Component | Runs as | Responsibility |
|---|---|---|
| `sentinela` | container, `sentinela serve` | API, auth, SPA hosting, stream proxy, camera control (DVRIP), ONVIF events, nightly DB backup |
| `recorder` | container, `sentinela record` (same image, ADR-0007) | ffmpeg per camera, segment indexer, retention, heartbeat |
| `go2rtc` | container (`alexxit/go2rtc`, pinned) | RTSP ingest, WebRTC/MSE/HLS/MJPEG out, two-way audio, snapshots |
| Tailscale | host package | Private remote access; MagicDNS name for the server |

All containers use `network_mode: host` (simplest for WebRTC ICE on LAN) but
bind only to the addresses listed in §4.

## 2. Data flows

### 2.1 Live

```
browser ──GET /api/cameras/1/stream-ticket──► sentinela (auth check)
browser ──WS /stream/ws?src=cam1&ticket=…───► sentinela ──proxy──► go2rtc :1984 (loopback)
browser ◄════ WebRTC (UDP/TCP 8555) ═════════ go2rtc
```

go2rtc's API/WS port is loopback-only; the browser never talks to it without a
ticket validated by sentinela. WebRTC ICE on 8555 is reachable on LAN and on
the Tailscale interface.

### 2.2 Recording

```
camera RTSP main ──► ffmpeg (-c copy, 300 s mp4 segments) ──► /data/recordings/cam1/2026/09/28/13-00-00.mp4
                                    │ close
                                    ▼
                         indexer (15 s scan + ffprobe) ──► SQLite segments
                         retention (every 5 min)     ──► delete oldest
```

The recorder pulls directly from the camera so a go2rtc crash cannot create a
gap. The camera accepts at most 3 RTSP sessions (recorder + go2rtc main +
go2rtc sub). If the camera can't serve two RTSP clients reliably (checked in Phase 1),
switch the recorder source to `rtsp://127.0.0.1:8554/cam1` (go2rtc) — ADR
required.

### 2.3 Control

```
browser ──POST /api/cameras/1/ptz {dir:"left",speed:4,ms:350}──► sentinela
sentinela ──DVRIP login → OPPTZControl start → sleep → stop(Preset:-1)──► camera:34567
```

One DVRIP session per camera, serialized by a mutex, closed after 30 s idle.
Held moves: the UI repeats `start` every 400 ms; the server stops the move if
no repeat arrives within 1 s (deadman), on release, or on disconnect.

### 2.4 Events

ONVIF PullPoint subscription (or DVRIP alarm callback) → `events` row
`(camera_id, type, start_utc, end_utc)` → timeline markers, optional push.

## 3. Storage layout

```
/data/
  recordings/<camera>/<YYYY>/<MM>/<DD>/<HH-MM-SS>.mp4   (UTC)
  db/sentinela.db           SQLite (WAL)
  backup/                   nightly VACUUM INTO copies
/etc/sentinela/
  config.yaml               non-secret config
  secrets.env               0600: camera usernames/passwords
```

## 4. Ports

| Port | Bind | Owner | Notes |
|---|---|---|---|
| 8080/tcp | LAN + tailscale0 | sentinela | HTTPS via Tailscale cert or plain HTTP on LAN |
| 1984/tcp | 127.0.0.1 | go2rtc API | never exposed |
| 8554/tcp | 127.0.0.1 | go2rtc RTSP | internal restream |
| 8555/tcp+udp | LAN + tailscale0 | go2rtc WebRTC | ICE |
| 22/tcp | LAN + tailscale0 | ssh | keys only |

Firewall (nftables/ufw): default deny inbound; allow the above; no port
forwarding on the router.

## 5. SQLite schema

Authoritative: `internal/store/migrations/`. Cameras are defined in
`config.yaml` (id = go2rtc stream name), not in the DB.

```sql
CREATE TABLE users(id INTEGER PRIMARY KEY, username TEXT UNIQUE NOT NULL,
  pw_hash TEXT NOT NULL, role TEXT NOT NULL CHECK(role IN('admin','operator','viewer')),
  created_at TEXT NOT NULL);
CREATE TABLE sessions(id_hash BLOB PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users,
  expires_at TEXT NOT NULL, csrf TEXT NOT NULL);
CREATE TABLE segments(id INTEGER PRIMARY KEY, camera_id TEXT NOT NULL,
  start_utc TEXT NOT NULL, end_utc TEXT NOT NULL, path TEXT UNIQUE NOT NULL,
  bytes INTEGER NOT NULL, protected INTEGER NOT NULL DEFAULT 0);
CREATE INDEX segments_cam_start ON segments(camera_id, start_utc);
CREATE TABLE events(id INTEGER PRIMARY KEY, camera_id TEXT NOT NULL,
  type TEXT NOT NULL, start_utc TEXT NOT NULL, end_utc TEXT, meta TEXT);
CREATE TABLE audit(id INTEGER PRIMARY KEY, at TEXT NOT NULL, user_id INTEGER,
  action TEXT NOT NULL, target TEXT, ok INTEGER NOT NULL);
```

## 6. Disk recommendation

Put `/data/recordings` on its own partition/disk (ext4, `noatime`,
`commit=60`). If the OS disk and recording disk are the same physical HDD,
set `vm.dirty_background_bytes=16M` to smooth writes.
