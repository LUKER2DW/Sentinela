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

Phase 0 — planning and camera audit. See [`docs/PLAN.md`](docs/PLAN.md).

## Layout

| Path | Purpose |
|---|---|
| `AGENTS.md` | Rules for any AI coding agent working in this repo |
| `CLAUDE.md` | Claude-specific entry point (imports `AGENTS.md`) |
| `docs/PLAN.md` | Master plan: scope, phases, milestones, acceptance criteria |
| `docs/ARCHITECTURE.md` | Components, data flow, ports, storage layout |
| `docs/CAMERA.md` | Everything known about the camera + audit procedure |
| `docs/RESEARCH.md` | Related projects and what we reuse from each |
| `docs/SECURITY.md` | Threat model and security rules |
| `docs/adr/` | Architecture decision records |
| `scripts/camera-audit.sh` | Read-only camera audit (run on the LAN) |
| `deploy/` | Docker Compose and go2rtc configuration |
| `cmd/`, `internal/` | Go backend (to be implemented) |
| `web/` | React + Vite frontend (to be implemented) |

## Quick start (Phase 0)

```bash
# On a machine in the same LAN as the camera:
./scripts/camera-audit.sh 192.168.1.20
# Password is prompted silently and never stored.
```

## Lineage

This project replaces two earlier attempts (`LUKER2DW/Sentinela`, Go, and
`LUKER2DW/Local-Cloud`, Django). Their protocol knowledge is kept; their
architecture is not. See `docs/PLAN.md` §2.
