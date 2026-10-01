# ADR-0001: Go single binary for the control plane

Status: Accepted · 2026-09-28

## Context
Host is an old CPU with 4 GB RAM. v1 Django stack (Local-Cloud) needed
Postgres, Redis, Celery and did not fit; v1 Go stack (Sentinela) fit but was
split into ~9 processes.

## Decision
One Go binary: API, auth, SPA hosting, DVRIP/ONVIF, recorder supervision,
indexing, retention. Stdlib `net/http`, `log/slog`, `modernc.org/sqlite`.
ADR-0007 separates its `serve` and `record` modes into independent containers.

## Consequences
+ ~20–60 MB RSS, static binary, trivial deploy, same language as go2rtc.
+ DVRIP knowledge from v1 and go2rtc is directly portable.
− A recorder-mode crash still affects its ffmpeg children; Docker restarts
  that container. API-mode crashes do not affect recording (ADR-0007).

## Alternatives
Django/FastAPI (higher RSS, more services), Rust (slower to build), Node (no win).
