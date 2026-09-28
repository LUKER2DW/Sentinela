# Security

## Threat model (home)

| Threat | Likelihood | Control |
|---|---|---|
| Internet scan hits camera (XMEye firmware has a long CVE/botnet history) | High if exposed | Camera has **no WAN egress/ingress**; cloud/P2P disabled |
| Internet scan hits server | High if exposed | No port forwarding; access only over Tailscale |
| Stolen phone | Medium | Tailscale device revocation; session expiry 30 d; per-device sessions revocable |
| LAN guest/IoT device attacks camera | Medium | Separate VLAN/SSID for cameras if router supports; strong camera password |
| Credential leak via logs/git | Medium | Redaction helper; `.env` git-ignored; secret scanning pre-commit (gitleaks) |
| Web attacks (CSRF/XSS/session theft) | Low (private) | SameSite=Strict HttpOnly cookies, CSRF token, strict CSP, no inline scripts |

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
   `secure_cookies: true`; CSRF header on unsafe methods; Origin checked.
   The first-run setup page only works while no user exists.
5. All camera writes and user management actions are written to `audit`.
6. Containers run as non-root, read-only root FS, `cap_drop: [ALL]`,
   no Docker socket, no host PID, no privileged mode.
7. Dependencies pinned; containers pinned by digest; `govulncheck` in `make check`.
8. Host: SSH is key-only; ufw allows only 192.168.1.0/24 (22, 8080, 8555,
   SMB 139/445, discovery, NTP). Samba binds to `eno1`, one account, no guest.
   The web container mounts the NAS read-only and gets SMART data as a file,
   never device access. The owner granted `ana` passwordless sudo for
   automated administration (`/etc/sudoers.d/90-ana-admin`); remove it
   when not needed.

## Reporting

Private project. Record findings in `docs/CAMERA.md` §3 (camera) or as an
issue marked `security`.
