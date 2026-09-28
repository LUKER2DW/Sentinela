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

## Install on the server (Ubuntu 22.04)

Before you start: the camera password must not contain `& ? / # %` or spaces
(the camera reads it literally from the RTSP URL), and nothing else (VLC,
vendor apps) should pull RTSP from the camera: it accepts only 3 sessions.

```bash
sudo apt-get update && sudo apt-get install -y git
sudo git clone https://github.com/LUKER2DW/Sentinela.git /opt/sentinela
cd /opt/sentinela
sudo ./scripts/install-server.sh
```

The script installs Docker, asks for the camera user and password (typed
silently, stored only in `/etc/sentinela/secrets.env`, mode 0600), creates
`/etc/sentinela/config.yaml` and `/data`, builds the image and starts three
containers: `go2rtc` (live video), `recorder` (24/7 recording) and
`sentinela` (web + API on port 8080).

Then open `http://<server-ip>:8080` in the browser. The first visit creates
the admin account.

| Task | Command |
|---|---|
| Logs | `cd /opt/sentinela/deploy && sudo docker compose logs -f --tail=50` |
| Update | `cd /opt/sentinela && sudo git pull && sudo ./scripts/install-server.sh` |
| Reset a password | `cd /opt/sentinela/deploy && sudo docker compose exec -T sentinela sentinela user passwd <user>` |
| Stop / start | `cd /opt/sentinela/deploy && sudo docker compose stop` / `start` |

Recordings go to `/data/recordings/<camera>/<YYYY>/<MM>/<DD>/<HH-MM-SS>.mp4`
(UTC), 5 minutes each. Oldest files are deleted after `retention_days` or when
the disk passes 90 %. Put `/data` on its own disk if you can.

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
