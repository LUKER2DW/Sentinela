#!/usr/bin/env bash
# Restore drill (ADR-0014): prove that the backups can be restored, without
# touching the running system. Run as root on the server, e.g. monthly:
#
#   sudo /opt/sentinela/scripts/restore-drill.sh
#
# 1. The newest local database copies (/srv/backup/db) open, pass
#    PRAGMA integrity_check and have their schema; the private one (or, in a
#    copy from before the split, the single file) has at least one user.
# 2. The config backup (/srv/backup/etc) validates with the current image,
#    secrets included (sentinela check-config, no network).
# 3. With /etc/sentinela/restic.env and its disk mounted: restic checks a
#    sample of the repository data and restores the newest database copies
#    from the latest snapshot, which must pass the same checks as (1).
#
# Everything is restored into a temporary folder that is removed at the
# end. Exit 0 = every step that could run passed. Idea from the legacy
# Sentinela restore drill.
set -euo pipefail

DEST="${SENTINELA_BACKUP_DIR:-/srv/backup}"
IMAGE="${SENTINELA_IMAGE:-sentinela:local}"
RESTIC_ENV=/etc/sentinela/restic.env

[[ $EUID -eq 0 ]] || { echo "run with sudo" >&2; exit 1; }
command -v sqlite3 >/dev/null || { echo "sqlite3 is missing (apt install sqlite3)" >&2; exit 1; }
WORK="$(mktemp -d)"
# STAGE is where a database copy is opened, by an unprivileged user (sq below).
STAGE="$(mktemp -d)"
trap 'rm -rf "$WORK" "$STAGE"' EXIT
chmod 0700 "$WORK"
chown 65534:65534 "$STAGE"
chmod 0700 "$STAGE"
fail=0
pass() { printf 'PASS  %s\n' "$*"; }
bad() { printf 'FAIL  %s\n' "$*"; fail=1; }
skip() { printf 'SKIP  %s\n' "$*"; }

# sq FILE SQL: read-only query on a database copy. The copy comes from a
# disk the containers (uid 1000) can write, so sqlite3 does not run as root:
# it runs as an unprivileged user (on a copy staged by check_db) with
# trusted_schema off, so nothing in the file's schema runs with root's rights.
sq() {
  setpriv --reuid=65534 --regid=65534 --clear-groups \
    sqlite3 -cmd 'PRAGMA trusted_schema=OFF' -readonly "$1" "$2"
}

# check_db FILE LABEL [users]: integrity and schema of a database copy and,
# with "users", at least one account.
check_db() {
  local label="$2" integrity users=0 migrations db="$STAGE/check.db"
  rm -f "$STAGE"/check.db*
  cp "$1" "$db" && chown 65534:65534 "$db" && chmod 0600 "$db" || { bad "$label: could not stage the copy"; return; }
  integrity="$(sq "$db" 'PRAGMA integrity_check' 2>&1 || true)"
  if [[ "$integrity" != ok ]]; then
    bad "$label: integrity_check: $integrity"
    return
  fi
  migrations="$(sq "$db" 'SELECT count(*) FROM schema_migrations' 2>/dev/null || echo 0)"
  [[ "${3:-}" != users ]] || users="$(sq "$db" 'SELECT count(*) FROM users' 2>/dev/null || echo 0)"
  if ((migrations < 1)) || { [[ "${3:-}" == users ]] && ((users < 1)); }; then
    bad "$label: schema migrations=$migrations users=$users"
    return
  fi
  pass "$label: integrity ok, $migrations migrations${3:+, $users users}"
}

# newest DIR PREFIX: the newest copy named PREFIX + YYYYMMDD.db.
newest() {
  # shellcheck disable=SC2012 # backup names are <prefix>YYYYMMDD.db
  ls -1t "$1/$2"[0-9]*.db 2>/dev/null | head -1 || true
}

# check_copies DIR LABEL: the shared index copy and the private one
# (accounts); a copy from before the split holds both in one file.
check_copies() {
  local index private
  index="$(newest "$1" sentinela-)" private="$(newest "$1" sentinela-private-)"
  if [[ -z "$index" ]]; then
    bad "$2: no database copy in $1 (has the nightly backup run?)"
  elif [[ -z "$private" ]]; then
    check_db "$index" "$2 $(basename "$index")" users
  else
    check_db "$index" "$2 $(basename "$index")"
    check_db "$private" "$2 $(basename "$private")" users
  fi
}

# 1. Local database copies.
check_copies "$DEST/db" "local copy"

# 2. Config and secrets backup.
if [[ ! -f "$DEST/etc/config.yaml" || ! -f "$DEST/etc/secrets.env" ]]; then
  bad "no config.yaml/secrets.env in $DEST/etc"
elif ! docker image inspect "$IMAGE" >/dev/null 2>&1; then
  skip "config check: image $IMAGE not on this host"
else
  install -m 0644 "$DEST/etc/config.yaml" "$WORK/config.yaml"
  if out="$(docker run --rm --network none --env-file "$DEST/etc/secrets.env" \
      -v "$WORK/config.yaml:/etc/sentinela/config.yaml:ro" "$IMAGE" check-config 2>&1)"; then
    pass "config backup: valid, $(printf '%s\n' "$out" | grep -c .) camera(s)"
  else
    bad "config backup does not validate: $(printf '%s' "$out" | tail -1)"
  fi
fi

# 3. Off-box restic copy.
if [[ ! -f "$RESTIC_ENV" ]]; then
  skip "restic: $RESTIC_ENV not configured"
else
  set -a
  # shellcheck disable=SC1090
  . "$RESTIC_ENV"
  set +a
  if [[ -z "${RESTIC_MOUNT:-}" ]] || ! mountpoint -q "$RESTIC_MOUNT"; then
    skip "restic: ${RESTIC_MOUNT:-RESTIC_MOUNT unset} is not mounted"
  elif ! restic check --quiet --read-data-subset=2% >/dev/null 2>&1; then
    bad "restic: repository check failed (restic check --read-data-subset=2%)"
  elif ! restic restore latest --quiet --target "$WORK/restic" --include "$DEST/db" >/dev/null 2>&1; then
    bad "restic: could not restore $DEST/db from the latest snapshot"
  else
    pass "restic: repository sample checked, snapshot restored"
    check_copies "$WORK/restic$DEST/db" "restic copy"
  fi
fi

if ((fail)); then
  echo "RESTORE DRILL FAILED"
  exit 1
fi
echo "RESTORE DRILL PASSED"
