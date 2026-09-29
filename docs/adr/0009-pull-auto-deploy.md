# ADR-0009: Pull-based auto deploy from GitHub

Status: Accepted · 2026-09-28 (owner request)

## Context
Deploys ran from the dev PC (`scripts/deploy.sh`: build locally, stream the
image over SSH). The owner wants a push to GitHub to reach the server on its
own. The server is LAN-only (AGENTS.md §2.5): nothing may call into it from
the internet, so a GitHub webhook or an Actions runner pushing to it is out.

## Decision
- The server **pulls**: `sentinela-autodeploy.timer` runs every 2 minutes and
  `git fetch`es `main` over SSH with a **read-only deploy key**
  (`/etc/sentinela/deploy_key`, root 0600). No inbound port, no registry, no
  token with write access.
- On a new head it builds the image **on the server** (same Dockerfile),
  ships `deploy/` + `scripts/`, restarts with `compose up
  --no-build`, and waits for the health check. Unhealthy → retag
  `sentinela:previous` → `sentinela:local` and restart (rollback).
- Each commit is attempted once; a broken commit waits for the next push.
- The deployer itself (`/usr/local/sbin/sentinela-autodeploy`) is installed by
  `scripts/setup-autodeploy.sh` and is **not** replaced by the commits it
  deploys, so a bad push cannot break the rollback path.
- `scripts/deploy.sh` from the PC stays for trying uncommitted work; the timer
  only acts when GitHub's head changes.

## Consequences
+ Push to `main` → live in ~5–15 min (first build longer: Go and npm caches
  are cold). Recording keeps running during the build; the recorder
  container restarts for a few seconds when the image changes, as with any
  deploy.
+ The build uses up to ~1.5 GB RAM and all cores for a few minutes on the
  4 GB host. The recorder is copy-only (~3 % CPU) and keeps up; live view
  may stutter during a build.
− Anyone who can push to `main` can run code on the server: write access to
  the repository is as sensitive as SSH access. Keep the repo private and
  2FA on.
− One more host unit to know about: `journalctl -u sentinela-autodeploy`.
