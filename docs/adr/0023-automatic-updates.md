# ADR-0023: Opt-in automatic server updates from releases

Status: accepted (2026-11-07). Amends ADR-0010 (manual deploy only).

## Context

ADR-0009 tried a pull-based auto deploy from the repository's main branch
and ADR-0010 retired it: the server built code, competed with recording
for CPU and could pick up unfinished work. With public, versioned releases
(ADR-0022) other installs need a way to stay current without a development
computer.

## Decision

- Off by default. `scripts/setup-auto-update.sh enable [HH:MM]` installs a
  daily systemd timer (default 04:00, ±30 min) in the hour the operator
  picks as the quietest; `disable` turns it off; `now` runs it at once.
- `scripts/auto-update.sh` (root, low CPU/IO priority) installs only
  **published releases**: it reads the latest release of
  `SENTINELA_UPDATE_REPO`, downloads `SHA256SUMS`, the deploy bundle and
  `image.txt`, checks them, and pulls the server image **by digest** (built
  and attested in CI). Nothing is compiled on the server.
- It leaves alone a server running a build deployed from a computer
  (`SENTINELA_UPDATE_MANUAL=keep`, the default) and never jumps a major
  version unless `SENTINELA_UPDATE_MAJOR=yes`.
- Before switching it keeps the running image as `sentinela:previous` and
  the deploy files; when Compose or the health check fails it puts them
  back. Recording pauses for the few seconds the recorder is recreated.
- The result goes to `/var/lib/sentinela/update.json` and the schedule to
  `update-schedule.json`; the System page shows both, with the changelog.

## Consequences

+ Installs follow releases without a build machine or SSH sessions.
+ Only checksummed, CI-built images run; rollback is automatic.
− The server reaches api.github.com, github.com and ghcr.io once a day.
− The detector image is not updated this way (built locally, optional).
