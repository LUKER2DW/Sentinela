# ADR-0008: The server is also a LAN file server (Samba, host package)

Status: Accepted · 2026-09-28 (owner request; supersedes the "NAS is scope
creep" non-goal in PLAN §1.2)

## Context
The server has Three disks. The owner wants one disk for camera
recordings and the rest as "cloud" storage reachable from every device on
the home network at LAN speed. v1 built a web "files" service for this, which
added a lot of code and failure modes.

## Decision
- Disk layout: `sdc` → `/data` (recordings, ext4 `largefile4`); `sdb` →
  `/srv/nas` and the free tail of the system disk (`sda3`) →
  `/srv/nas/Extra` (LAN storage). Separate filesystems: recordings can never
  fill the NAS and vice versa. Mount points are `chattr +i` so nothing writes
  to the root disk if a data disk is missing.
- Plain **Samba from Ubuntu packages on the host** (not a container) plus
  **wsdd** so Windows lists the server under Network. No web file manager.
- Shares: `Nuvem` (read-write, `/srv/nas`) and `Gravacoes` (read-only,
  `/data/recordings`). Only the `ana` account, LAN interface only,
  firewall limited to 192.168.1.0/24.
- For speed on the old CPU, SMB signing/encryption are negotiated, not forced
  (LAN only; Tailscale adds its own encryption for remote use later).
- Config lives in `deploy/nas/smb.conf`; `scripts/setup-nas.sh` applies it.

## Consequences
+ Gigabit-limited transfers (~110 MB/s) with zero custom code.
+ Recording remains independent: Samba is not in the recording path.
− One more host service to patch (covered by unattended-upgrades).
− The recordings disk wears fastest: it holds
  the disposable rolling recordings, not user files. Replace it if the
  counts grow.
