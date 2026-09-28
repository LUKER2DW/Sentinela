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

1. The camera password is stored encrypted (AES-256-GCM) with a master key in
   `/etc/sentinela/secrets.env` (0600, root). Never in the DB in clear text.
2. RTSP URLs are built server-side; the API never returns them.
3. go2rtc API is loopback-only; the browser gets stream access only through a
   ticket (random 256-bit, stored hashed, 60 s TTL, bound to user+camera).
4. Passwords: argon2id (m=64 MB, t=3, p=1). Login rate limit 5/min/IP.
5. All camera writes and user management actions are written to `audit`.
6. Containers run as non-root, read-only root FS, `cap_drop: [ALL]`,
   no Docker socket, no host PID, no privileged mode.
7. Dependencies pinned; containers pinned by digest; `govulncheck` in `make check`.

## Reporting

Private project. Record findings in `docs/CAMERA.md` §3 (camera) or as an
issue marked `security`.
