# ADR-0001: Go single binary for the control plane

Status: Accepted · 2026-09-28

## Context
Host is an old CPU with 4 GB RAM. v1 Django stack (Local-Cloud) needed
Postgres, Redis, Celery and did not fit; v1 Go stack (Sentinela) fit but was
split into ~9 processes.

## Decision
One Go binary: API, auth, SPA hosting, DVRIP/ONVIF, recorder supervision,
indexing, retention. Stdlib `net/http`, `log/slog`, `modernc.org/sqlite`.

## Consequences
+ ~20–60 MB RSS, static binary, trivial deploy, same language as go2rtc.
+ DVRIP knowledge from v1 and go2rtc is directly portable.
− A crash takes down API and supervision together → recorders are separate
  ffmpeg child processes restarted by the container's restart policy, and
  Docker restarts the binary within seconds.

## Alternatives
Django/FastAPI (higher RSS, more services), Rust (slower to build), Node (no win).
