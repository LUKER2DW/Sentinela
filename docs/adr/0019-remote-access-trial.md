# ADR-0019: Remote access without Tailscale on the phone — three-way trial

Status: **Accepted for a trial** · 2026-09-29 (owner decision D8). Suspends
AGENTS.md §2.5 and ADR-0004 *for the paths below only* until the owner picks
a winner; the losers are then removed and this ADR is rewritten as the
decision.

## Context
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

## Decision

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
