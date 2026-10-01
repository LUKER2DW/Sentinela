# ADR-0014: Backups — SQLite `VACUUM INTO` + restic to an external disk

Status: Accepted · 2026-09-29

## Context
Nothing was backed up: the SQLite index and users live on the worn
recordings disk (`sdc`), and the Nuvem (`sdb`) has one copy. Recordings are
rolling and disposable; user files, config and the DB are not.

## Decision
- **Local target:** `sda3`, the free tail of the system disk, leaves the
  Nuvem and is mounted at `/srv/backup` (owner decision D7; it was empty).
  It is a different disk from both the DB and the Nuvem.
- **DB + config:** a host timer (`sentinela-backup.timer`, nightly) runs
  `sqlite3 <db> "VACUUM INTO …"` (consistent under WAL, the app keeps
  running) into `/srv/backup/db/sentinela-YYYYMMDD.db`, keeps the last 14,
  and copies `/etc/sentinela` into `/srv/backup/etc/` (root-only, 0700).
- **restic** nightly to an external USB disk the owner provides:
  `/srv/nas` and `/srv/backup`. Repository and password file in
  `/etc/sentinela/restic.env` (0600).
  `--keep-daily 7 --keep-weekly 4 --keep-monthly 6`. The script is shipped;
  the timer runs it only when `restic.env` exists. Monthly restore drill
  (ARCHITECTURE §7).
- Samba recycle bin (`.lixeira/%U`, purged after 30 days) catches
  accidental deletes between backups.
- Off-site copy (B2 or a friend's machine over Tailscale): later, owner's call.
- Recovery keys introduced later (`/data/acme`, `/data/push`, `/data/fcm`)
  are not copied by the current DB/config backup script. Keep protected
  copies separately, including the PC's Android signing directory. A changed
  TLS certificate requires a new APK pin; signing-key loss prevents an
  in-place Android update.

External restic media and a completed restore drill remain operator tasks.
The available local backup does not imply those acceptance checks passed.

## Consequences
+ Losing `sdc` loses at most a day of DB changes; losing `sdb` loses nothing
  that reached the USB disk the night before.
− restic is a host package and one more timer.
