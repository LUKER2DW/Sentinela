# ADR-0022: Versioned releases and a published server image

Status: accepted (2026-11-07). Amends ADR-0010 (CI only).

## Context

Sentinela is becoming a public project. Other people need a way to install
a known version without building on their own machine, and a place to get
the Android app. ADR-0010 keeps GitHub Actions to checks only: nothing in CI
deploys, uses a self-hosted runner or holds secrets for a server.

## Decision

- A version tag `vMAJOR.MINOR.PATCH` on the public repository runs
  `.github/workflows/release.yml`: the same checks as CI, then
  - the server image, pushed to GitHub Packages as
    `ghcr.io/luker2dw/sentinela:<version>`, `:<major>.<minor>` and `:latest`,
    with an SBOM and a build-provenance attestation;
  - a Linux amd64 binary with the web app embedded, with `SHA256SUMS`;
  - the GitHub release, with notes generated from the commits.
- The workflow uses only its own `GITHUB_TOKEN` (packages and contents
  write). It never deploys and never sees server or signing secrets.
- The generic Android APK is built and signed on the maintainer's machine
  (`SENTINELA_GENERIC_APK=1 ./scripts/build-apk.sh`) and attached to the
  release by hand: the signing key never leaves that machine.
- Installs keep building locally by default; a server may use a published
  image instead (docs/RELEASING.md).

## Consequences

+ A known, attested version for anyone, without a build toolchain.
+ No new secrets and no deploy path from CI.
− The image is linux/amd64 only (the target host class); ARM builds are a
  later decision with their own budget measurements.
− The detector image is still built locally (large, optional).
