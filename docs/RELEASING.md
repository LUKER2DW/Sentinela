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
3. Build the generic Android app on the maintainer's machine and attach it:

   ```bash
   SENTINELA_GENERIC_APK=1 ./scripts/build-apk.sh
   sha256sum mobile/build/sentinela.apk
   gh release upload v1.2.0 mobile/build/sentinela.apk
   ```

   Add the APK's SHA-256 to the release notes.

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
