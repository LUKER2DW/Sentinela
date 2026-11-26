# Releasing

Maintainers only. Releases follow [Semantic Versioning](https://semver.org/)
and are made on the public repository (ADR-0022).

1. Make sure `main` is green in CI and `CHANGELOG.md` has a section for the
   new version (move the *Unreleased* items under it). That section becomes
   the release notes on GitHub, so keep it short: what changed for people,
   in a few lines per item.
2. Tag and push:

   ```bash
   git tag -s v1.2.0 -m "Sentinela v1.2.0"   # -a instead of -s without a signing key
   git push origin v1.2.0
   ```

   The `release` workflow runs the checks, publishes
   `ghcr.io/luker2dw/sentinela:1.2.0` (plus `1.2` and `latest`) with an SBOM
   and provenance, builds the Linux binary and creates the GitHub release.
   The same run builds the generic Android app without any key, signs it
   in a separate job (the only one that can read the key; it runs no npm
   or Gradle code) and attaches `sentinela.apk` and `app.json`. Every Sentinela server whose
   `app_updates.github` points here offers it to its phones at its daily
   update time, or right away after *Check for updates* in System → Updates.
   The release page shows that version's CHANGELOG section and a link to the
   full comparison.

## One-time setup: the app signing key

Android only installs an update signed with the same key as the installed
app, so the release job needs the existing key. On the maintainer's machine
(the key never goes into the repository):

```bash
gh api -X PUT repos/LUKER2DW/Sentinela/environments/release
base64 -w0 ~/.sentinela-android/release.jks | gh secret set ANDROID_KEYSTORE_B64 --env release -R LUKER2DW/Sentinela
gh secret set ANDROID_KEYSTORE_PASSWORD --env release -R LUKER2DW/Sentinela < ~/.sentinela-android/keystore.pass
```

Before building anything the workflow refuses a tag that is not a plain
`vX.Y.Z` without leading zeros, whose commit is not on `main`
(`refs/remotes/origin/main`, named in full), that is not newer than every
published release or that does not contain the latest one; the gradle
wrapper is checked against Gradle's checksums. That guards against mistakes
and against a reviewer approving a tag that only looks right, but anyone who
can push a tag runs the workflow file at that commit, so the signing key is
only as safe as these hosted settings. Set them before the first release and
keep them (Settings of the repository):

- **Environments → release**: *Required reviewers*: the maintainers (and
  *Prevent self-review* once there is more than one); *Allow administrators
  to bypass* off; *Deployment branches and tags* → *Selected* with only the
  tag pattern `v*.*.*` (no branch). The two secrets live only here, never
  as repository or organization secrets.
- **Rules → Rulesets → New tag ruleset**: target all tags (`~ALL`), enforcement
  *Active*, bypass list only the maintainers' role; rules *Restrict
  creations*, *Restrict updates* and *Restrict deletions*. Nobody else can
  then push `v*` tags, move one, or push a tag named like a branch.
- **Rules → Rulesets → branch ruleset for `main`**: *Restrict deletions*,
  *Block force pushes*, *Require a pull request before merging* and
  *Require status checks to pass* with the `ci` check.
- **General → Releases**: *Enable release immutability*, so a published
  release's assets and tag cannot be changed.
- **Collaborators and Actions**: write access only for maintainers; *Actions
  → General → Workflow permissions* read-only; no personal access tokens
  with write access to this repository or its environments.

## Using a published image on a server

Instead of building on a computer, a server can pull a release:

```bash
sudo docker pull ghcr.io/luker2dw/sentinela:1.2.0
sudo docker tag ghcr.io/luker2dw/sentinela:1.2.0 sentinela:local
cd /opt/sentinela && sudo git fetch --tags && sudo git checkout v1.2.0
cd deploy && sudo docker compose up -d --no-build
```

Check the provenance first if you like:
`gh attestation verify oci://ghcr.io/luker2dw/sentinela:1.2.0 -R LUKER2DW/Sentinela`.
