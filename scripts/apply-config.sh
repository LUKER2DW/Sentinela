#!/usr/bin/env bash
# Apply settings changed in the dashboard (ADR-0024). Run as root by
# sentinela-config.service when the API leaves <data>/config/request:
#   pending.yaml           the new config.yaml (already checked by the API)
#   pending-secrets.json   camera credentials to add or change (0600)
# It checks the candidate again with the real secrets (sentinela
# check-config, no network) and against the installed config: the files come
# from a directory the unprivileged containers can write, so the host applies
# its own policy (config.CheckDashboardChange: apps and the update source stay
# as installed, public ports 1024..65535, nas volumes inside /srv/nas, camera
# credential names <NAME>_USER/<NAME>_PASS or as installed) instead of
# trusting the API's. Credentials are accepted only for names the
# candidate's cameras use, with values every reader of secrets.env reads the
# same. Then it keeps backups, installs, regenerates go2rtc's streams,
# recreates the services and rolls back when the health check fails. The
# result goes to /var/lib/sentinela/config-apply.json.
set -euo pipefail

ETC=/etc/sentinela
DIR="${SENTINELA_DIR:-/opt/sentinela}"
# shellcheck source=scripts/lib/safe.sh
source "$DIR/scripts/lib/safe.sh"
DATA="${SENTINELA_DATA:-/data}"
REQ="$DATA/config"
STATUS=/var/lib/sentinela/config-apply.json
[[ $EUID -eq 0 ]] || { echo "run with sudo" >&2; exit 1; }
exec 9>/run/sentinela-config.lock
flock -n 9 || exit 0

status() { # state message
  printf '{"at":"%s","state":"%s","message":"%s"}\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$1" "${2//\"/\'}" > "$STATUS.tmp"
  chmod 0644 "$STATUS.tmp" && mv -f "$STATUS.tmp" "$STATUS"
}
# <data>/config belongs to uid 1000: its files are read and removed only
# through safe.sh (no link followed, plain files only, size-capped). The
# request stays until the end: while it exists the API refuses another change.
rc=0
safe_read "$DATA" "$REQ/request" 64 >/dev/null 2>&1 || rc=$?
case "$rc" in
  0) ;;
  2) exit 0 ;;
  *) safe_rm "$DATA" "$REQ/request" "$REQ/pending.yaml" "$REQ/pending-secrets.json" 2>/dev/null || true
     status failed "refused: the settings request is not a plain file"
     exit 1 ;;
esac
work="$(mktemp -d)"
# shellcheck disable=SC2329 # run by the EXIT trap
cleanup() { rm -rf "$work"; safe_rm "$DATA" "$REQ/request" "$REQ/pending.yaml" "$REQ/pending-secrets.json" 2>/dev/null || true; }
trap cleanup EXIT
status applying ""

rc=0
safe_read "$DATA" "$REQ/pending.yaml" 1048576 > "$work/config.yaml" 2>/dev/null || rc=$?
case "$rc" in
  0) ;;
  2) status failed "nothing to apply"; exit 1 ;;
  *) status failed "could not read the settings request"; exit 1 ;;
esac
chmod 0644 "$work/config.yaml"
rc=0
safe_read "$DATA" "$REQ/pending-secrets.json" 262144 > "$work/request.json" 2>/dev/null || rc=$?
[[ "$rc" == 0 || "$rc" == 2 ]] || { status failed "invalid credential request"; exit 1; }

# The credential names the candidate's cameras use (user_env/password_env),
# read by the real parser from the cameras themselves: a stream URL need
# not carry them (a custom path without {user}/{password}).
if ! docker run --rm --network none -v "$work/config.yaml:/etc/sentinela/config.yaml:ro" \
      --entrypoint /usr/local/bin/sentinela sentinela:local credential-names > "$work/names" 2> "$work/names.err"; then
  status failed "invalid settings: $(cat "$work/names" "$work/names.err" | tail -1)"
  exit 1
fi

# Merge new credentials into a copy of secrets.env (values never logged).
if ! python3 -I - "$ETC/secrets.env" "$work/request.json" "$work/names" "$work/secrets.env" <<'PY'
import json, re, sys
src, req, names, out = sys.argv[1:]
# secrets.env is split on "\n" only, the one line break every reader of the
# file agrees on (str.splitlines() would also split at \v, \f, \x1c-\x1e,
# \x85, U+2028...); its lines are kept as bytes, untouched.
lines = open(src, "rb").read().split(b"\n")
if lines[-1] == b"":
    lines.pop()
try:
    raw = open(req, "rb").read()
    new = json.loads(raw) if raw.strip() else {}
except Exception:
    sys.exit("bad credential request")
if not isinstance(new, dict):
    sys.exit("bad credential request")
# Only names a camera of the candidate uses (user_env/password_env), shaped
# like the API's <ID>_USER / <ID>_PASS or already in the file (a camera whose
# names were written by hand): nothing else can be set from a directory the
# containers can write.
used = set(open(names).read().split())
shape = re.compile(r"[A-Z0-9_]{1,59}_(USER|PASS)")
have = {l.split(b"=", 1)[0] for l in lines if b"=" in l}
# Values (config.SecretValueOK): 1-256 printable ASCII characters except
# space, quotes, backslash, $ { } and #. Docker's --env-file (the check
# below) reads a value literally; Compose's env_file expands ${...} and
# strips quotes and comments; go2rtc pastes it into its YAML. Without these
# characters every reader sees the same text, and no value can carry a line
# break or a control character that starts another variable.
bad = set("\"#$'\\`{}")
def value_ok(v):
    return 0 < len(v) <= 256 and all("!" <= c <= "~" and c not in bad for c in v)
for k, v in new.items():
    if not (isinstance(k, str) and isinstance(v, str) and k in used
            and (shape.fullmatch(k) or k.encode() in have) and value_ok(v)):
        sys.exit("bad credential entry")
    kb = k.encode()
    lines = [l for l in lines if not l.startswith(kb + b"=")]
    lines.append(kb + b"=" + v.encode())
with open(out, "wb") as f:
    f.write(b"\n".join(lines) + b"\n")
PY
then
  status failed "invalid credential request"
  exit 1
fi
chmod 0600 "$work/secrets.env"

# -against: besides parsing, the change must stay within what the dashboard
# may edit, compared with the config installed now (mounted read-only).
if ! out="$(docker run --rm --network none --env-file "$work/secrets.env" \
      -v "$work/config.yaml:/etc/sentinela/config.yaml:ro" \
      -v "$ETC/config.yaml:/run/installed-config.yaml:ro" \
      sentinela:local check-config -against /run/installed-config.yaml 2>&1)"; then
  if [[ "$out" == *'policy:'* ]]; then
    status failed "refused by the host policy: $(printf '%s' "$out" | tail -1)"
  else
    status failed "invalid settings: $(printf '%s' "$out" | tail -1)"
  fi
  exit 1
fi

ts="$(date +%Y%m%d%H%M%S)"
cp -p "$ETC/config.yaml" "$ETC/config.yaml.bak-$ts"
cp -p "$ETC/secrets.env" "$ETC/secrets.env.bak-$ts"
chmod 0600 "$ETC/secrets.env.bak-$ts"
# shellcheck disable=SC2012 # names are config.yaml.bak-<timestamp>
ls -1t "$ETC"/config.yaml.bak-* 2>/dev/null | tail -n +11 | xargs -r rm -f
# shellcheck disable=SC2012
ls -1t "$ETC"/secrets.env.bak-* 2>/dev/null | tail -n +11 | xargs -r rm -f

rollback() {
  cp -p "$ETC/config.yaml.bak-$ts" "$ETC/config.yaml"
  cp -p "$ETC/secrets.env.bak-$ts" "$ETC/secrets.env"
  "$DIR/scripts/gen-go2rtc-cameras.sh" >/dev/null || true
  (cd "$DIR/deploy" && docker compose up -d --no-build --force-recreate sentinela recorder go2rtc >/dev/null 2>&1) || true
}

install -m 0644 -o root -g root "$work/config.yaml" "$ETC/config.yaml"
install -m 0600 -o root -g root "$work/secrets.env" "$ETC/secrets.env"
"$DIR/scripts/gen-go2rtc-cameras.sh" >/dev/null || { rollback; status failed "go2rtc streams failed; rolled back"; exit 1; }
if ! (cd "$DIR/deploy" && docker compose up -d --no-build --force-recreate sentinela recorder go2rtc >/dev/null 2>&1); then
  rollback
  status failed "restart failed; rolled back"
  exit 1
fi
for _ in $(seq 1 45); do
  if (cd "$DIR/deploy" && docker compose exec -T sentinela /usr/local/bin/sentinela healthcheck >/dev/null 2>&1); then
    # The public address follows the settings (ADR-0019): open its ports
    # when it is on, close them when it is off or its port changed.
    if docker run --rm --network none -v "$ETC/config.yaml:/etc/sentinela/config.yaml:ro" \
        --entrypoint /usr/local/bin/sentinela sentinela:local public-ports 2>/dev/null | grep -q .; then
      touch "$ETC/public-access"
    else
      rm -f "$ETC/public-access"
    fi
    "$DIR/scripts/firewall.sh" >/dev/null 2>&1 || true
    # A Cloudflare Tunnel may reach the proxy listener's port alone.
    "$DIR/scripts/setup-cloudflared.sh" firewall >/dev/null 2>&1 || true
    status applied "backup: config.yaml.bak-$ts"
    exit 0
  fi
  sleep 2
done
rollback
status failed "health check failed; rolled back"
exit 1
