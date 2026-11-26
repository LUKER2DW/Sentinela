# Security

Current policy: 2026-09-30. Historical public-access and notification choices
are superseded where noted by ADR-0019 and ADR-0020.

## Threat model

| Threat | Control and current limits |
|---|---|
| Camera attacked from the internet | Never forward camera ports. Cloud/P2P, UPnP and WAN egress isolation remain operator checks; do not assume they are complete |
| Public server login attacked | Only the approved TLS/WebRTC path; closed door, rate limits, outside-login alert, optional TOTP (ADR-0019 amendment) |
| Stolen phone/session | Native app lock, revocable sessions, expiration and session-bound FCM tokens |
| Another app or web page on the phone | The Android app opens only `/#/` pages from its own signed notifications and invite hand-offs, and saves a download only after a tap; its native session copy is bound to the paired server and certificate pin, and pairing another house signs the app out |
| LAN guest or IoT attack | Firewall and camera isolation; a separate camera VLAN where available |
| Secrets in Git/logs/bundles | Runtime secret files, redaction, ignored local release files and review before commit |
| CSRF/XSS | Same-origin cookies, CSRF header and Origin check, CSP and file-response restrictions |
| Cloud loss/ransomware | Recycle bin, audit, local DB/config backup; external restic/restore acceptance still pending |
| Build pipeline competes with recording | NVR images are built on the PC. Self-hosted services build on the server (ADR-0021): one at a time, ≤ 1 core / 1 GB, refused under 400 MB free RAM, on the system disk |
| A self-hosted service is compromised | No capabilities, capped resources, no private-range egress (camera, LAN, tailnet) and no host services (ufw); only the `apps` agent holds the Docker socket, behind loopback + token; Compose files are checked before they run |
| Dashboard admin session abused to run code | Creating or changing services needs admin on LAN/tailnet; from the internet an admin session (2FA when the account turned it on) can only start/stop/redeploy configured ones; every change audited |
| Notification third party | Browser payload encryption; Android sends only an opaque id through Firebase and fetches details from home |

## Rules

1. Camera credentials belong in `/etc/sentinela/secrets.env`, root 0600,
   injected into the appropriate containers. They are never stored in the
   DB, returned by the API, committed or logged. RTSP userinfo and password
   query parameters must pass through the redaction helpers.
2. RTSP URLs are constructed on the server. A browser never receives them
   or calls the camera directly.
3. go2rtc's API/restream are loopback-only. Stream tickets are random,
   single-use, 60-second, stored hashed, user/camera-bound, and require an
   authenticated session when consumed. Talk needs an operator: only the
   `<id>_talk` go2rtc stream reaches a camera's speaker, only operators get
   a ticket for it, and watching streams carry no backchannel at all.
   Loopback keeps the network out, but
   every container uses the host network and shares that loopback, so
   go2rtc's API (which holds every camera credential) also requires a
   password, from loopback callers too (`local_auth`), and answers only
   `/api/ws`, `/api/streams`, `/api/frame.jpeg` and the restart and exit a cut needs. The password
   (`GO2RTC_API_PASSWORD`, `scripts/go2rtc-secret.sh`) is in `secrets.env`,
   which the detector (it decodes camera video) never gets; without it
   go2rtc does not start. Residual: go2rtc 1.9.14 exempts loopback clients
   from RTSP credentials, so any host-network container can still read the
   restream (video only, no credential) and add a publisher to a configured
   stream. Requests to cameras (ONVIF) never follow
   HTTP redirects, so a device at a camera address cannot point them there.
   Live streams (`/stream/ws`, event and notification streams) re-check the
   session every 45 s and end when it is revoked or the account is
   disabled; a narrowed camera list applies to open streams too. WebRTC media does not end with its WebSocket in go2rtc, so
   the API tags every stream it opens there and sweeps go2rtc's consumers:
   one left by a revoked session, a disabled, demoted or deleted account or
   a camera taken away is cut. go2rtc 1.9 cannot drop one consumer or one
   stream's consumers (`DELETE /api/streams` leaves them running), so a cut
   restarts go2rtc: every live view and talk reconnects, and the detector's
   restream and event snapshots pause for a few seconds (detection is blind
   meanwhile). Recording does not use go2rtc. Cuts are rationed: one cut
   takes every stale consumer found, cuts are at least 2 minutes apart, and
   each is charged to the account whose stream it cut (an admin's change to
   the account it changed). An account's first cut in an hour is free;
   each further one refuses it new stream tickets for 10 minutes, doubling
   up to a day, with a `live.cut` audit row naming it (in memory; a quiet
   day after the cool-down clears it). The web client closes its WebRTC
   media with its WebSocket and never causes a cut. An admin change, a
   revoked session or a sign-out sweeps at once and the media stops about
   10 s later (or at the next cut slot); otherwise (an expired session)
   within 45 s more.
   A camera id is never given to a second camera (grants, recordings and
   events name only the id): removing a camera retires its id, drops it
   from every account's camera list (an account limited to that camera
   alone keeps it and sees none of the others, never all) and from its
   per-camera settings; its recordings and events stay under the retired id
   until retention (`default_retention_days`, else disk pressure). The
   dashboard, the wizard and `scripts/add-camera.sh` (fail closed, through
   `sentinela check-camera-id`) refuse a retired id. If a hand edit of
   `config.yaml` brings one back, it is a new camera: at the API's start the
   old camera's recordings, events and remaining grants move to an archive
   id (`<id>~<retired at>`, never valid in a configuration) and its
   settings go, so nothing of the old camera attaches to it. Recording of
   every camera goes on throughout.
4. Passwords use argon2id (64 MiB, 3 iterations, one lane). Policy is at
   least 10 characters mixing two kinds, or a 16-character passphrase;
   common passwords are rejected. Remembered browser sessions last 30 days
   from last use (sliding), otherwise 12 hours; Android app sessions slide
   over 1 year from last use and also end on sign-out, revocation, password change or disable. Cookies are HttpOnly, SameSite=Strict and Secure
   for HTTPS. Unsafe requests require CSRF and a valid Origin. Forwarded
   headers are trusted only from loopback. Limits include 5 requests/minute
   per IP, 10 failed outside logins/account/source/hour (300 from all sources
   together; an account counts by user id, so its username and e-mail share
   them) and 20 failed sign-ins or invite codes/IP/hour (IPv6 clients count
   per /64). The all-sources count is a trade-off: a stranger who knows a
   username can spend those 300 (10 from each of 30 addresses) and keep that
   account out of outside sign-in for the hour, again every hour; home and
   tailnet sign-in and existing outside sessions (the app) keep working. In public zones an IPv4 /24 or IPv6 /48 also shares 30
   attempts/minute and 100 failures/hour. Everyone behind one carrier-grade
   NAT or mobile network shares that budget, so a guesser there can keep the
   whole network out of outside sign-in for as long as they keep at it: any
   request spends the minute budget, and 100 failures (bad invite codes cost
   no hash) close it for the hour (home and tailnet sign-in are not
   affected). A tunnel or reverse proxy that does not send
   `X-Forwarded-For` makes every outside client look like 127.0.0.1: they all
   share one address and network budget, so one stranger's wrong passwords
   lock everyone out of the public door for the hour. One that passes on the
   client's own `X-Forwarded-For` lets each client choose the address the
   limits count, so it can mint fresh keys or spend someone else's budget:
   the proxy must set the header itself (INSTALL.md §8.3). Home
   (LAN/tailnet) and public zones keep separate tables; each tracks at most
   20 000 keys (about 3 MB; login names are hashed, so long names cost no
   more) and never forgets a live counter; with every table a stranger can
   fill full, about 23 MB. A key that a full table cannot track is not
   counted and never locked, so filling a table locks nobody out; the network
   and account tables behind it still count (accounts by user id, which
   strangers cannot fill). Residual: while the tables of names that match no
   account are full (20 000 failed sign-ins with distinct names within the
   hour, each a hash), a made-up name no longer locks, so 11 wrong passwords
   from one address (or 301 from many) can tell it from a real account. Names
   over 254 bytes are refused without a hash and do not count as failures.
   At most 2 password hashes run at once; public-zone work uses at most one of
   them with its own short queue, so the internet cannot starve home sign-ins,
   and a busy server answers 503 instead of piling up work. Audit entries are
   length-capped and pruned after 180 days. Setup works only before the first
   user exists.
5. Camera writes are allow-listed and audited. Persistent writes use
   read/modify/read-back/restore. User access changes and file API writes
   are audited. Do not put secret token values in audit targets or details.
6. Containers run without root (go2rtc runs its image's default user; a
   pinned non-root user is still open work), with read-only roots, dropped
   capabilities, no Docker socket, host PID or privileged mode. The API's NAS
   mount is separate from the recorder; detection cannot become a recording
   dependency. The recorder, which parses untrusted camera data, mounts only
   the recordings, the database folder and the volume marker, not all of
   `/data` (which holds the request folders root agents act on, the agent and
   detector tokens and the TLS keys). Accounts, sessions, two-step secrets,
   settings and the audit log are in the API's private database
   (`db-private/`), which the recorder does not mount; the index it shares
   with the API holds recordings, events and recorder status only. The API
   opens recordings through `os.Root`, so a link planted under `recordings/`
   cannot lead it to the rest of `/data`. The one exception to "no Docker socket"
   is the `apps` agent (ADR-0021): uid 1000 in the docker group, no camera
   secrets, loopback API with `/data/apps-agent/token`, and read-write access
   to the Cloud share (importing a local folder moves it into the service).
7. Images and model downloads are pinned; check dependency vulnerabilities
   through the existing tooling. No new service or decode path without an
   accepted ADR and the applicable measured resource budget.
8. SSH should be key-only and is limited to LAN/tailnet administration: the
   scripts open port 22 to those ranges but do not edit `sshd_config`, so
   key-only login and rate limiting (fail2ban) are the operator's setup.
   Sensitive go2rtc/internal APIs remain loopback-only. Samba binds to the LAN
   and uses its own account, with a minimum password of 8 characters and a
   30-minute lock after 5 wrong passwords (`setup-nas.sh`); web roles do not
   restrict that SMB account, and the camera and any other device on the LAN
   can reach it.
   Remote SMB needs explicit approval and stronger per-user credentials.
   The earlier ntfy relay and its tailnet port 8443 were removed on
   2026-10-01 (FCM replaced it). API Cloud access uses `os.Root`, personal/
   shared scope, sandboxed file responses and authenticated media/PDF
   previews. The recorder cannot see the NAS.
9. Tailscale remains the private path under ADR-0004, amended by the
   specific public exception in ADR-0019. There is no camera subnet router
   or exit node. A minimal family/admin ACL is:

   ```json
   {
     "groups": {"group:family": ["owner@example.com"]},
     "tagOwners": {"tag:nvr": ["autogroup:admin"]},
     "acls": [
       {"action": "accept", "src": ["group:family"], "dst": ["tag:nvr:443,8555"]},
       {"action": "accept", "src": ["autogroup:admin"], "dst": ["tag:nvr:22"]}
     ],
     "ssh": []
   }
   ```

   Add 445 only for explicitly approved remote SMB, and the services range
   (`tag:nvr:20000-20099`, ADR-0021) for devices that open self-hosted
   services. HTTPS tailnet names enter public
   certificate-transparency logs. Key expiry on phones, a stable server
   identity and Tailnet Lock are operator responsibilities.
   Tailscale's input chain can precede ufw; its ACL is the effective tailnet
   filter, so ufw interface allowances do not replace the ACL.
10. Deploy manually over authenticated SSH. CI checks code and holds no
    server deployment credential. An API-only change must not need a camera
    config write. A full image deployment can briefly restart recording;
    publishing an APK alone does not need an NVR restart.
11. `/srv/backup/etc` contains camera secrets and must remain root-only.
    Restic repositories are encrypted; keep the repository password in a
    protected recovery location. Local backups are not a completed recovery
    guarantee until a restore drill succeeds. Runtime keys under
    `/data/acme`, `/data/push`, `/data/fcm` and the development PC's Android
    signing directory need separate protected recovery copies; the current
    backup script copies the DB and `/etc/sentinela` to `/srv/backup`; when
    `restic.env` exists it also sends `/srv/nas` (every user's Cloud files and
    recycle bin) and `/srv/backup` (including the copied secrets) to the
    encrypted restic repository. Backup and restore-drill read the database
    with an unprivileged uid and `trusted_schema=OFF`, since the container
    that writes it is not trusted with root.
12. Browser push uses allow-listed HTTPS services and encrypted RFC 8291
    payloads; the picture is fetched from the house. Android FCM uses fixed
    Google OAuth/FCM endpoints and an opaque random id (ADR-0020). The private
    service-account key is `/data/fcm/service-account.json`, 0600, UID 1000.
    FCM tokens are bound to the user's login session and removed on logout/
    revocation; expired sessions do not receive messages. Event lookup is
    authenticated, user-bound and expires after 15 minutes. A user keeps at
    most 10 browser subscriptions and 10 app tokens (the least recently
    used go first); a subscription that never delivered and failed three
    times in a row is dropped. Each user's deliveries have their own 20 s
    (10 s per device), apart from the event snapshot, and users are served
    side by side, so one account's devices cannot delay another's alerts.
13. Detector endpoints require loopback and `/data/detector/token`.
    The sidecar consumes only the loopback substream restream, never camera
    credentials. Its accepted decode budget is ADR-0016; snapshot decoding
    is separately limited by ADR-0015. What it can store is bounded: per
    camera and kind 60 events at once, then one every 10 s; at most 50
    objects of 10 zones each, of which a summary is kept (the count and
    the 5 best objects' allow-listed fields) within 1 KiB;
    its picture at most once per camera every 10 s; and all event
    snapshots within 1 % of the data disk (ADR-0015).
14. Public access is the accepted P1 path (ADR-0019): a WAN port forwarded
    to TLS 7443, with the approved WebRTC 8555 allowance. Funnel
    (`remote.funnel_listen`) and the loopback proxy listener
    (`remote.proxy.listen`, for Cloudflare Tunnel, Caddy or nginx; ADR-0025)
    are also public zones when the owner turns them on from the dashboard;
    point a tunnel only at that listener, never at the dashboard port.
    A Cloudflare Tunnel's routes live in the Cloudflare account, not on the
    host: its one public hostname must have the service
    `http://<remote.proxy.listen>` (`http://127.0.0.1:7444`) and the tunnel
    no private-network routes. `sentinela-cloudflared.service` confines the
    connector to the host's loopback and the IPv4 internet (no LAN, cameras,
    router, tailnet, Docker networks or IPv6), and on loopback to that
    listener: the connector runs as its own system user, and an nftables
    table keyed on that user's uid (`scripts/lib/cloudflared.sh`, loaded
    before every start, kept by the agent's timer, following the port in
    the settings) refuses everything else it sends through the loopback
    interface (go2rtc's API and RTSP, any other local service), apart from a
    local name server. Who can edit the tunnel (account members, API
    tokens) still decides who reaches that listener; it is the owner's
    responsibility.
    Embedded tailnet enrollment/app keys were removed (`mobile/tailnet` is
    parked source that no build uses).
    Public zones are determined by listener, never by a client header:
    - TOTP is optional (ADR-0019 amendment, 2026-11-06). For an account
      with TOTP, a public session must be created with the code; its
      sessions without MFA are ignored there.
    - Before MFA authentication, only the shell, setup-status and permitted
      authentication/invite onboarding endpoints answer. Protected APIs,
      first-admin setup and public health checks remain closed.
    - Access-granting account administration and ordinary TOTP management
      require LAN/tailnet; invited first access can enroll TOTP through its
      one-time invite flow.
    - A session records the zone of the listener it was created on
      (`sessions.zone`, decided by the server) and its host (`sessions.host`).
      A public listener accepts only sessions created on that same zone and
      host, so a LAN-panel or tailnet cookie cannot be replayed from the
      internet whatever Host header is sent; sessions from before this check
      work only at home. No binding stops a cookie made at the public address
      that leaks to a service on another port of that address (browsers share
      cookies across ports): keep *Internet* services (ADR-0021) on another
      host name. Camera probes (`/api/cameras/test`, `discover`)
      and the service file/secret/folder reads are LAN/tailnet only; from the
      internet the Services page shows status and logs and starts, stops,
      restarts or redeploys. Version strings are not sent to the internet.
    - A tunnel or proxy on this host that forwards a public client address to
      the dashboard port (instead of `remote.proxy.listen`) is treated as the
      public zone it is. Anonymous requests on public listeners have a 15 s
      read and 2 min write deadline from their first byte to the last one
      net/http sends (only a signed-in request lifts them), headers up to
      16 KiB and no body except the sign-in forms (at most 8 KiB, read
      first). The port-forwarded listener keeps at most 1024 connections,
      64 per source (IPv4 address or IPv6 /64) and 128 per network (IPv4
      /24, IPv6 /48), of which 40 not yet signed in; when full, a newcomer
      replaces an anonymous connection of the network holding the most
      (idle ones first), never a signed-in one. Its HTTP/2 allows 32
      streams and 512 KiB receive windows: strangers hold at most about
      75 MB there (measured, `internal/api/publiclisten.go`).
    - TOTP is RFC 6238 with single-use steps, and outside login is audited.
      Five wrong codes in a row lock an account's code (by user id, any name
      or address) for 1 minute, doubling per further wrong code up to an hour
      (about 24 codes a day). Home and public zones count apart, and a good
      code at home clears the outside lock. Locks are always audited
      (`login.code_locked`, in the activity log) and alert the account and
      every admin, whatever their notice choices (at most daily). The count
      is in memory; a restart, or a day without wrong codes, clears it.
    - The household currently uses a self-signed IP certificate because
      normal ACME challenge ports are unavailable. Do not disable TLS
      checks: the Android app accepts only normal system trust or the exact
      configured certificate fingerprint.
15. Release APK settings are in `~/.sentinela-android/connection.json` or
    explicit environment overrides. A release needs a valid HTTPS origin
    and 64-character SHA-256 pin. Verify the fingerprint through trusted SSH,
    then compare the actual served certificate before publishing. The
    signing keystore and Firebase client config stay outside Git.
    In-app installation verifies the APK hash; Android enforces the signing
    identity. A changed self-signed certificate needs a new pinned APK.
    Release 1.11 omitted its pin; recover with a manual same-key 1.12 install,
    without removing the existing app.
16. Cloud viewers are blocked at the API. Operators use their own folder
    and the shared folder (*Shared*); admins can access the share. Folders
    are keyed by username, so deleting an account moves its folder to
    *Contas removidas* (admins only) and its recycle bin out of the name
    (still purged after 30 days); a new account never takes over a folder
    or bin already under its name unless the admin chooses to give it.
    Top-level folders are protected from API rename/move/delete. Explicit recycle-bin purge is
    permanent. Profile uploads accept bounded JPEG/PNG/WebP data and are
    resized on the client; they are not a new server image-processing pipeline.

17. Self-hosted services (ADR-0021) are admin-only. Service creation and
    changes, source inspection and the GitHub token require LAN/tailnet;
    request bodies (environment values) are never logged, and secret
    variables are never returned by the API. The GitHub token is a
    fine-grained read-only token stored 0600 under `/var/lib/sentinela-apps`.
    Services publish only on `apps.port_first..last`; `apps-firewall.sh`
    keeps them off private ranges and off the internet unless marked
    *Internet* by the owner (who also forwards the port). No service starts
    before that firewall has verified its rules for the current boot
    (marker `/run/sentinela-apps/firewall-ready`). A public service
    has no Sentinela authentication in front of it. Compose files are
    checked against an allow-list (ADR-0021): no privileged mode, host
    namespaces, devices, extra capabilities, Docker socket or provider
    services; binds only from the release and `data` folders (never the
    agent's files); only the project's own bridges and volumes (IPv4, no
    chosen subnets); one replica per service; build output only in the
    service's image namespace. Builds that run outside the services firewall
    (Compose `build:`, BuildKit) need the owner's consent per service.

18. Root host agents never trust files the containers write. They reach the
    data disk only through `scripts/lib/datafs.py` (no link followed,
    descriptors only, plain size-capped files); the updater takes the running
    version from the installed image, and the deploy files it installs belong
    to root. `apply-config.sh` re-checks every dashboard-applied config with
    `sentinela check-config -against <installed>` (non-editable keys
    unchanged, public ports in 1024..65535, credential names
    `<NAME>_USER`/`<NAME>_PASS` or as installed) and accepts credentials only
    for names the candidate's cameras use, as printable ASCII without spaces,
    quotes, backslashes, `$`, `{`, `}` or `#`, so Docker's `--env-file`,
    Compose's `env_file` and go2rtc all read the same value. Releases are
    made only from a plain `vX.Y.Z` tag (no leading zeros) whose commit is on
    `main`, is newer than every published release and contains the latest
    one; the APK is signed in a job that runs no project code, and the
    signing key additionally needs the `release` environment protections in
    `docs/RELEASING.md`, which only GitHub settings can enforce.

## Reporting and verification

Record camera evidence in CAMERA.md with its original date; use an issue
for actionable security findings. Before release, run the repository check
gate and the Android build/validation relevant to the change. Never run a
hardware test that moves a camera or writes its config without the owner's
explicit go-ahead.