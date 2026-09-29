# ADR-0018: "Versões anteriores" on the Nuvem with Btrfs + snapper — plan only

Status: Approved · 2026-09-29 (owner) — waits on its preconditions (restic USB disk + restore drill); the migration window is scheduled with the owner

## Context
The Nuvem (`/srv/nas`, disk `sdb`, ext4) has a 30-day recycle bin for
deletions, but no protection against overwrites or ransomware on a
Windows PC that maps the share. Btrfs snapshots are cheap, read-only and
show up in Windows Explorer as "Versões anteriores" through Samba's
`vfs_shadow_copy2`. Converting means reformatting `sdb`.

## Plan
1. Preconditions: restic to the USB disk working **and** a restore drill
   passed (ADR-0014), plus a full `rsync -aHAX` copy of `/srv/nas` to the
   USB disk verified by checksum. Nothing starts without both.
2. Owner-scheduled window; Samba and the Nuvem API stopped (recording is on
   a different disk and keeps running).
3. `mkfs.btrfs` on `sdb`, subvolume `@nuvem` mounted at `/srv/nas`
   (`noatime,compress=zstd:1`), copy back, compare checksums, start Samba.
4. snapper: hourly × 24, daily × 14, weekly × 8; snapshots are read-only.
5. Samba: `vfs objects = shadow_copy2 recycle`, `shadow:snapdir = .snapshots`,
   `shadow:format = …` matching snapper; the Nuvem page gets a read-only
   "Versões" list per file through the API (`os.Root` on the snapshot dir).
6. Rollback: the verified rsync copy on the USB disk.

## Consequences
+ Explorer's "Versões anteriores" and a per-file history on the phone;
  a ransomware encryption of the share becomes a one-click restore.
− Btrfs on one HDD: snapshots are not backups (restic still is); needs
  a free-space alarm (snapshots keep deleted blocks).
− A risky one-time migration; about 1 h per 100 GB of copy on this disk.
