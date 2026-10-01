# Sentinela v2

A home NVR and file server for XMEye/Xiongmai cameras, built for an Intel
old x86-64 PC with 4–8 GB RAM. The same Go binary runs the API and, in a
separate container, the recording supervisor. go2rtc handles live media;
ffmpeg copies H.265 video into fragmented MP4 recordings.

## Current status

Live view, recordings, camera control, event review, detection zones,
Nuvem, accounts and the Capacitor Android app are deployed. The current
Android release is **1.13 (version code 14)**. Outstanding operator checks,
camera isolation, backup recovery and future work are in
[the master plan](docs/PLAN.md).

- Recording pulls directly from the camera and survives API/go2rtc failures.
  Full image deployments restart the recorder container and can leave a
  short gap.
- Video is never transcoded. The measured substream snapshot/detector
  exceptions are recorded in ADR-0015 and ADR-0016.
- The UI supports desktop, mobile browsers and Android, with six palettes
  and light, dark, black or automatic appearance.
- Android notifications use Firebase for an opaque wake-up id; the app
  fetches event details from the house. The direct foreground connection
  remains a fallback (ADR-0020).
- Nuvem (the file storage) is available to operators and admins. Viewers
  see cameras/events; operators use their own folder and the shared one
  (*Compartilhado*).
- Self-hosted services (ADR-0021, optional): admins deploy their own
  projects from GitHub or a Nuvem folder — script, Dockerfile or Compose —
  as capped containers next to the NVR (`scripts/setup-apps.sh enable`).

## Server and remote access

The deployed server is `ana@192.168.1.10`, Ubuntu 22.04, with three
disks. The LAN UI is `http://192.168.1.10:8080`; Samba provides
`\\192.168.1.10\Nuvem` and read-only `Gravacoes`.
See [architecture](docs/ARCHITECTURE.md) for storage and services.

The Android app and remote browser use the public HTTPS address selected
under ADR-0019. Public login requires TOTP and has a closed door before
authentication. The current household uses an IP certificate signed by
itself and pinned in the APK; browsers may warn about this certificate.
Tailscale remains available for private administration. Embedded Tailscale
was removed from the app; Funnel is disabled.

Do not expose camera ports, SMB, go2rtc's API or RTSP to the internet.
The public HTTPS/WebRTC allowances are the specific ADR-0019 exception;
see [security](docs/SECURITY.md).

## Deploy server changes

From the development PC:

```bash
./scripts/deploy.sh
```

The script builds images locally, sends them over SSH using
`~/.ssh/id_ed25519_sentinela`, ships `deploy/` and `scripts/`, restarts the
Compose stack, and checks health. Override the target with
`SENTINELA_HOST=user@host`. GitHub Actions checks code; it does not deploy.
Commit before deployment so the running version identifies the revision.

## Build and publish Android

Needs JDK 21, Android SDK and Node/npm. Local release configuration belongs
outside Git in `~/.sentinela-android/`:

| File | Purpose |
|---|---|
| `release.jks`, `keystore.pass` | Signing identity; retain for every update |
| `google-services.json` | Firebase Android client for `casa.sentinela.app` |
| `connection.json` | Server origin and exact TLS certificate fingerprint |

Example connection configuration (replace with the household's values):

```json
{
  "domain_url": "https://203.0.113.7:18443",
  "public_cert_sha256": "<64 hexadecimal characters>"
}
```

Obtain the fingerprint over trusted SSH with
`sudo /opt/sentinela/scripts/setup-public.sh status`.
`SENTINELA_DOMAIN_URL` and `SENTINELA_PUBLIC_CERT_SHA256` override the file;
`SENTINELA_CONNECTION_JSON` selects another file. Gradle refuses a release
without a valid HTTPS origin and fingerprint. Never solve a TLS error by
disabling certificate checks.

```bash
./scripts/build-apk.sh
./scripts/publish-apk.sh "What changed"
```

The signed file is `mobile/build/sentinela.apk`. Install it over the
existing app, with the same signing key. Since 1.9, My account → App
(*Minha conta → Aplicativo*) offers later published versions and opens
Android's installer.
Android asks once to allow this installation source.

**Recovery from 1.11:** its APK omitted the certificate pin, so it cannot
reach the server or its updater. Install 1.12 manually once, without
uninstalling; subsequent updates use the in-app flow.

Firebase's service-account key is separate from the Android client JSON.
Deploy it as `/data/fcm/service-account.json`, mode 0600, readable by UID
1000. It is never packaged in the APK or committed. The app does not need
the Tailscale app for the current public access path.
The `.dev` Android build uses direct notifications.

## Install a fresh server

Follow **[docs/INSTALL.md](docs/INSTALL.md)**: hardware, camera
preparation, disks, `install-server.sh`, first login and two-step login,
Samba, remote access, notifications, the Android app, updates, backups and
troubleshooting. In short: mount the recording disk at `/data`, clone the
repository to `/opt/sentinela`, run `sudo ./scripts/install-server.sh`, then
open `http://<server>:8080` to create the admin.

| Operation on the server | Command |
|---|---|
| Logs | `cd /opt/sentinela/deploy && sudo docker compose logs -f --tail=50` |
| Reset a web password | `cd /opt/sentinela/deploy && sudo docker compose exec -T sentinela sentinela user passwd <user>` |
| Check public listener/certificate | `sudo /opt/sentinela/scripts/setup-public.sh status` |
| Check running services | `cd /opt/sentinela/deploy && sudo docker compose ps` |

Recordings use UTC paths under
`/data/recordings/<camera>/<YYYY>/<MM>/<DD>/<HH-MM-SS>.mp4`, normally five
minutes each. Default retention is 15 days, alerts 30 days, with a 90%
disk cap. Protected segments are excluded from automatic deletion.

## Develop

Copy `deploy/config.example.yaml` to `dev-config.yaml` (git-ignored), point
`data_dir` at a local folder and set the camera variables it names
(`CAM1_USER`, `CAM1_PASS`) in your shell. Then:

```bash
cd web && npm ci && npm run build && cd ..
go run ./cmd/sentinela serve -config ./dev-config.yaml
cd web && npm run dev
make check
```

Vite proxies to `http://127.0.0.1:8080`; set `SENTINELA_API` for another
local API. Windows has no `make`: run the `check` steps of the
[Makefile](Makefile) by hand. Android Studio: `cd mobile && npm ci && npm run studio`.
The SDK and signing configuration must be present on that PC.

## Documentation and code

| Path | Purpose |
|---|---|
| [AGENTS.md](AGENTS.md) / [CLAUDE.md](CLAUDE.md) | Agent rules and entry point |
| [docs/INSTALL.md](docs/INSTALL.md) | Step-by-step installation, updates and troubleshooting |
| [docs/PLAN.md](docs/PLAN.md) | Current status, historical phases and pending checks |
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | Components, flows, ports and storage |
| [docs/CAMERA.md](docs/CAMERA.md) | Allow-listed commands and dated hardware evidence |
| [docs/SECURITY.md](docs/SECURITY.md) | Authentication, network and secret handling |
| [docs/RESEARCH.md](docs/RESEARCH.md) / `docs/adr/` | Research history and accepted decisions |
| `cmd/`, `internal/` | Go API, auth, camera drivers, recording and store |
| `web/`, `mobile/` | React SPA and native Android wrapper |
| `deploy/`, `scripts/` | Compose configuration and operator scripts |

This replaces the earlier Sentinela Go and Local-Cloud Django projects.
Protocol knowledge is retained; the current architecture is described here
and in the ADRs.
