# ADR-0007: Recorder runs as its own container from the same image

Status: Accepted · 2026-09-28

## Context
Hard constraint 2: recording must continue if the API, web UI, go2rtc or the
database fails. ADR-0001 put recorder supervision inside the API process. But
if that process crashes, its ffmpeg children die with the container.

## Decision
The single `sentinela` binary has two modes, run as two containers of the
**same image**:

- `sentinela record`: ffmpeg supervisor, segment indexer, retention. It keeps
  no HTTP listener. It keeps recording to disk even when SQLite is not
  writable: segment rows are (re)indexed from the files later.
- `sentinela serve`: API, auth, SPA, stream proxy, camera control, ONVIF
  events.

Both share `/data` (SQLite in WAL mode with `busy_timeout`, which is safe for
two processes on one host). The recorder writes a heartbeat row that the API
shows on the System page.

## Consequences
+ An API crash, bad deploy or UI bug can't create a recording gap.
+ No new technology: same binary, same image, one more compose entry.
− Two processes write to SQLite; mitigated by WAL, short transactions and
  retries on `SQLITE_BUSY`.
