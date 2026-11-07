#!/usr/bin/env bash
# Restore drill (ADR-0014): prove that the backups can be restored, without
# touching the running system. Run as root on the server, e.g. monthly:
#
#   sudo /opt/sentinela/scripts/restore-drill.sh
#
# 1. The newest local database copy (/srv/backup/db) opens, passes
#    PRAGMA integrity_check, has its schema and at least one user.
# 2. The config backup (/srv/backup/etc) validates with the current image,
#    secrets included (sentinela check-config, no network).
# 3. With /etc/sentinela/restic.env and its disk mounted: restic checks a
#    sample of the repository data and restores the newest database copy
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
trap 'rm -rf "$WORK"' EXIT
chmod 0700 "$WORK"
fail=0
pass() { printf 'PASS  %s\n' "$*"; }
bad() { printf 'FAIL  %s\n' "$*"; fail=1; }
skip() { printf 'SKIP  %s\n' "$*"; }

# check_db FILE LABEL: integrity, schema and users of a database copy.
check_db() {
  local db="$1" label="$2" integrity users migrations
  integrity="$(sqlite3 -readonly "$db" 'PRAGMA integrity_check' 2>&1 || true)"
  if [[ "$integrity" != ok ]]; then
    bad "$label: integrity_check: $integrity"
    return
  fi
  migrations="$(sqlite3 -readonly "$db" 'SELECT count(*) FROM schema_migrations' 2>/dev/null || echo 0)"
  users="$(sqlite3 -readonly "$db" 'SELECT count(*) FROM users' 2>/dev/null || echo 0)"
  if ((migrations < 1 || users < 1)); then
    bad "$label: schema migrations=$migrations users=$users"
    return
  fi
  pass "$label: integrity ok, $migrations migrations, $users users"
}

# 1. Local database copy.
# shellcheck disable=SC2012 # backup names are sentinela-YYYYMMDD.db
newest="$(ls -1t "$DEST"/db/sentinela-*.db 2>/dev/null | head -1 || true)"
if [[ -z "$newest" ]]; then
  bad "no database copy in $DEST/db (has the nightly backup run?)"
else
  cp -a "$newest" "$WORK/local.db"
  check_db "$WORK/local.db" "local copy $(basename "$newest")"
fi

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
    # shellcheck disable=SC2012
    restored="$(ls -1t "$WORK/restic$DEST"/db/sentinela-*.db 2>/dev/null | head -1 || true)"
    if [[ -z "$restored" ]]; then
      bad "restic: the latest snapshot holds no database copy"
    else
      pass "restic: repository sample checked, snapshot restored"
      check_db "$restored" "restic copy $(basename "$restored")"
    fi
  fi
fi

if ((fail)); then
  echo "RESTORE DRILL FAILED"
  exit 1
fi
echo "RESTORE DRILL PASSED"
