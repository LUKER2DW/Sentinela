# ADR-0019: Remote access without Tailscale on the phone — three-way trial

Status: **Accepted P1 public HTTPS path after the trial** · 2026-09-29
(owner decision D8). Amends ADR-0004/AGENTS.md only for that TLS/WebRTC path.
Embedded tailnet was removed; Funnel is disabled.

## Result (2026-09-29, owner, on the phone)
P1 (public address) worked, P3 (Funnel) worked, P2 (embedded tailnet) did
not. **P1 is kept** for the app and for browsers. P2 is removed (server
endpoint, app code, AAR; `mobile/tailnet` parked). P3 is switched off in
the server config, its code kept so it can return by configuration. The
public listener keeps mandatory two-step login and the closed door; first
access by invite code enrolls it from outside. The Android app gets
notifications through Firebase wake-ups and a pinned HTTPS fetch (ADR-0020);
the direct connection from round 4 remains its fallback.

## Current household configuration (2026-09-30)

- `remote.public.self_signed: true`: the fixed IP uses a persistent
  self-signed certificate under `/data/acme/self`. The ISP did not provide
  the normal ACME challenge ports used by the original trial options.
- Public TLS is WAN 18443 → server 7443, plus the approved WebRTC 8555
  allowance. HTTP-01 on 7080 is off. Camera/SMB/go2rtc APIs remain private.
- Android trusts normal system CAs or the exact household certificate pin.
  Browsers may show the self-signed warning; the app must not bypass TLS.
- APK defaults come from `~/.sentinela-android/connection.json` or explicit
  environment overrides. Release builds reject missing/malformed pins.
  `setup-public.sh status` prints the fingerprint over trusted SSH.
- APK 1.11 omitted its pin and could not connect. Install the same-key 1.12
  manually once; certificate validation and later in-app updates then work.
- Invited first access can enroll TOTP using its one-time link; ordinary
  account/TOTP administration remains on private access.

The trial designs below are historical alternatives, not instructions to
enable every listener or to reinstall the removed app tailnet.

## Amendment (2026-11-06, owner): two-step login is optional

The owner chose convenience for the household: two-step login is no longer
required from outside. It stays available and, once an account turns it
on, its code is asked everywhere and a session made without it does not
open the public door. Without it, the public path relies on the password
policy, the closed door (nothing behind the login answers without a
session), per-account and per-address limits, and the outside-login push
alert. Invite/password-reset links no longer enroll TOTP. The Android app's
session no longer expires (it ends on sign-out, device revocation, password
change or account disable); remembered browser sessions slide 30 days from
last use. Accepted risk: a leaked password alone now opens the account
remotely; accounts that want the stronger setting turn TOTP on in Minha
conta.

## Original trial context
Today every client needs the Tailscale app (ADR-0004). The owner wants to
install only the Sentinela APK and open it, and also to open the web UI from
any browser on any network, with the lowest live latency possible. No single
option gives all three with the security of the tailnet, so the owner will
measure three on the phone and keep one.

| Path | Browser anywhere | APK only | Live path | Exposure |
|---|---|---|---|---|
| **P1 Port-forward + domain** | yes | yes | WebRTC direct (UDP 8555) | Public TLS port, login reachable by anyone |
| **P2 Tailscale embedded in the APK** | no | yes | WebRTC over the app's tailnet node, or MSE | None (tailnet only) |
| **P3 Tailscale Funnel** | yes | yes | MSE over WebSocket (Funnel is TCP only) | Public `*.ts.net` port, login reachable by anyone |

## Historical trial design

### Access zones (all paths)
The API tags every request with a zone **by the listener it arrived on**,
never by a header:

| Listener | Zone | Who |
|---|---|---|
| `listen` (`:8080`), peer not loopback | `lan` | home Wi-Fi |
| `listen`, peer loopback (`tailscale serve :443`) | `tailnet` | Tailscale app, P2 app node |
| `remote.funnel_listen` (`127.0.0.1:8081`) | `funnel` (public) | P3 |
| `remote.public.listen` (`:7443`, TLS) | `public` | P1 |

Public zones (`funnel`, `public`) add:
1. **Two-step login (TOTP, RFC 6238)** is mandatory. A user without 2FA
   cannot log in from outside; 2FA is enrolled only from `lan`/`tailnet`,
   so a stolen password alone never opens anything remotely.
2. **Closed door:** without a session made with 2FA, only the SPA shell,
   `GET /api/setup` and `POST /api/login`/`logout` answer; everything else
   is 404. `POST /api/setup` and `/healthz` are refused. Sessions made at
   home (without 2FA) are ignored in public zones.
3. Per-account lockout (10 failures/hour) on top of the 5/min/IP limit, and
   a push to admins on every successful outside login.
4. Admin actions that grant access (users, 2FA reset, tailnet app keys)
   only from `lan`/`tailnet`.

TOTP is stdlib only (HMAC-SHA1, 30 s, 6 digits, ±1 step, no step reuse);
the QR code is drawn by `rsc.io/qr` (BSD-3, no dependencies). Passkeys
(WebAuthn) are the follow-up for the winner: each path has its own origin
(and the P2 WebView has no WebAuthn), so they wait for one origin.

### P1 — port-forward + domain
- `sentinela serve` opens `remote.public.listen` (`:7443`) with a Let's
  Encrypt certificate for `remote.public.domain` from
  `golang.org/x/crypto/acme/autocert` (already a dependency; TLS-ALPN-01, so
  no port 80). Cache in `/data/acme` (0700).
- Router (owner): WAN 443/tcp → server 7443; WAN 8555/tcp+udp → server
  8555. `scripts/setup-public.sh enable|disable` opens/closes 7443 and 8555
  to any source in ufw (marker `/etc/sentinela/public-access`).
- go2rtc adds `stun:8555` to its candidates so browsers outside learn the
  home's public address (outbound STUN only).
- Needs a domain whose A record points at home; a changing IP needs the
  registrar's or the router's dynamic DNS (chosen with the owner).
- **No domain (2026-09-29, owner):** the home has a fixed public IP, so
  `remote.public.domain` may be that IP. Let's Encrypt signs IP addresses
  only with its `shortlived` profile (160 h), which autocert cannot request,
  so `internal/publiccert` uses `github.com/go-acme/lego/v4` (MIT; pulls
  go-jose, miekg/dns, backoff): TLS-ALPN-01 on the same listener, EC key,
  renewal with a third of the lifetime left, files in `/data/acme/ip`
  (0600). Browsers and the app open `https://<ip>` with a valid certificate,
  no warning. Router: the same two forwards.
- **CGNAT check first:** if the router's WAN IP differs from the public IP,
  port-forwarding cannot work (ask the ISP for a public IP, or IPv6).

### P2 — Tailscale embedded in the APK
- `mobile/tailnet/` (own Go module, never linked into the server) wraps
  `tailscale.com/tsnet` for gomobile: a userspace node inside the app — no
  Android VpnService, no VPN icon, only the app's traffic. It serves a
  reverse proxy on `127.0.0.1:<random port>` to
  `https://sentinela.<tailnet>.ts.net`, rewriting `Origin` and requiring a
  per-launch random token cookie (other apps on the phone cannot use it),
  plus TCP/UDP forwarders `127.0.0.1:8555 → server:8555` for WebRTC.
- go2rtc adds the candidate `127.0.0.1:8555`, which only the app answers
  (elsewhere it fails fast; ICE uses the other candidates).
- Enrollment: in the app, on home Wi-Fi, the user signs in with their
  Sentinela account; the server mints a **single-use, pre-approved,
  10-minute** key tagged `tag:sentinela-app` through the Tailscale API
  (OAuth client in `secrets.env`, `TS_OAUTH_CLIENT_ID/SECRET`), only for
  `lan` requests. A key pasted by hand also works. The node state lives in
  the app's private storage; revoke the device in the admin console.
- ACL: `tag:sentinela-app` reaches only `tag:nvr:443,8555`.

### P3 — Tailscale Funnel
- `scripts/setup-funnel.sh enable|disable`:
  `tailscale funnel --bg --https=10000 http://127.0.0.1:8081`. 443 stays
  tailnet-only (`serve`); Funnel allows only 443/8443/10000.
- Needs the `funnel` node attribute in the ACL for `tag:nvr`.
- The SPA skips WebRTC in the `funnel` zone (no UDP, no 8555) and goes
  straight to MSE.

### The app
The APK gets a start screen (bundled) to pick the path: domain URL (P1),
embedded tailnet (P2, with enrollment) or Funnel URL (P3), switchable later
from the same screen, so one build tests all three. The UI shows the zone
("Conectado por: …") to compare.

## Consequences
+ The owner measures real latency and usability of all three on the same
  phone and network.
− P1/P3 put the login page on the internet: the 2FA and the closed door are
  what keep a bug behind the login unreachable. Review before enabling.
− P2 adds ~25–35 MB to the APK and needs the Android NDK + gomobile on the
  dev PC.
− Notifications stay as today (ntfy over the tailnet) during the trial; the
  winner decides the replacement (e.g. a public ntfy topic with no image).
− Removal after the trial: drop the losing listener/config, script and app
  mode; P1 and P3 share all the zone/2FA code.
