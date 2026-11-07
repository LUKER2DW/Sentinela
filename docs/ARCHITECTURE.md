# Architecture

Current implementation: 2026-10-01. Dated measurements below remain evidence
from their original runs; they are not new hardware measurements.

## 1. Components

| Component | Runtime | Responsibility |
|---|---|---|
| `sentinela` | `sentinela serve` container | Auth, SPA/API, camera control, stream proxy, events, Nuvem, notification sending and APK feed |
| `recorder` | `sentinela record`, same image | Separate ffmpeg supervisor, indexing, retention and heartbeat |
| `go2rtc` | Pinned container | Live WebRTC/MSE, RTSP restream and talk backchannel |
| `detector` | Motion-gated sidecar, ADR-0016 | Substream keyframes, YOLO26n, zones and event snapshots; CPU capped at 0.15 core, memory 192 MB |
| `apps` | `sentinela apps`, same image, profile `apps` | Self-hosted services agent (ADR-0021): the only container with the Docker socket; loopback API with a token; 0.5 CPU, 128 MB |
| Tailscale | Host package | Optional private access/administration through HTTPS serve |
| Samba, wsdd, Avahi | Host packages | LAN shares/discovery, independent of the web user's role model |
| Host timers | systemd | SMART, local DB/config backup, optional restic and trash expiry |
| Firebase | Outbound service, ADR-0020 | An opaque wake-up signal to Android; no event title, link or picture |

Host-networked containers still bind sensitive APIs/restreams to loopback.
The concrete Compose caps are safety limits, not observed steady-state RSS.

## 2. Data flows

### 2.1 Live

```
browser -> POST /api/cameras/{id}/stream-ticket -> authenticated API
browser -> WS /stream/ws with short-lived ticket -> API proxy -> go2rtc
browser <- WebRTC ICE on 8555, or MSE over the authenticated WebSocket
```

The UI tries H.265 WebRTC when the device supports it, with MSE fallback.
The browser never gets camera credentials or a raw go2rtc control endpoint.
Camera ids come from configuration; they are not database camera rows.

### 2.2 Recording and playback

```
camera main RTSP -> ffmpeg (-c:v copy; optional AAC audio)
                -> /data/recordings/<camera>/<UTC date>/<UTC time>.mp4
                -> indexer -> SQLite segments
                -> retention -> age/disk-cap deletion
```

The recorder connects directly to the camera. API, detector and go2rtc
failures do not stop recording. A full image deployment restarts the
recorder container and can create a short gap.

Five-minute fragmented MP4 segments are the default. Regular retention is
15 days, alert segments 30 days, and the disk cap is 90%. Protected
segments are not automatically deleted.

Playback supports HTTP ranges and `?at=<seconds>` (0 when absent): the init
boxes, a generated `sidx` segment index, then the fragments beginning at the
preceding keyframe. Without the `sidx`, Chrome's demuxer read every `moof`
before the first frame (~90 range requests per five-minute file, ~10 s over
the phone's remote path); with it, playback starts after one request. Only
index metadata is added; media bytes are unchanged. Fragment indexes are
cached by file size/modification time.

Timeline previews use `?at=&gop=1`: one video keyframe in a small MP4, or a
GOP fallback when trimming is not possible. The SPA decodes them on the
viewing device with one reused hidden `<video>`, newest request first,
and keeps small JPEG copies in memory; holding a finger on the ribbon or
ruler shows them. The server copies bytes; the viewing device decodes them.

### 2.3 Camera control

The browser posts operator-authorized PTZ actions. The Go controller owns
one serialized DVRIP connection per camera, closes it after idle, and stops
held movement when keepalives cease. Persistent config writes use the
allow-list and read/modify/read-back/restore procedure in CAMERA.md.

Pan and tilt are configurable independently (`invert_pan`, `invert_tilt`)
and inherit `invert_pan_tilt` when unset. Talk uses go2rtc's backchannel.
There is no browser-to-camera connection.

### 2.4 Events and notifications

ONVIF events enter SQLite and the live SSE hub; PullMessages calls are
spaced at least one second apart for this camera. The detector consumes the
motion gate, analyzes only substream keyframes, applies zones, and posts
person/vehicle/animal events over a token-protected loopback endpoint.

Modes Casa/Noite/Fora and per-user preferences decide which events notify.
While detection is healthy, raw motion stays in the timeline but does not
send a push. Each user/camera burst is limited to one notice per minute.

- Browsers: Web Push with VAPID and RFC 8291 encryption.
- Android: an FCM data message with a random id. The app fetches
  `GET /api/notify/pending/{id}` using its own pinned HTTPS connection and
  session. Payloads expire in memory after 15 minutes; the cache is bounded.
- Android fallback: authenticated `GET /api/notify/stream` held by the
  foreground service when Firebase registration is unavailable.

FCM tokens are tied to login sessions. Logout/revocation deletes them;
expired sessions are excluded. Registration uses a validation-only FCM
request before the direct service is stopped.

### 2.5 Remote and Android

ADR-0019 selected the public HTTPS listener as the app/browser path.
The household's router forwards a public TLS port to `:7443` and permits
WebRTC on 8555. Public sessions face the closed-door policy and login
limits; two-step login is optional per account (ADR-0019 amendment). Tailscale HTTPS remains the private administrative path; Funnel is
off and the embedded app tailnet was removed.

The household uses `remote.public.self_signed: true`. Its certificate is
stored on the server and its exact fingerprint is compiled into the APK.
Release configuration is `~/.sentinela-android/connection.json`, outside
Git. The Android WebView loads the selected server origin, preserving
same-origin cookie/CSRF auth. A bundled page handles connection failures.

### 2.6 Self-hosted services (ADR-0021)

```
browser -> /api/hosting/* (admin; changes only from LAN/tailnet) -> API
API -> 127.0.0.1:7091 with /data/apps-agent/token -> apps agent
agent -> Docker socket: capped containers "sapp-<id>" on bridges sn-*
agent -> api.github.com/codeload, or a folder under apps.local_root (ro)
```

Sources are snapshotted into `/var/lib/sentinela-apps/apps/<id>/releases`
(system disk); `data/` and `cache/` persist across deploys. Script
services run an allow-listed runtime image with the release at `/app`;
Dockerfile services are built with the classic builder under build caps;
Compose files are normalized, checked and rewritten before `up`. The agent
supervises (no Docker restart policy), starts only autostart services
after a host boot and only after the boot delay, and writes the ports open
to the internet for `scripts/apps-firewall.sh` (DOCKER-USER chain:
services cannot reach private ranges; their ports answer private sources,
and the internet only when listed). Services and builds share at most
`apps.total_memory_mb` and one core; they weigh less than the NVR.

### 2.7 Nuvem and accounts

The existing API confines filesystem access through `os.Root`.
Viewers cannot use Nuvem; operators use personal and shared directories;
admins can access the share. Top-level folders cannot be renamed, moved or
deleted through the API. Samba's separate LAN account is not a web account.

Uploads are resumable chunks with no file-size cap (only free space): the
first is 8 MiB, later ones grow with the measured speed to ~6 s each, up to
128 MiB. A request may stay silent for at most a minute. Desktop folder
uploads create containing folders, then send files; up to four files under
32 MiB are parallel, larger files serial. Chunks go through XHR for byte
progress; the panel shows speed (5 s window) and time left, and leaving
the page during an upload asks first. Plain-HTTP downloads use
sendfile; TLS listeners accept ~4 MiB HTTP/2 receive windows. The picker does not represent
empty directories. A conflicting top folder gets a new name.

Deletes enter the recycle bin; explicit trash purge is permanent. The owner
column is informative: personal-folder identity or upload/mkdir audit data
for shared paths. API permissions do not depend on that display column.

Profile photos are resized on the client and served with authentication.
Admins authorize users by invite links; passwords, optional e-mail sign-in,
TOTP, device sessions, disabled accounts and audit are in the existing DB.

## 3. Storage

```
/data/
  recordings/                 UTC fragmented MP4 files
  db/sentinela.db              SQLite WAL
  snapshots/<camera>/          Event JPEGs
  cache/timelapse/             Prepared timelapse files
  avatars/                    User photo files
  push/vapid.json              Browser push signing key, 0600
  fcm/service-account.json     Firebase private key, 0600, UID 1000
  acme/self/                   Household TLS certificate/key
  app/app.json                 Published Android version metadata
  app/sentinela.apk            Signed APK
/srv/nas/                      Personal/shared files and recycle bin
/srv/backup/                   Local DB/config backups
/var/lib/sentinela-apps/       Self-hosted services: state.json (0600), sources, data, caches
/data/apps-agent/token         API ↔ apps agent shared secret
/etc/sentinela/
  config.yaml                  Server configuration
  secrets.env                  Camera secrets, root 0600
```

Developer-only files in `~/.sentinela-android/`: signing keystore/password,
Firebase client JSON, connection JSON and the retained service-account
source. None are committed or sent in the Docker build context.

## 4. Ports

| Port | Access | Owner / purpose |
|---|---|---|
| 8080/tcp | LAN and loopback proxy | API/UI, LAN HTTP |
| 443/tcp | Tailnet | Tailscale HTTPS serve to loopback 8080 |
| 7443/tcp | Approved public forwarding | TLS listener, mandatory public auth |
| 7080/tcp | Off in the household | Optional ACME HTTP-01 challenge listener |
| 8555/tcp+udp | LAN, tailnet and ADR-0019 public allowance | WebRTC ICE |
| 1984/tcp, 8554/tcp | Loopback | go2rtc API and RTSP restream |
| 22/tcp | LAN/tailnet admins | Key-only SSH |
| 127.0.0.1:7091 | Loopback | Apps agent API (token) |
| 20000–20099 | Per service: loopback, LAN+tailnet, tailnet, or public (ADR-0021) | Self-hosted services, filtered by the DOCKER-USER services chain |
| 445/tcp | LAN | Samba; remote SMB remains an explicit owner decision |

The household public URL uses WAN port 18443. Public ports are enabled by
the marker/config applied by `setup-public.sh`; all other exposure remains
forbidden. See SECURITY.md for the firewall and ACL rules.

## 5. SQLite

The authoritative schema is `internal/store/migrations/`, through 0009.
WAL, `busy_timeout=10000`, `synchronous=NORMAL` and foreign keys are enabled.

| Tables/data | Purpose |
|---|---|
| `users`, `sessions` | Roles, login identities, invitations, TOTP and revocable sessions |
| `segments`, `recorder_status` | Recorded-file index and recorder health |
| `events`, review seen state | Camera/detector events and per-user review |
| `settings` | House mode, detection settings and zones |
| `push_subscriptions`, `fcm_tokens` | Browser subscriptions and session-bound Android devices |
| `audit` | Security, user, camera and file actions |

Cameras remain in `config.yaml`. The recorder can continue writing files
when indexing fails; later scans recover the index.

## 6. Disk layout

Each role gets its own filesystem, so recordings can never fill the system
or the file share. Mount points are `chattr +i` so a missing data disk
cannot silently redirect writes to `/`. The recorder also checks for
`/data/.sentinela-volume`, a marker that exists only on the mounted disk
(`recording.volume_marker`); without it recording pauses and the System
page shows the error instead of filling `/`.

| Mount | Use | Required |
|---|---|---|
| `/` | OS and Docker | Yes |
| `/data` | Rolling video and application state (database, keys) | Yes: its own disk |
| `/srv/nas` | Nuvem (LAN share and web file browser) | Optional, its own disk |
| `/srv/backup` | Local DB/config backups (ADR-0014) | Optional, recommended; not on the `/data` disk |

The SMART timer snapshots disk state hourly for the System page.

## 7. Deployment and recovery

`scripts/deploy.sh` builds locally, ships images/configuration/scripts over
SSH and restarts the Compose stack. The server needs no source tree or
Go/Node toolchain. CI does not deploy.

Android-only changes use `build-apk.sh` then `publish-apk.sh`; the published
version/hash is served through authenticated `/api/app/*`. These operations
do not require restarting the NVR containers. Same-key APK updates preserve
the installed app identity.

Nightly backups cover SQLite and `/etc/sentinela`; restic runs only with a
configured and mounted external target. Current scripts do not automatically
copy `/data/fcm`, `/data/acme` or `/data/push`: retain those separately in
protected recovery storage. Losing the TLS certificate requires a new APK
pin; losing the Android signing key prevents an in-place app update.

Restore drills use a scratch restore, SQLite integrity check and a restored
Nuvem-file checksum. External backup and recovery acceptance remain pending
in PLAN.md.
