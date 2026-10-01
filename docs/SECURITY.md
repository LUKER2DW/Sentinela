# Security

Current policy: 2026-09-30. Historical public-access and notification choices
are superseded where noted by ADR-0019 and ADR-0020.

## Threat model

| Threat | Control and current limits |
|---|---|
| Camera attacked from the internet | Never forward camera ports. Cloud/P2P, UPnP and WAN egress isolation remain operator checks; do not assume they are complete |
| Public server login attacked | Only the approved TLS/WebRTC path; mandatory TOTP, closed door and rate limits |
| Stolen phone/session | Native app lock, revocable sessions, expiration and session-bound FCM tokens |
| LAN guest or IoT attack | Firewall and camera isolation; a separate camera VLAN where available |
| Secrets in Git/logs/bundles | Runtime secret files, redaction, ignored local release files and review before commit |
| CSRF/XSS | Same-origin cookies, CSRF header and Origin check, CSP and file-response restrictions |
| Nuvem loss/ransomware | Recycle bin, audit, local DB/config backup; external restic/restore acceptance still pending |
| Build pipeline competes with recording | Local manual builds; no server GitHub credentials, runner or build toolchain |
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
   authenticated session when consumed.
4. Passwords use argon2id (64 MiB, 3 iterations, one lane). Policy is at
   least 10 characters mixing two kinds, or a 16-character passphrase;
   common passwords are rejected. Remembered sessions last 30 days,
   otherwise 12 hours. Cookies are HttpOnly, SameSite=Strict and Secure
   for HTTPS. Unsafe requests require CSRF and a valid Origin. Forwarded
   headers are trusted only from loopback. Limits include 5 requests/minute
   per IP, 10 outside-login attempts/account/hour and 20 failed sign-ins or
   invite codes/IP/hour. Setup works only before the first user exists.
5. Camera writes are allow-listed and audited. Persistent writes use
   read/modify/read-back/restore. User access changes and file API writes
   are audited. Do not put secret token values in audit targets or details.
6. Containers run without root, with read-only roots, dropped capabilities,
   no Docker socket, host PID or privileged mode. The API's NAS mount is
   separate from the recorder; detection cannot become a recording dependency.
7. Images and model downloads are pinned; check dependency vulnerabilities
   through the existing tooling. No new service or decode path without an
   accepted ADR and the applicable measured resource budget.
8. SSH is key-only and limited to LAN/tailnet administration. Sensitive
   go2rtc/internal APIs remain loopback-only. Samba binds to the LAN and
   uses its own account; web roles do not restrict that SMB account.
   Remote SMB needs explicit approval and stronger per-user credentials.
   The existing ntfy firewall allowance is legacy; current Android push
   does not require port 8443. API Nuvem access uses `os.Root`, personal/
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

   Add 445 only for explicitly approved remote SMB, and 8443 only if the
   legacy relay is intentionally used. HTTPS tailnet names enter public
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
    backup script copies only the DB and `/etc/sentinela`.
12. Browser push uses allow-listed HTTPS services and encrypted RFC 8291
    payloads; the picture is fetched from the house. Android FCM uses fixed
    Google OAuth/FCM endpoints and an opaque random id (ADR-0020). The private
    service-account key is `/data/fcm/service-account.json`, 0600, UID 1000.
    FCM tokens are bound to the user's login session and removed on logout/
    revocation; expired sessions do not receive messages. Event lookup is
    authenticated, user-bound and expires after 15 minutes.
13. Detector endpoints require loopback and `/data/detector/token`.
    The sidecar consumes only the loopback substream restream, never camera
    credentials. Its accepted decode budget is ADR-0016; snapshot decoding
    is separately limited by ADR-0015.
14. Public access is the accepted P1 path (ADR-0019). The current household
    forwards WAN 18443 to TLS 7443, with the approved WebRTC 8555 allowance.
    Funnel is disabled; embedded tailnet enrollment/app keys were removed.
    Public zones are determined by listener, never by a client header:
    - A public session must be created with password and TOTP. Home sessions
      without MFA are ignored there.
    - Before MFA authentication, only the shell, setup-status and permitted
      authentication/invite onboarding endpoints answer. Protected APIs,
      first-admin setup and public health checks remain closed.
    - Access-granting account administration and ordinary TOTP management
      require LAN/tailnet; invited first access can enroll TOTP through its
      one-time invite flow.
    - TOTP is RFC 6238 with single-use steps, and outside login is audited.
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
16. Nuvem viewers are blocked at the API. Operators use their own folder
    and the shared folder (*Compartilhado*); admins can access the share.
    Top-level folders are protected from API rename/move/delete. Explicit recycle-bin purge is
    permanent. Profile uploads accept bounded JPEG/PNG/WebP data and are
    resized on the client; they are not a new server image-processing pipeline.

## Reporting and verification

Record camera evidence in CAMERA.md with its original date; use an issue
for actionable security findings. Before release, run the repository check
gate and the Android build/validation relevant to the change. Never run a
hardware test that moves a camera or writes its config without the owner's
explicit go-ahead.