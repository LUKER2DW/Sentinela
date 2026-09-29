# Security

## Threat model (home)

| Threat | Likelihood | Control |
|---|---|---|
| Internet scan hits camera (XMEye firmware has a long CVE/botnet history) | High if exposed | Camera has **no WAN egress/ingress**; cloud/P2P disabled |
| Internet scan hits server | High if exposed | No port forwarding; access only over Tailscale. **ADR-0019 trial:** the P1/P3 listeners are public, behind mandatory 2FA and the closed door (rule 14) |
| Stolen phone | Medium | Tailscale device revocation; session expiry 30 d; per-device sessions revocable |
| LAN guest/IoT device attacks camera | Medium | Separate VLAN/SSID for cameras if router supports; strong camera password |
| Credential leak via logs/git | Medium | Redaction helper; `.env` git-ignored; secret scanning pre-commit (gitleaks) |
| Web attacks (CSRF/XSS/session theft) | Low (private) | SameSite=Strict HttpOnly cookies, CSRF token, strict CSP, no inline scripts |
| Compromised or lost tailnet device | Medium | Tailscale ACL: only `group:family` reaches `tag:nvr`, only ports 443/8443/8555 (22 for admins); key expiry on phones; app sessions revocable |
| Supply chain via CI / auto deploy | Medium | Retired (ADR-0010): the server holds no GitHub credential and runs no runner; CI runs on GitHub-hosted runners only |
| Data loss in the Nuvem (bug, mistake, ransomware on a client) | Medium | Recycle bin, audit of API writes, nightly restic to an external disk (ADR-0014) |

## Rules

1. The camera password lives only in `/etc/sentinela/secrets.env` (0600,
   root), injected as env into the containers. It is never stored in the DB,
   logged (redact helpers) or returned by the API. (Changed 2026-09-28 from
   "AES-GCM in the DB": with cameras in config, a DB copy would add a second
   place for the same secret without extra protection.)
2. RTSP URLs are built server-side; the API never returns them.
3. go2rtc API is loopback-only; the browser gets stream access only through a
   ticket (random 256-bit, stored hashed in memory, 60 s TTL, single use,
   bound to user+camera, plus a valid session cookie).
4. Passwords: argon2id (m=64 MB, t=3, p=1), min 8 chars (owner's choice). Login rate limit
   5/min/IP. Sessions: 30 days, HttpOnly, SameSite=Strict, `Secure` when
   the request arrived over HTTPS (TLS, or `X-Forwarded-Proto: https` from
   the loopback `tailscale serve` proxy), so LAN HTTP keeps working;
   `secure_cookies: true` forces it. CSRF header on unsafe methods; Origin
   checked. Forwarded headers are trusted **only** from 127.0.0.1/::1;
   the login rate limit keys on the forwarded client IP.
   The first-run setup page only works while no user exists.
5. All camera writes and user management actions are written to `audit`.
6. Containers run as non-root, read-only root FS, `cap_drop: [ALL]`,
   no Docker socket, no host PID, no privileged mode.
7. Dependencies pinned; containers pinned by digest; `govulncheck` in `make check`.
8. Host: SSH is key-only; ufw allows only 192.168.1.0/24 (22, 8080, 8555,
   SMB 139/445, discovery, NTP) and, on `tailscale0`, only 443/tcp
   (`tailscale serve`), 8443/tcp (`serve` for the ntfy relay), 8555/tcp+udp
   (WebRTC) and 22/tcp. 8080 is never
   reachable over the tailnet: remote users always get HTTPS. Samba binds to
   `eno1`, no guest; it joins the tailnet only after per-user passwords of
   ≥ 12 characters and the owner's approval.
   The web container mounts the NAS read-write for the file API (ADR-0012:
   `os.Root` confinement, per-user folders, deletes go to the recycle bin,
   user files served with a sandbox CSP and never inline unless media or
   PDF; an inline PDF drops the sandbox, which browsers refuse to render
   PDFs in, but keeps `default-src 'none'` and may only be framed by the
   app itself) and
   gets SMART data as a file, never device access. The recorder cannot see
   the NAS. The owner granted `ana` passwordless sudo for
   automated administration (`/etc/sudoers.d/90-ana-admin`); remove it
   when not needed.

9. Remote access is Tailscale only (ADR-0004): no router port-forward, no
   subnet router or exit node on the server, the camera never joins the
   tailnet. The HTTPS name `sentinela.<tailnet>.ts.net` is published in
   Certificate Transparency logs, so use a randomized tailnet name. Key
   expiry stays on for phones and off for the server; enable Tailnet Lock.
   Policy to paste in the admin console (replace the e-mails):

   ```json
   {
     "groups":    {"group:family": ["owner@example.com"]},
     "tagOwners": {"tag:nvr": ["autogroup:admin"]},
     "acls": [
       {"action": "accept", "src": ["group:family"],    "dst": ["tag:nvr:443,8443,8555"]},
       {"action": "accept", "src": ["autogroup:admin"], "dst": ["tag:nvr:22"]}
     ],
     "ssh": []
   }
   ```
   Add `445` to the family rule only when SMB over the tailnet is approved.
   The server gets the tag with `tailscale up --advertise-tags=tag:nvr …`
   (`scripts/setup-tailscale.sh`); without it no rule matches and
   tailscaled drops everything ("no rules matched" in its journal).
   **The ACL is the only effective filter on the tailnet:** tailscaled puts
   `ts-input -i tailscale0 -j ACCEPT` ahead of ufw, so the ufw rules for
   `tailscale0` (rule 8) are documentation of intent, not enforcement.
10. Deploys are manual over SSH (ADR-0010). GitHub Actions is CI only.
11. Backups (ADR-0014): `/srv/backup/etc` holds a copy of `secrets.env`, so
    it is root-only (0700); the restic repository is encrypted with a
    password kept in `/etc/sentinela/restic.env` (0600) **and** in the
    owner's password manager (without it the backup is unreadable).
12. Notifications (ADR-0013): the server only POSTs to allow-listed push
    services (outbound HTTPS) or to `up*` topics of its own ntfy relay
    (loopback); payloads are end-to-end encrypted and hold no image. VAPID private key in `/data/push/vapid.json` (0600).

13. Object detector (ADR-0016): `/internal/detector/*` answers only loopback
    peers with the bearer token from `/data/detector/token` (0640, uid 1000,
    created by `serve`); the detector reads video only from go2rtc's
    loopback restream and never sees camera credentials. Model weights are
    pinned by sha256 at image build.

14. Remote access trial (ADR-0019). Requests are zoned **by listener**
    (`lan`, `tailnet`, `funnel`, `public`), never by header. In `funnel`
    (`127.0.0.1:8081`, Tailscale Funnel on :10000) and `public` (`:7443`,
    Let's Encrypt via TLS-ALPN-01, router WAN 443 → 7443):
    - login needs password **and** a TOTP code (RFC 6238, a code works
      once); accounts without 2FA cannot log in from outside, and 2FA is
      enrolled/disabled only from `lan`/`tailnet`;
    - without a 2FA session only the SPA shell, `GET /api/setup`,
      `POST /api/login|logout` answer; everything else is 404
      (`/healthz` and `POST /api/setup` included); sessions created at
      home are ignored there;
    - 10 login attempts per account per hour on top of 5/min/IP; every
      outside login is audited with its zone and pushed to the user and
      the admins;
    - users, 2FA and app keys are managed only from `lan`/`tailnet`.
    P2 (Tailscale in the APK): the server mints single-use, pre-approved,
    10-minute auth keys tagged `tag:sentinela-app` only for signed-in `lan`
    or `tailnet` requests, with an OAuth client (`auth_keys` scope) kept in
    `secrets.env`. ACL: `tag:sentinela-app` → `tag:nvr:443,8555` only. The
    app's local proxy on 127.0.0.1 answers only requests with a per-process
    random cookie. The losing paths are removed after the trial.

## Reporting

Private project. Record findings in `docs/CAMERA.md` §3 (camera) or as an
issue marked `security`.
