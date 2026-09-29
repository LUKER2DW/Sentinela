# AGENTS.md — rules for AI coding agents

This file is the single source of truth for how any agent (Claude, Codex,
Cursor, etc.) works in this repository. Read it fully before changing code.

## 1. Project in one paragraph

Sentinela v2 is a home NVR for XMEye/Xiongmai cameras. A single Go binary
(`cmd/sentinela`) owns the API, auth, camera control (DVRIP + ONVIF),
recording supervision, retention and the SQLite index. **go2rtc** handles all
live media (RTSP ingest, WebRTC/MSE/HLS out, two-way audio). **ffmpeg** writes
recordings with `-c copy`. A React + Vite SPA is served by the Go binary.
Target host: old CPU, 4 GB RAM, a dedicated recordings disk, Ubuntu 22.04.

## 2. Hard constraints (never violate)

1. **No transcoding on the server.** Video is H.265 and the CPU has no HEVC
   hardware decode. Every media path is copy/remux only. Any change that
   decodes video must come with a measured CPU budget and an ADR.
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
5. **No public exposure.** Nothing binds to the WAN. Remote access = Tailscale.
   Do not add port-forwarding, DDNS or Cloudflare tunnels. **Exception
   during the ADR-0019 trial:** the port-forwarded TLS listener and the
   Tailscale Funnel listener it describes, both behind mandatory 2FA and
   the closed-door rule. Nothing else may be exposed.
6. **Camera writes are allow-listed.** The DVRIP driver may only send commands
   documented in `docs/CAMERA.md`. Any persistent config write must read the
   full object first, change one allow-listed field, read back, and restore
   the original on failure.
7. **Never execute vendor binaries** (VideoPlayTool, CMS, DLLs). Protocol
   knowledge only.
8. **Keep it small.** No new service, container, database or queue without an
   ADR in `docs/adr/`. No Redis, Celery, Postgres, Kafka, MQTT.

## 3. Tech stack

| Area | Choice |
|---|---|
| Backend | Go ≥ 1.23, stdlib `net/http` (1.22 routing), `log/slog` |
| DB | SQLite via `modernc.org/sqlite` (pure Go), WAL mode |
| Media | go2rtc (sidecar container), ffmpeg (recorder) |
| Frontend | React 19 + Vite + TypeScript, TanStack Query, plain CSS tokens |
| Mobile | PWA + Capacitor Android app wrapping the same SPA (ADR-0011) |
| Deploy | Docker Compose, Ubuntu 22.04 host (Debian 12/13 also fine) |
| Remote | Tailscale |

Dependencies must be justified. Prefer stdlib. No ORMs. No web frameworks
(gin/echo/fiber) — the stdlib router is enough.

## 4. Repository layout

```
cmd/sentinela/        main binary (flags, wiring, graceful shutdown)
internal/api/         HTTP handlers, JSON contracts, middleware
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

1. Check `docs/PLAN.md` for the current phase. Work only on in-scope items.
2. Small, reviewable changes. One concern per commit.
3. Before finishing: `make check` (= `go vet`, `go test ./...`,
   `staticcheck`, `npm run lint && npm test` in `web/`).
4. Update docs **only when behaviour changes**. Do not create reports,
   status logs, or per-task markdown files. The plan is updated in place.
5. Commit messages: Conventional Commits (`feat(dvrip): …`).

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
- Do not add AI/object detection until Phase 5, and only on the substream.
- Do not commit binaries, recordings, snapshots, `.env` or audit output.
