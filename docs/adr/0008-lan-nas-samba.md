# ADR-0008: The server is also a LAN file server (Samba, host package)

Status: Accepted · 2026-09-28 (owner request; supersedes the "NAS is scope
creep" non-goal in PLAN §1.2)

Current amendments: ADR-0014 moves the system disk's free tail from Cloud Extra to `/srv/backup`;
ADR-0012 adds the web/mobile file API. Samba still uses its own LAN account
(the installing user); web user/role restrictions do not
automatically apply to that SMB login. The original decision below is the
historical setup, with those later changes taking precedence.

## Context
The reference server has three disks of similar size: one for camera
recordings, the rest as "cloud" storage reachable from every device on
the home network at LAN speed. v1 built a web "files" service for this, which
added a lot of code and failure modes.

## Original decision (later amendments above)
- Disk layout: recordings disk → `/data` (ext4 `largefile4`); NAS disk →
  `/srv/nas` and the free tail of the system disk →
  `/srv/nas/Extra` (LAN storage). Separate filesystems: recordings can never
  fill the NAS and vice versa. Mount points are `chattr +i` so nothing writes
  to the root disk if a data disk is missing.
- Plain **Samba from Ubuntu packages on the host** (not a container) plus
  **wsdd** (Windows "Network") and **Avahi** (`sentinela.local` for macOS,
  iOS, Android). One name everywhere: hostname `sentinela`, NetBIOS
  `SENTINELA`. No web file manager.
- Shares: `Nuvem` (read-write, `/srv/nas`) and `Gravacoes` (read-only,
  `/data/recordings`). Only the installing user's account, LAN interface only,
  firewall limited to 192.168.1.0/24.
- For speed on an old CPU, SMB signing/encryption are negotiated, not forced
  (LAN only; Tailscale adds its own encryption for remote use later).
- Config lives in `deploy/nas/smb.conf`; `scripts/setup-nas.sh` applies it.
- Share passwords may be 4 characters (owner's choice, e.g. a numeric PIN;
  Samba's account policy `min password length` set to 4). Acceptable only
  because the share is LAN-only and firewalled; revisit before any remote
  access other than Tailscale.

## Consequences
+ Gigabit-limited transfers (~110 MB/s) with zero custom code.
+ Recording remains independent: Samba is not in the recording path.
− One more host service to patch (covered by unattended-upgrades).
− The recordings disk wears fastest; it holds disposable rolling
  recordings, not user files. Watch its SMART counters (System page).
