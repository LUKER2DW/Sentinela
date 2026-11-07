# Contributing to Sentinela

Thanks for helping! Sentinela is a small project with a few strong rules, so
a quick read here saves everyone a round of review.

## Before you start

- **Bugs:** open an issue with the bug template. Include `sentinela version`,
  how you deployed, the camera brand/model, and logs with secrets removed.
- **Security problems:** never in a public issue. See [SECURITY.md](SECURITY.md).
- **Features:** open an issue first and describe the problem you want to
  solve. Some ideas, however good, do not fit the hard rules below.

## The hard rules

These come from [AGENTS.md](AGENTS.md), which is the full contributor guide:

1. **No transcoding.** Video is copied, never re-encoded. A change that
   decodes video needs a measured CPU budget and an ADR in `docs/adr/`.
2. **Recording never depends on anything else.** If the web app, the
   database or go2rtc stop, recording continues.
3. **Stay inside the budget:** the Go binary ≤ 80 MB RAM and ≤ 3 % CPU, the
   whole stack ≤ 600 MB, on an old 4 GB machine.
4. **Secrets stay on the server.** Never log, return or commit camera
   passwords, RTSP URLs with credentials or tokens.
5. **Camera writes are allow-listed** in [docs/CAMERA.md](docs/CAMERA.md).
6. **Keep it small.** No new service, database or queue without an ADR.
7. **Nothing site-specific.** No real addresses or names; everything is
   configurable with a generic default.

## Development setup

See the README's [Development](README.md#development) section. In short:

```bash
cd web && npm ci && npm run build && cd ..
go run ./cmd/sentinela serve -config ./dev-config.yaml   # git-ignored
cd web && npm run dev
```

Before you push, run the same checks as CI:

```bash
make check   # go vet, go test, staticcheck, govulncheck, web lint and tests
```

On Windows without `make`, run the steps of the `check` target by hand.

## Pull requests

- One concern per PR, small enough to review in one sitting.
- Tests for new behaviour (Go: table-driven; web: Vitest).
- Commit messages in [Conventional Commits](https://www.conventionalcommits.org/)
  style, with a body that says *why* when it is not obvious.
- Update the docs only when behaviour changes.
- UI text is Portuguese (Brazil); code, comments and docs are English.

## Licence

By contributing you agree that your work is released under the
[MIT licence](LICENSE). Do not copy code from GPL projects (ZoneMinder,
Shinobi, Moonfire NVR, motionEye…); ideas are welcome, code is not.
MIT/Apache sources may be adapted with attribution in [NOTICE](NOTICE).
