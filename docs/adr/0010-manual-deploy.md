# ADR-0010: Manual deploy from the dev PC; CI checks only

Status: Accepted · 2026-09-29 (owner decision D2; supersedes ADR-0009)

## Context
ADR-0009 let the NVR host deploy itself: a timer polled GitHub every 2
minutes with a read-only deploy key, and a self-hosted Actions runner was
prepared (never registered). That puts a build pipeline, a GitHub credential
and, with the runner, workflow code execution on the one machine whose job
is to keep recording. A full image build also takes ~1.5 GB RAM and all
cores of the 4 GB host for minutes. The owner deploys from the LAN anyway.

## Decision
- Deploys are manual only: `scripts/deploy.sh` on the dev PC builds the
  image, streams it over SSH, ships `deploy/` + `scripts/` and restarts
  with a health check (ARCHITECTURE §7). The server builds nothing.
- Removed: `sentinela-autodeploy.{service,timer}`,
  `/usr/local/sbin/sentinela-autodeploy`, the runner setup and its sudoers
  rule, and `/etc/sentinela/deploy_key` (the owner revokes the key in
  GitHub → Settings → Deploy keys). `scripts/uninstall-autodeploy.sh`
  removes them from an existing host.
- GitHub Actions stays as **CI only** (`.github/workflows/ci.yml`):
  `go vet`, `go test`, `staticcheck`, `govulncheck`, web lint and tests on
  GitHub-hosted runners. No workflow may use a self-hosted runner, hold
  secrets for the server, or deploy anything.
- Android artifacts are built/signed with `scripts/build-apk.sh` and published
  with `scripts/publish-apk.sh`. Publishing an APK does not restart Compose.
  Release server origin/TLS pin are permanent local build configuration
  (`~/.sentinela-android/connection.json`); missing pins fail the build.

## Consequences
+ The server holds no GitHub credential and runs no code it did not get
  from the owner's PC over SSH.
+ No surprise builds competing with the recorder for RAM.
− A push does not reach the server by itself; the owner runs
  `./scripts/deploy.sh` (from the LAN or the tailnet).
