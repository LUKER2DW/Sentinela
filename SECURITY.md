# Security policy

Sentinela records cameras inside people's homes, so security reports are
welcome and handled first.

## Reporting a vulnerability

**Please do not open a public issue for a vulnerability.**

Use GitHub's private reporting instead: the repository's **Security** tab →
**Report a vulnerability**. Include:

- what an attacker can do, and from where (internet, LAN, a signed-in
  viewer, a self-hosted service…);
- the version (`sentinela version` or the commit) and how it was deployed;
- steps or a proof of concept, kept to what is needed to reproduce.

You will get an answer within 7 days. Fixes land on `main` with a security
note in the commit message; the reporter is credited unless they prefer not
to be. Please give us 90 days, or until a fix is released, before
disclosing.

## Supported versions

Only the latest `main` is supported. Sentinela is self-hosted: update with
`./scripts/deploy.sh` (see [docs/INSTALL.md](docs/INSTALL.md#11-updating)).

## Scope

In scope: the Go server (`cmd/`, `internal/`), the web app (`web/`), the
Android app (`mobile/`), the detector (`detector/`), and the deployment
files and scripts (`deploy/`, `scripts/`).

Out of scope: vulnerabilities in the cameras' own firmware (report those to
the vendor), in go2rtc, FFmpeg or other upstream projects (report upstream;
tell us if Sentinela should pin a fixed version), and setups that ignore
the documented rules, such as forwarding camera, RTSP, SMB or go2rtc ports
to the internet.

## How Sentinela is built to be safe

The full threat model and rules are in [docs/SECURITY.md](docs/SECURITY.md).
In short:

- **No exposure by default.** Everything listens on the LAN. Remote access
  is Tailscale or one opt-in TLS listener behind a "closed door": without a
  session the internet sees only the login page.
- **Accounts.** argon2id passwords with a strength policy, per-address and
  per-account login limits, optional TOTP two-step login, revocable
  sessions, alerts on logins from outside, roles (viewer, operator, admin)
  and per-camera visibility. Every change is audited.
- **Web hardening.** SameSite=Strict HttpOnly cookies, CSRF token and
  Origin checks, a strict Content-Security-Policy, sandboxed user files.
- **Secrets stay on the server.** Camera passwords live only in a root-only
  `secrets.env`; RTSP URLs are built on the server and redacted from logs;
  the browser gets single-use, 60-second stream tickets.
- **Cameras.** Only allow-listed DVRIP commands are ever sent
  ([docs/CAMERA.md](docs/CAMERA.md)); persistent writes read back and roll
  back on failure.
- **Containers.** Non-root, read-only, no capabilities. Only the optional
  self-hosted services agent holds the Docker socket, behind loopback and a
  token; user services are capped, cannot reach private networks, and their
  Compose files are checked (no privileged mode, host namespaces, devices,
  extra capabilities, or binds outside the project, symlinks included).
- **Supply chain.** Pinned images and actions, `govulncheck` and
  `staticcheck` in CI, no secrets in CI, no self-hosted runners.

## For operators

- Keep the server updated (`unattended-upgrades`) and deploy new releases.
- Never forward camera, RTSP (554), DVRIP (34567), ONVIF, SMB or go2rtc
  (1984/8554) ports. Block the cameras' internet access on the router.
- Prefer Tailscale for remote access. If you open the public listener,
  turn on two-step login for every admin account.
- Run `scripts/restore-drill.sh` after setting up backups, then monthly.
