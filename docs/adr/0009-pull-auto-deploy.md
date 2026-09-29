# ADR-0009: Auto deploy from GitHub without inbound access

Status: Accepted · 2026-09-28 (owner request)

## Context
Deploys ran from the dev PC (`scripts/deploy.sh`: build locally, stream the
image over SSH). The owner wants a push to GitHub to reach the server on its
own. The server is LAN-only (AGENTS.md §2.5): nothing may call into it from
the internet, so a GitHub webhook or an Actions runner pushing to it is out.

## Decision
- **Trigger: a GitHub Actions self-hosted runner** on the server
  (`.github/workflows/deploy.yml`, label `sentinela`). The runner keeps an
  outbound long-poll to GitHub, so a push starts the job within seconds and
  no port is opened. It runs as `gh-runner` (no shell, no docker group) whose
  only privilege is one sudoers rule: `sentinela-autodeploy --wait`. A
  workflow can therefore only ask the server to deploy what is on `main`.
  Installed by `scripts/setup-runner.sh` (pinned version + SHA-256; the
  one-time registration token is piped on stdin).
- **Fallback:** `sentinela-autodeploy.timer` checks every 15 minutes in case
  the runner is down. A lock serialises both.
- The deployer `git fetch`es `main` over SSH with a **read-only deploy key**
  (`/etc/sentinela/deploy_key`, root 0600). No registry, no token with write
  access on the server.
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
+ Push to `main` → build starts in seconds, live in ~5 min (first build longer: Go and npm caches
  are cold). Recording keeps running during the build; the recorder
  container restarts for a few seconds when the image changes, as with any
  deploy.
+ The build uses up to ~1.5 GB RAM and all cores for a few minutes on the
  4 GB host. The recorder is copy-only (~3 % CPU) and keeps up; live view
  may stutter during a build.
− Anyone who can push to `main` can run code on the server: write access to
  the repository is as sensitive as SSH access. Keep the repo private and
  2FA on.
− Two more host services: the runner (~80 MB RSS idle, ~0 % CPU;
  self-update is disabled, so bump `VER`/`SHA` in `setup-runner.sh`) and the
  fallback timer. Logs: the repo's Actions tab, or
  `journalctl -u sentinela-autodeploy`.
