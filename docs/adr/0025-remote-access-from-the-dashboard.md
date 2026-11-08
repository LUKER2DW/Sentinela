# ADR-0025: Remote access from the dashboard, tunnels and reverse proxies

Status: accepted (2026-11-08). Amends ADR-0019 and ADR-0024; replaces the
"no Cloudflare tunnels" rule in AGENTS.md §2.5.

## Context

Remote access needed SSH: `setup-tailscale.sh`, `setup-public.sh`, editing
`remote.*` in `config.yaml` and a redeploy, with no way to tell whether an
address really reached the house. Homes behind CGNAT (no public IP) had only
Tailscale, so phones without the Tailscale app could not get in, and the
port-forward always used port 7443. Owners also asked for their own domain
through Cloudflare Tunnel or a reverse proxy they already run.

## Decision

- **System → Remote access** (admin, from home or the tailnet) sets up all
  ways in; the welcome guide links to it and sets up Tailscale inline.
- **Tailscale.** The person creates an auth key in the Tailscale admin
  console and pastes it. The API writes it once (0600) with a request under
  `data_dir/tailscale`; the host agent (`scripts/tailscale-agent.sh`,
  `sentinela-tailscale.path`, root) moves it to a root-only file, installs
  Tailscale from its apt repository if needed, runs
  `tailscale up --auth-key=file:…` (never on a command line or in a log),
  deletes it and publishes the UI with `tailscale serve`. It writes a status
  (`/var/lib/sentinela/tailscale.json`: installed, connected, MagicDNS,
  HTTPS certificates, serve) that the page shows as a checklist; "Use this
  address" saves `remote.tailnet_url` through the settings flow.
- **Public address (port-forward, ADR-0019).** Domain or fixed public IP,
  the server's port, the router's port and the certificate mode are now
  edited in the dashboard (ADR-0024 flow). The firewall opens exactly the
  ports the config names (`sentinela public-ports`) and removes earlier
  public rules, so a changed port or a switched-off address closes the old
  one; `apply-config.sh` keeps the public-access marker in step with the
  config.
- **Tunnel or reverse proxy (`remote.proxy`).** A listener on loopback
  (`127.0.0.1:7444` by default, plain HTTP) for a proxy on this server that
  terminates TLS for a domain: Cloudflare Tunnel, Caddy, nginx, ngrok...
  Every request on it is in the public zone `proxy`: the closed door,
  login limits, two-step login where the account has it, and login alerts,
  exactly as the port-forward. The client address comes from the proxy's
  `X-Forwarded-For` (trusted from loopback only). Live video uses MSE; talk
  is off there (no WebRTC media through an HTTP tunnel).
- **Cloudflare Tunnel, guided.** The person creates a remotely-managed
  tunnel in Cloudflare Zero Trust and pastes its install command or token.
  The host agent (`scripts/cloudflared-agent.sh`) installs `cloudflared`
  from Cloudflare's apt repository and runs `sentinela-cloudflared.service`
  with the token from a root-only `EnvironmentFile` (dynamic user, read-only
  system, no capabilities, 256 MB). The public hostname's service is
  `http://127.0.0.1:7444`. No container is added; cloudflared is a host
  service like tailscaled.
- **Reachability check.** `GET /.well-known/sentinela` answers a random
  per-process id on every listener; `GET /api/remote/check` (admin) fetches
  it through the configured public address or tunnel (from the config only,
  never from the request), with the pinned certificate when self-signed,
  and reports DNS, connection, certificate or "another server" problems and
  the home's public IP.

## Consequences

+ Remote access is set up and verified from the browser, also behind CGNAT.
+ A tunnel or proxy can never widen access: its listener is always public
  and only reachable from this host.
+ Secrets (auth key, tunnel token) are written once, read by root, and
  never returned, logged or put on a command line.
− Cloudflare terminates TLS for the tunnel: it can see that traffic.
  Tailscale and the port-forward keep end-to-end TLS; the page says so.
− Two more host agents (Tailscale, cloudflared) run as root oneshots on
  request and every ten minutes for status.
