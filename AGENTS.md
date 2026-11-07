# AGENTS.md — rules for AI coding agents

This file is the single source of truth for how any agent (Claude, Codex,
Cursor, etc.) works in this repository. Read it fully before changing code.

## 1. Project in one paragraph

Sentinela is a home NVR for RTSP cameras, with full control of
XMEye/Xiongmai cameras. A single Go binary
(`cmd/sentinela`) owns the API, auth, camera control (DVRIP + ONVIF),
recording supervision, retention and the SQLite index. **go2rtc** handles all
live media (RTSP ingest, WebRTC/MSE out, two-way audio). **ffmpeg** copies
recorded video; optional audio conversion is ADR-0006. A React + Vite SPA is served by the Go binary.
Minimum host: x86-64 with 2 cores and no HEVC hardware decode, 4 GB RAM, a
dedicated recordings disk, Ubuntu 22.04+ or Debian 12+. Every budget below
holds on that host.

## 2. Hard constraints (never violate)

1. **No transcoding on the server.** Video is H.265 and the CPU has no HEVC
   hardware decode. Every media path is copy/remux only. Any change that
   decodes video must come with a measured CPU budget and an ADR. Existing
   substream exceptions are event snapshots (ADR-0015) and the motion-gated
   detector (ADR-0016); timeline previews remain copy-only.
2. **Recording is independent of everything else.** If the API, web UI,
   go2rtc or the database crashes, recording must continue. Recorders are
   supervised ffmpeg processes pulling RTSP directly from the camera.
3. **Resource budget** (steady state, 1 camera):
   - Go binary: ≤ 80 MB RSS, ≤ 3 % CPU.
   - go2rtc: ≤ 100 MB RSS.
   - Per-camera recorder: ≤ 3 % CPU.
   - Total stack: ≤ 600 MB RSS so it fits 4 GB with OS cache.
4. **Secrets never leave the server.** Camera credentials, RTSP URLs with
   userinfo, session tokens: never in logs, API responses, frontend bundles,
   git, or error messages. Use `internal/auth/redact` helpers.
5. **No additional public exposure.** Tailscale is the private remote path.
   Do not add port-forwarding, DDNS or Cloudflare tunnels. **Exception
   (ADR-0019):** the port-forwarded TLS listener (and the Tailscale Funnel
   listener, currently switched off), behind the closed-door rule, login
   limits and optional per-account 2FA (ADR-0019 amendment, 2026-11-06).
   This is the accepted household path after the trial.
   **Exception (ADR-0021):** a self-hosted service the owner explicitly
   marks "Internet" (off by default), on its own port of the services
   range, behind the services firewall. Nothing else may be exposed.
6. **Camera writes are allow-listed.** The DVRIP driver may only send commands
   documented in `docs/CAMERA.md`. Any persistent config write must read the
   full object first, change one allow-listed field, read back, and restore
   the original on failure.
7. **Never execute vendor binaries** (VideoPlayTool, CMS, DLLs). Protocol
   knowledge only.
8. **Keep it small.** No new service, container, database or queue without an
   ADR in `docs/adr/`. No Redis, Celery, Postgres, Kafka, MQTT (as parts of
   Sentinela; the owner's own projects under ADR-0021 may use anything).
9. **Self-hosted services stay out of the NVR's way (ADR-0021).** Only the
   `apps` agent holds the Docker socket; the API never does. Services are
   capped, run on the system disk (never `/data`), cannot reach private
   ranges, and never start before the NVR after a reboot.

## 3. Tech stack

| Area | Choice |
|---|---|
| Backend | Go 1.26 (see `go.mod`), stdlib `net/http`, `log/slog` |
| DB | SQLite via `modernc.org/sqlite` (pure Go), WAL mode |
| Media | go2rtc (sidecar container), ffmpeg (recorder) |
| Frontend | React 19 + Vite + TypeScript, TanStack Query, plain CSS tokens |
| Mobile | PWA + Capacitor Android app; FCM wake-ups/direct fallback (ADR-0011, ADR-0020) |
| Deploy | Docker Compose, Ubuntu 22.04 host (Debian 12/13 also fine) |
| Remote | Tailscale/private administration; approved public TLS path (ADR-0019) |

Dependencies must be justified. Prefer stdlib. No ORMs. No web frameworks
(gin/echo/fiber) — the stdlib router is enough.

## 4. Repository layout

```
cmd/sentinela/        main binary (flags, wiring, graceful shutdown)
internal/api/         HTTP handlers, JSON contracts, middleware
internal/apps/        self-hosted services agent (ADR-0021)
internal/auth/        users, sessions, argon2id, CSRF, redaction
internal/camera/dvrip DVRIP/Sonia client (port 34567)
internal/camera/onvif ONVIF discovery, profiles, events
internal/media/       go2rtc config generation + API client
internal/recording/   ffmpeg supervisor, segment indexer, retention
internal/store/       SQLite schema, migrations, queries
web/                  React SPA
mobile/               Capacitor Android wrapper of the SPA (ADR-0011)
deploy/               compose + configs
docs/                 plan, architecture, ADRs
scripts/              operator scripts (audit, backup)
```

## 5. Workflow

1. Check `docs/PLAN.md` for the current phase when it exists (maintainers
   keep it in their private notes; public clones do not have it). Work only
   on in-scope items.
2. Small, reviewable changes. One concern per commit.
3. Before finishing: `make check` (= `go vet`, `go test ./...`,
   `staticcheck`, `npm run lint && npm test` in `web/`).
4. Update docs **only when behaviour changes**. Do not create reports,
   status logs, or per-task markdown files. The plan is updated in place.
5. Commit messages: Conventional Commits (`feat(dvrip): …`).
6. Android release configuration lives outside Git in
   `~/.sentinela-android/connection.json` (HTTPS origin and exact certificate
   SHA-256), with explicit environment overrides. Release builds must reject
   missing pins. Verify the packaged pin against the served certificate,
   sign with the existing keystore, then publish the APK. An APK-only change
   does not require restarting the NVR stack. Never bypass TLS validation.

## 6. Coding conventions

- Go: `context.Context` first arg on anything doing I/O; wrap errors with
  `%w`; no panics outside `main`; table-driven tests; no global state except
  in `main`.
- Every network call to the camera has a timeout (default 5 s) and a single
  owner. DVRIP connections are short-lived and serialized per camera.
- Timestamps UTC in storage, local time only in the UI.
- Frontend: no inline credentials, no direct calls to go2rtc from the
  browser except via signed, short-lived stream URLs issued by the API.

## 7. Testing

- Unit tests for protocol encoding/decoding use recorded fixtures in
  `internal/camera/dvrip/testdata/` (sanitized — no passwords, serials, MACs).
- Hardware tests are tagged `//go:build hardware` and never run in CI.
- Never run a hardware test that moves PTZ or writes config without the
  operator's explicit go-ahead.

## 8. What NOT to do

- Do not copy code from GPL projects (lightNVR, Moonfire, ZoneMinder,
  Shinobi, motionEye) into this MIT-intended repo. Ideas only.
  MIT/Apache sources (Frigate, go2rtc, MediaMTX, python-dvr) may be adapted
  with attribution in `NOTICE`.
- Detection is implemented under Phase 5/ADR-0016. Keep it on substream
  keyframes and within the accepted budget; no main-stream detection.
- Do not commit binaries, recordings, snapshots, `.env` or audit output.
- The repository is public: no site-specific data in code, configs, docs or
  commit messages: no real IP addresses, host or user names, network
  ranges, hardware specs, serials, public addresses or tailnet names. Use
  documentation examples (`192.168.1.x`, `203.0.113.x`, `you@…`).
- Nothing site-specific is hard-coded: it comes from `config.yaml`,
  `secrets.env`, environment variables or detection at install time, with
  a generic default.
