# Installing Sentinela v2

A step-by-step guide from a blank PC to a recording NVR with live view,
playback, the Android app and remote access. It follows the household
deployment; replace the example addresses with your own.

| Example value | Meaning |
|---|---|
| `192.168.1.10` | Server (fixed LAN address) |
| `192.168.1.20` | Camera |
| `192.168.1.0/24` | Home LAN |
| `203.0.113.7` | Fixed public IP of the house (documentation range) |
| `you` | Your server login and Samba user (example) |

The rules behind every step are in [AGENTS.md](../AGENTS.md),
[SECURITY.md](SECURITY.md) and the ADRs. Do not skip the security steps:
the camera firmware is untrusted and the server holds its credentials.

---

## 1. What you need

**Server.** Any x86-64 PC that runs Ubuntu Server 22.04+ or Debian 12+.
Minimum: 2 CPU cores, 4 GB RAM (8 GB if you enable object detection or
self-hosted services). The server never decodes the main stream, so an
old CPU without HEVC hardware decode is enough.

**Disks.** Use separate mounts:

| Mount | Use | Required |
|---|---|---|
| `/data` | Recordings, database, keys | Yes. The installer refuses to record on the system disk |
| `/srv/nas` | Nuvem files (LAN share and web file browser) | Optional |
| `/srv/backup` | Nightly database/config backups | Optional, recommended |

Size `/data` for your cameras: one stream at ~2 Mbit/s writes about
20 GB per day. By default recordings fill the disk and the oldest are
overwritten (`max_disk_percent`); `retention_days` caps the age instead.

**Network.** Wired Gigabit for the server and the camera. A DHCP
reservation (or static address) for both.

**Camera.** An XMEye/Xiongmai IP camera with RTSP on 554, DVRIP on 34567 and
ONVIF (8899 on the reference unit). H.265 main stream is expected.

**Development PC** (for updates and the Android app): Git, Docker Desktop
(or Docker Engine), Go 1.26, Node.js 22 with npm, an SSH key for the
server and, for the app, JDK 21 and the Android SDK.

---

## 2. Prepare the camera

1. In the camera's own web page or the XMEye app, set a new admin password.
   It must not contain `& ? / # %` or spaces: the camera reads the RTSP URL
   literally and those characters break it.
2. Turn off Cloud/P2P and UPnP.
3. Give the camera a fixed address (DHCP reservation on the router).
4. Optional but recommended: run the read-only audit from any LAN PC and
   keep the output private (it is git-ignored):

   ```bash
   ./scripts/camera-audit.sh 192.168.1.20 admin
   ```

   The password is asked silently. Compare the result with
   [CAMERA.md](CAMERA.md) §3.
5. After the server is installed (step 4), point the camera's NTP at the
   server's IP and block all WAN traffic from the camera on the router.
   The full checklist is [CAMERA.md](CAMERA.md) §4.

---

## 3. Prepare the server

1. Install **Ubuntu Server 22.04** (minimal, OpenSSH enabled, no desktop).
2. Reserve its address on the router (`192.168.1.10` here).
3. From the development PC, install an SSH key:

   ```bash
   ssh-keygen -t ed25519 -f ~/.ssh/id_ed25519_sentinela
   ssh-copy-id -i ~/.ssh/id_ed25519_sentinela.pub you@192.168.1.10
   ```

4. Format and mount the data disks. Find them with `lsblk -f`, then, for
   each new partition (this **erases** it):

   ```bash
   sudo mkfs.ext4 -L sentinela-data /dev/sdX1   # your recordings disk
   sudo mkdir -p /data
   echo 'LABEL=sentinela-data /data ext4 defaults,noatime 0 2' | sudo tee -a /etc/fstab
   sudo mount -a && findmnt /data
   ```

   Repeat for `/srv/nas` and `/srv/backup` if you have those disks.
   `noatime` avoids a metadata write for every read of a recording.

5. The scripts take the LAN range and interface from the server's default
   route. If that is not your home LAN (several interfaces), export
   `SENTINELA_LAN=192.168.1.0/24` and `SENTINELA_LAN_IFACE=eno1` before
   installing. The Samba account is the user who runs `sudo`.

---

## 4. Install Sentinela

On the server:

```bash
sudo apt-get update && sudo apt-get install -y git
sudo git clone https://github.com/LUKER2DW/Sentinela.git /opt/sentinela
cd /opt/sentinela
sudo ./scripts/install-server.sh
```

The installer is idempotent. It:

1. installs Docker Engine and the Compose plugin;
2. creates `/etc/sentinela/config.yaml` from
   [deploy/config.example.yaml](../deploy/config.example.yaml) and asks for
   the camera user and password (typed silently, stored only in
   `/etc/sentinela/secrets.env`, mode 0600);
3. prepares `/data` (owned by UID 1000, the containers' user);
4. sets kernel write-back limits, serves NTP to the LAN (chrony), installs
   the SMART timer, applies the firewall, and sets up Samba and backups
   when `/srv/nas` and `/srv/backup` exist;
5. builds and starts the containers: `sentinela` (API/UI), `recorder`,
   `go2rtc` and `detector` (plus `apps` once section 13 is enabled);
6. waits for the health check.

Then review `/etc/sentinela/config.yaml`:

- `cameras[0].host`: the camera address; `name`: shown in the UI.
- `invert_pan_tilt` / `invert_pan` / `invert_tilt`: set if PTZ arrows move
  the wrong way (a camera mounted upside down).
- `record_audio`: `true` stores AAC audio (ADR-0006); `false` is a pure copy.
- `recording.default_retention_days`, `alert_retention_days`,
  `max_disk_percent`: retention.

After editing, apply it:

```bash
cd /opt/sentinela/deploy && sudo docker compose up -d
```

The first build on an old CPU takes several minutes. Later updates are
built on the development PC instead (section 11).

---

## 5. First login

1. Open `http://192.168.1.10:8080` on the LAN. The first visit creates the
   **admin** account.
2. In **My account** (*Minha conta*), enroll **two-step login** (TOTP) with
   an authenticator app. It is mandatory for remote logins, and it can only
   be enrolled from home.
3. Check **Live** (*Ao vivo*): the picture should open in about a second.
4. After 5–10 minutes, check **Recordings** (*Gravações*): the day ribbon
   fills and playback works.
5. Create the other people's accounts in **System → Users** (*Sistema →
   Usuários*, desktop): *viewer* (cameras, recordings and events),
   *operator* (also Nuvem and camera control) or *admin*.

If a password is lost:

```bash
cd /opt/sentinela/deploy && sudo docker compose exec -T sentinela sentinela user passwd <user>
```

---

## 6. LAN file share (Nuvem)

When `/srv/nas` is mounted, the installer configures Samba with two shares:
`\\192.168.1.10\Nuvem` (read/write) and `\\192.168.1.10\Gravacoes`
(recordings, read-only). Set the share password once:

```bash
sudo smbpasswd -a "$USER"
```

Samba accounts are separate from web accounts. The same files are
available in the web/app **Nuvem** page for operators and admins.

---

## 7. Reliability and hardening

```bash
sudo /opt/sentinela/scripts/optimize-host.sh
```

It removes services an NVR does not need and makes the machine recover
from crashes and hangs. Also:

- BIOS: set **After power failure = Power On**.
- Confirm the firewall: `sudo ufw status verbose` (LAN and tailnet only,
  plus the public ports of section 8.2 if you enable them).
- Finish the camera hardening of section 2, step 5.

---

## 8. Remote access

### 8.1 Tailscale (private administration)

```bash
sudo /opt/sentinela/scripts/setup-tailscale.sh
```

Open the login link it prints. In the Tailscale admin console enable HTTPS
certificates, tag the server `tag:nvr`, and set the ACL and key expiry as
described in [SECURITY.md](SECURITY.md) rule 9. The UI is then at
`https://sentinela.<tailnet>.ts.net` from your own devices. Nothing opens on
the router.

### 8.2 Public HTTPS (household access, ADR-0019)

This is the only accepted internet exposure. It requires two-step login,
and the "closed door" hides everything before authentication.

1. Make sure every remote user has enrolled two-step login at home.
2. In `/etc/sentinela/config.yaml`:

   ```yaml
   remote:
     public:
       domain: 203.0.113.7           # fixed public IP, or a domain with an A record
       listen: ":7443"
       url: https://203.0.113.7:18443 # the address used from outside
       # email: you@example.com      # Let's Encrypt account
       # http01_listen: ":7080"      # if inbound 443 is blocked but 80 works
       # self_signed: true           # if 80 is blocked too: the app pins it
   ```

3. Open the server's ports and restart:

   ```bash
   sudo /opt/sentinela/scripts/setup-public.sh enable
   cd /opt/sentinela/deploy && sudo docker compose up -d
   ```

4. On the router, forward `WAN 18443/tcp → 192.168.1.10:7443` (the site)
   and `WAN 8555/tcp+udp → 192.168.1.10:8555` (WebRTC video). Use WAN 443
   instead of 18443 if your provider allows it.
5. Check it and note the certificate fingerprint (needed by the Android app
   when `self_signed` is used):

   ```bash
   sudo /opt/sentinela/scripts/setup-public.sh status
   ```

Never forward the camera's ports, SMB, RTSP or go2rtc's API.
`setup-public.sh disable` closes the ports again (remove the router
forwards too).

---

## 9. Notifications

- **Browsers** use Web Push; keys are created automatically. Enable them in
  **My account → Notifications** on each device.
- **Android app** uses Firebase Cloud Messaging as an opaque wake-up
  (ADR-0020). Create a Firebase project, add the Android app
  `casa.sentinela.app`, then:
  - download `google-services.json` to `~/.sentinela-android/` on the
    development PC (it goes into the APK, section 10);
  - create a service-account key and install it on the server (never in
    Git or the APK):

    ```bash
    sudo install -D -m 0600 -o 1000 -g 1000 service-account.json /data/fcm/service-account.json
    cd /opt/sentinela/deploy && sudo docker compose restart sentinela
    ```

  Without Firebase the app falls back to a direct foreground connection.

---

## 10. Android app

The app is a Capacitor wrapper that loads the server's own UI over pinned
HTTPS (ADR-0011). On the development PC:

1. Create `~/.sentinela-android/connection.json` with the public origin and
   the certificate fingerprint from section 8.2:

   ```json
   {
     "domain_url": "https://203.0.113.7:18443",
     "public_cert_sha256": "<64 hexadecimal characters>"
   }
   ```

2. Build and sign (the signing key is created on the first run in
   `~/.sentinela-android/`; **back that folder up**: without it no update
   can be installed over the existing app):

   ```bash
   ./scripts/build-apk.sh
   ```

3. Publish it for in-app updates and install `mobile/build/sentinela.apk`
   on the phones once (allow installing from this source when Android asks):

   ```bash
   ./scripts/publish-apk.sh "What changed"
   ```

Later versions appear in **My account → App** (*Aplicativo*). Only
changes in `mobile/` need a new APK; UI changes reach the app with a server
deploy. Never work around a TLS error by disabling certificate checks.

---

## 11. Updating

From the development PC, with the change committed. Tell the scripts
where the server is once, in a file outside the repository:

```bash
mkdir -p ~/.sentinela
cat > ~/.sentinela/deploy.env <<'EOF'
SENTINELA_HOST=you@192.168.1.10
SENTINELA_SSH_KEY=~/.ssh/id_ed25519_sentinela
EOF
./scripts/deploy.sh
```

It builds the images locally, sends them over SSH, ships `deploy/` and
`scripts/` to `/opt/sentinela`, restarts the stack and waits for the health
check. Recording pauses for a few seconds while the recorder container is
recreated. `SENTINELA_HOST=user@host` in the environment overrides the
file. Docker
Desktop must be running on the PC.

Without a development PC you can update on the server itself (slower):

```bash
cd /opt/sentinela && sudo git pull && sudo SENTINELA_BUILD=1 ./scripts/install-server.sh
```

---

## 12. More cameras

Any number of cameras, of any brand that serves RTSP. `vendor` picks the
stream layout:

| `vendor` | Streams | Remote control |
|---|---|---|
| `xmeye` (default) | XMEye/Xiongmai URL with credentials in the path | PTZ, presets, tours, light, day/night, two-way audio (DVRIP) |
| `hikvision` | `/Streaming/Channels/{channel}01` and `…02` | View and record |
| `dahua` | `/cam/realmonitor?channel={channel}&subtype=0` and `1` | View and record |
| `generic` | your `rtsp_main` (and optional `rtsp_sub`) path | View and record |

For an XMEye camera, test it first in **System → Add camera** (*Adicionar
câmera*, admin, desktop). Then, on the server:

```bash
sudo /opt/sentinela/scripts/add-camera.sh cam2 192.168.1.21 "Back yard"
sudo VENDOR=hikvision /opt/sentinela/scripts/add-camera.sh garage 192.168.1.30 "Garage"
sudo VENDOR=generic RTSP_MAIN=/stream1 RTSP_SUB=/stream2 \
  /opt/sentinela/scripts/add-camera.sh yard 192.168.1.31 "Yard"
```

The password is asked silently and stored only in `secrets.env`. The new
config is validated before it replaces the old one, go2rtc's stream list is
regenerated from `config.yaml` (`scripts/gen-go2rtc-cameras.sh`) and
recording pauses for a few seconds. You can also edit
`/etc/sentinela/config.yaml` by hand (see `deploy/config.example.yaml`) and
run `./scripts/deploy.sh`, which regenerates the stream list.

---

## 13. Self-hosted services (optional)

Sentinela can also run your own projects — APIs, bots, sites, Compose
stacks — from GitHub or from a folder on the server (ADR-0021). Turn it on
once, on the server:

```bash
sudo /opt/sentinela/scripts/setup-apps.sh enable
```

It adds `apps: {enabled: true}` to the config, starts the `apps` agent and
installs the services firewall. Admins then see **Serviços** in the
sidebar (on the phone, under **Mais**). To deploy a project from your PC
without GitHub, copy its folder (with `.git`) into the Nuvem over the LAN
share and pick it under **Novo serviço → Pasta no servidor**.

- Services use host ports 20000–20099. For Tailscale devices, allow
  `tag:nvr:20000-20099` in the tailnet ACL.
- **Internet** exposure needs `apps.public_host` (defaults to the public
  address of section 8.2) and a router forward of that service's port.
  The service has no Sentinela login in front of it.
- Private GitHub repositories: **Acesso ao GitHub** takes a fine-grained
  token with read-only *Contents* on the repositories you use.
- `setup-apps.sh status` shows the agent, the services and the firewall;
  `disable` stops everything (files stay in `/var/lib/sentinela-apps`).

---

## 14. Backups

`setup-backup.sh` (run by the installer when `/srv/backup` exists) installs
a nightly timer that copies the database and `/etc/sentinela` to
`/srv/backup`. For an off-machine copy, create `/etc/sentinela/restic.env`
(mode 0600) for an external disk:

```bash
RESTIC_REPOSITORY=/media/backup/sentinela-restic
RESTIC_PASSWORD=<keep it in a password manager>
RESTIC_MOUNT=/media/backup
```

Also keep a copy of `~/.sentinela-android/` and of `/data/fcm`, `/data/acme`
and the other runtime keys under `/data`. Do one restore drill: a backup
that was never restored is not a backup yet.

---

## 15. Checks and troubleshooting

| Check | Command |
|---|---|
| Health | `curl -s http://127.0.0.1:8080/healthz` (on the server) |
| Containers | `cd /opt/sentinela/deploy && sudo docker compose ps` |
| Logs | `cd /opt/sentinela/deploy && sudo docker compose logs -f --tail=50` |
| Public listener | `sudo /opt/sentinela/scripts/setup-public.sh status` |
| Self-hosted services | `sudo /opt/sentinela/scripts/setup-apps.sh status` |
| Memory hogs | `ps aux --sort=-rss \| head` |

| Symptom | Likely cause |
|---|---|
| Live view never opens | Camera password with `& ? / # %` or spaces; wrong `host`; camera unreachable |
| No recordings | `/data` not mounted or not owned by UID 1000; see `recorder` logs |
| Recording gaps of seconds | A deploy or config change recreated the recorder (expected) |
| Live "delay" of many seconds | Usually clock skew: the camera has no NTP, or the viewing PC's clock is off. Use the live delay meter, not the camera's on-screen clock |
| `deploy.sh` hangs or fails at build | Docker Desktop is not running on the PC |
| App cannot connect after an update | The APK was built without the certificate pin; rebuild with `connection.json` and install it over the old one |
| Everything slow, SSH hangs | Another process (e.g. an IDE remote server) is using the RAM |

---

## UI names

The interface is in Portuguese. The names used in this guide:

| UI | English |
|---|---|
| Ao vivo | Live |
| Gravações | Recordings |
| Eventos | Events |
| Nuvem | Cloud (file storage) |
| Mais | More |
| Minha conta | My account |
| Sistema | System |
| Usuários | Users |
| Notificações | Notifications |
| Aplicativo | App (Android updates) |
| Compartilhado | Shared (common Nuvem folder) |
| Adicionar câmera | Add camera |
| Serviços | Self-hosted services |
