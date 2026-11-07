# Releasing

Maintainers only. Releases follow [Semantic Versioning](https://semver.org/)
and are made on the public repository (ADR-0022).

1. Make sure `main` is green in CI and `CHANGELOG.md` has a section for the
   new version (move the *Unreleased* items under it).
2. Tag and push:

   ```bash
   git tag -s v1.2.0 -m "Sentinela v1.2.0"   # -a instead of -s without a signing key
   git push origin v1.2.0
   ```

   The `release` workflow runs the checks, publishes
   `ghcr.io/luker2dw/sentinela:1.2.0` (plus `1.2` and `latest`) with an SBOM
   and provenance, builds the Linux binary and creates the GitHub release.
   The same run builds and signs the generic Android app (`sentinela.apk`
   and `app.json`) and attaches them. Every Sentinela server whose
   `app_updates.github` points here offers it to its phones within 6 hours.

## One-time setup: the app signing key

Android only installs an update signed with the same key as the installed
app, so the release job needs the existing key. On the maintainer's machine
(the key never goes into the repository):

```bash
gh api -X PUT repos/LUKER2DW/Sentinela/environments/release
base64 -w0 ~/.sentinela-android/release.jks | gh secret set ANDROID_KEYSTORE_B64 --env release -R LUKER2DW/Sentinela
gh secret set ANDROID_KEYSTORE_PASSWORD --env release -R LUKER2DW/Sentinela < ~/.sentinela-android/keystore.pass
# optional, keeps Firebase notifications for servers with this Firebase project:
gh secret set ANDROID_GOOGLE_SERVICES_JSON --env release -R LUKER2DW/Sentinela < ~/.sentinela-android/google-services.json
```

When the repository is public, protect the environment: Settings →
Environments → release → *Required reviewers* (yourself) and *Deployment
branches and tags* → tags `v*`.

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
