# Sentinela v2

A small, fast, self-hosted NVR for XMEye/Xiongmai IP cameras, built to run on
modest hardware (an old CPU, 4 GB RAM).

Goals, in priority order:

1. **Recording never stops.** Continuous, copy-only (no re-encode) recording.
2. **Secure by default.** No ports opened on the router; camera blocked from
   the internet; remote access only through a private VPN (Tailscale).
3. **Lowest possible live latency.** WebRTC first (< 500 ms on LAN/VPN),
   MSE/HLS fallback.
4. **Full camera control.** PTZ, presets, light, day/night, two-way audio via
   the reverse-engineered DVRIP protocol (TCP 34567).
5. **Small and maintainable.** One Go binary + go2rtc + ffmpeg. SQLite.
   Three containers, one `docker-compose.yml`.

## Status

Phases 1–4 (live view, recording, API, web UI) are built and were verified on
a dev PC against the real camera. Next: install on the server and run the
acceptance checks. See [`docs/PLAN.md`](docs/PLAN.md).

## The server today

`ana@192.168.1.10` (Ubuntu 22.04, old CPU, three disks): web panel on
`http://192.168.1.10:8080`, LAN share `\\192.168.1.10\Nuvem`
(~670 GB) and read-only `\\192.168.1.10\Gravacoes`. Disk layout and
health notes: [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) §6.

## Deploy an update (from the dev PC)

```bash
./scripts/deploy.sh
```

Builds the image on the PC, streams it to the server over SSH (key
`~/.ssh/id_ed25519_sentinela`), ships `deploy/` + `scripts/`, restarts the
three containers and waits for the health check (~1–2 min). Override the
target with `SENTINELA_HOST=user@host`.

Deploys are manual only ([ADR-0010](docs/adr/0010-manual-deploy.md)).
GitHub Actions runs checks (`ci` workflow) on GitHub-hosted runners and never
deploys. A server that still has the retired auto deployer:
`sudo /opt/sentinela/scripts/uninstall-autodeploy.sh`.

## Install on a fresh server (Ubuntu 22.04)

1. Mount the recordings disk at `/data` (and the NAS disk at `/srv/nas` if
   you want the LAN share). The installer refuses to put recordings on `/`.
2. Copy the repo to `/opt/sentinela` and run `sudo ./scripts/install-server.sh`.
   It installs Docker, asks for the camera user/password (typed silently,
   stored only in `/etc/sentinela/secrets.env`, 0600), writes
   `/etc/sentinela/config.yaml`, applies the LAN-only firewall, sets up Samba,
   chrony (camera NTP) and the SMART timer, then starts the stack.
3. Set the share password once: `sudo smbpasswd -a ana`.
4. Open `http://<server-ip>:8080`; the first visit creates the admin account.

The camera password must not contain `& ? / # %` or spaces (the camera reads
it literally from the RTSP URL). Nothing else (VLC, vendor apps) should pull
RTSP from the camera: it accepts only 3 sessions.

| Task | Command (on the server) |
|---|---|
| Logs | `cd /opt/sentinela/deploy && sudo docker compose logs -f --tail=50` |
| Reset a web password | `cd /opt/sentinela/deploy && sudo docker compose exec -T sentinela sentinela user passwd <user>` |
| Share password | `sudo smbpasswd -a ana` |
| Stop / start | `cd /opt/sentinela/deploy && sudo docker compose stop` / `start` |

Recordings go to `/data/recordings/<camera>/<YYYY>/<MM>/<DD>/<HH-MM-SS>.mp4`
(UTC), 5 minutes each (~24 GB/day). The oldest are deleted after
`retention_days` (15) or when the disk passes 90 % (≈ 18 days on -).

## Develop

```bash
cd web && npm ci && npm run build && cd ..   # UI, embedded into the binary
go run ./cmd/sentinela serve -config ./dev-config.yaml
cd web && npm run dev                        # UI with hot reload on :5173
make check                                   # vet, tests, staticcheck, lint
```

## Layout

| Path | Purpose |
|---|---|
| `AGENTS.md` | Rules for any AI coding agent working in this repo |
| `CLAUDE.md` | Claude-specific entry point (imports `AGENTS.md`) |
| `docs/PLAN.md` | Master plan: scope, phases, status, acceptance criteria |
| `docs/ARCHITECTURE.md` | Components, data flow, ports, storage layout |
| `docs/CAMERA.md` | Everything known about the camera + audit results |
| `docs/SECURITY.md` | Threat model and security rules |
| `docs/adr/` | Architecture decision records |
| `cmd/sentinela` | The binary: `serve`, `record`, `user`, `healthcheck` |
| `internal/` | API, auth, DVRIP/ONVIF drivers, recorder, store |
| `web/` | React + Vite UI (pt-BR, light/dark themes) |
| `deploy/` | Docker Compose, go2rtc and example configs |
| `scripts/` | `install-server.sh`, read-only `camera-audit.sh` |

## Lineage

This project replaces two earlier attempts (`LUKER2DW/Sentinela`, Go, and
`LUKER2DW/Local-Cloud`, Django). Their protocol knowledge is kept; their
architecture is not. See `docs/PLAN.md` §2.
