#!/usr/bin/env bash
# Nightly backup (ADR-0014). Runs as root from sentinela-backup.timer.
#   1. SQLite: VACUUM INTO /srv/backup/db/sentinela-YYYYMMDD.db (keep 14).
#      Consistent under WAL while the app keeps running.
#   2. /etc/sentinela (config + secrets) -> /srv/backup/etc (root-only).
#   3. restic to the external disk, only if /etc/sentinela/restic.env exists:
#        RESTIC_REPOSITORY=/media/backup/sentinela-restic
#        RESTIC_PASSWORD=...            (also in the owner's password manager)
#        RESTIC_MOUNT=/media/backup     (required: skipped unless mounted, so a
#                                        missing disk never fills /)
# Writes a status file the System page shows (/var/lib/sentinela/backup.json).
set -euo pipefail

DB="${SENTINELA_DB:-/data/db/sentinela.db}"
DEST="${SENTINELA_BACKUP_DIR:-/srv/backup}"
STATUS="${SENTINELA_BACKUP_STATUS:-/var/lib/sentinela/backup.json}"
KEEP=14
RESTIC_ENV=/etc/sentinela/restic.env

log() { echo "backup: $*"; }
now() { date -u +%Y-%m-%dT%H:%M:%SZ; }

mountpoint -q "$DEST" || { log "$DEST is not a mounted disk; refusing to write to /"; exit 1; }
install -d -m 0755 "$DEST"
install -d -m 0700 "$DEST/db" "$DEST/etc"

# 1. Database. sqlite3 runs as the DB owner so it can never leave root-owned
# -wal/-shm files the container (UID 1000) could not open afterwards.
db_at="" db_file="" db_bytes=0 db_ok=false
if [[ -f "$DB" ]]; then
  uid="$(stat -c %u "$DB")" gid="$(stat -c %g "$DB")"
  tmp="$(mktemp -d)"
  chown "$uid:$gid" "$tmp"
  name="sentinela-$(date -u +%Y%m%d).db"
  if setpriv --reuid="$uid" --regid="$gid" --clear-groups \
      sqlite3 "$DB" ".timeout 10000" "VACUUM INTO '$tmp/$name'" &&
     [[ "$(sqlite3 "$tmp/$name" 'PRAGMA integrity_check')" == ok ]]; then
    chown root:root "$tmp/$name" && chmod 0600 "$tmp/$name"
    mv -f "$tmp/$name" "$DEST/db/$name"
    db_at="$(now)" db_file="$name" db_bytes="$(stat -c %s "$DEST/db/$name")" db_ok=true
    log "database -> $DEST/db/$name ($db_bytes bytes)"
    # Keep the newest $KEEP.
    ls -1t "$DEST"/db/sentinela-*.db | tail -n +$((KEEP + 1)) | xargs -r rm -f
  else
    log "database backup FAILED"
  fi
  rm -rf "$tmp"
else
  log "no database at $DB"
fi

# 2. Config and secrets.
cp -a /etc/sentinela/. "$DEST/etc/"
chmod -R go-rwx "$DEST/etc"

# 3. restic (off-box copy). Skipped until the owner provides the disk.
restic_state=off restic_at=""
if [[ -f "$RESTIC_ENV" ]]; then
  set -a; . "$RESTIC_ENV"; set +a
  if [[ -z "${RESTIC_MOUNT:-}" ]] || ! mountpoint -q "$RESTIC_MOUNT"; then
    restic_state=disk_missing
    log "restic: ${RESTIC_MOUNT:-RESTIC_MOUNT unset} is not mounted; skipped"
  elif restic snapshots --quiet >/dev/null 2>&1 || restic init --quiet; then
    if restic backup --quiet --tag nightly /srv/nas "$DEST" &&
       restic forget --quiet --prune --keep-daily 7 --keep-weekly 4 --keep-monthly 6; then
      restic_state=ok restic_at="$(now)"
      log "restic ok"
    else
      restic_state=failed
      log "restic FAILED"
    fi
  else
    restic_state=failed
    log "restic: repository unreachable"
  fi
fi

# Status for the UI: keep the previous success times when a step fails.
python3 - "$STATUS" "$db_ok" "$db_at" "$db_file" "$db_bytes" "$restic_state" "$restic_at" <<'PY'
import json, os, sys, tempfile, time
path, db_ok, db_at, db_file, db_bytes, rstate, rat = sys.argv[1:]
try:
    old = json.load(open(path))
except Exception:
    old = {}
st = {
    "updated": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
    "db_ok": db_ok == "true",
    "db_at": db_at or old.get("db_at", ""),
    "db_file": db_file or old.get("db_file", ""),
    "db_bytes": int(db_bytes) if db_at else old.get("db_bytes", 0),
    "restic": rstate,
    "restic_at": rat or old.get("restic_at", ""),
}
os.makedirs(os.path.dirname(path), exist_ok=True)
fd, tmp = tempfile.mkstemp(dir=os.path.dirname(path))
with os.fdopen(fd, "w") as f:
    json.dump(st, f)
os.chmod(tmp, 0o644)
os.replace(tmp, path)
PY

[[ "$db_ok" == true && "$restic_state" != failed ]]
