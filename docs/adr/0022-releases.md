# ADR-0022: Versioned releases and a published server image

Status: accepted (2026-11-07). Amends ADR-0010 (CI only) and ADR-0011 (APK built on the dev PC).

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
- The generic Android APK is built and signed in the same run, with the
  version taken from the tag (code = major·10000 + minor·100 + patch), and
  attached with an `app.json` (version, size, SHA-256). Android accepts an
  update only when it is signed with the same key, so the key is fixed: it
  lives as secrets of the `release` environment (`ANDROID_KEYSTORE_B64`,
  `ANDROID_KEYSTORE_PASSWORD`; no Firebase project is built in, ADR-0020),
  which only this job can read; pull requests and forks never see them.
  Once public, the environment requires the maintainer's approval and only
  runs for `v*` tags.
- Servers mirror the app (`app_updates.github`, default the project's
  repository): every 6 hours they read the latest release, download the APK
  when its `app.json` is newer, check size and SHA-256, and offer it to the
  phones through `/api/app/latest`. Phones keep updating from their own
  house.
- The workflow never deploys and never sees server secrets.
- Installs keep building locally by default; a server may use a published
  image instead (docs/RELEASING.md), or follow releases automatically
  (ADR-0023). Each release also carries the deploy bundle and `image.txt`
  (the image digest), both in `SHA256SUMS`.

## Consequences

+ A known, attested version for anyone, without a build toolchain.
+ No server secrets and no deploy path from CI.
− The signing key is now held by GitHub as an environment secret: a
  compromised maintainer account could sign an update. Mitigations: the
  environment's approval rule, two-factor login on the account, and servers
  only offering an APK whose SHA-256 matches the release's app.json.
− The image is linux/amd64 only (the target host class); ARM builds are a
  later decision with their own budget measurements.
− The detector image is still built locally (large, optional).
