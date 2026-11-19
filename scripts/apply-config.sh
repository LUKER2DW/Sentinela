#!/usr/bin/env bash
# Apply settings changed in the dashboard (ADR-0024). Run as root by
# sentinela-config.service when the API leaves <data>/config/request:
#   pending.yaml           the new config.yaml (already checked by the API)
#   pending-secrets.json   camera credentials to add or change (0600)
# It checks the candidate again with the real secrets (sentinela
# check-config, no network) and against the installed config: the files come
# from a directory the unprivileged containers can write, so the host applies
# its own policy (config.CheckDashboardChange: apps and the update source stay
# as installed, public ports 1024..65535, nas volumes inside /srv/nas) instead
# of trusting the API's. Then it keeps backups, installs, regenerates go2rtc's
# streams, recreates the services and rolls back when the health check
# fails. The result goes to /var/lib/sentinela/config-apply.json.
set -euo pipefail

ETC=/etc/sentinela
DIR="${SENTINELA_DIR:-/opt/sentinela}"
# shellcheck source=scripts/lib/safe.sh
source "$DIR/scripts/lib/safe.sh"
REQ="${SENTINELA_DATA:-/data}/config"
STATUS=/var/lib/sentinela/config-apply.json
[[ $EUID -eq 0 ]] || { echo "run with sudo" >&2; exit 1; }
[[ -f "$REQ/request" ]] || exit 0
exec 9>/run/sentinela-config.lock
flock -n 9 || exit 0

status() { # state message
  printf '{"at":"%s","state":"%s","message":"%s"}\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$1" "${2//\"/\'}" > "$STATUS.tmp"
  chmod 0644 "$STATUS.tmp" && mv -f "$STATUS.tmp" "$STATUS"
}
# <data>/config belongs to uid 1000: follow no link found in it.
if ! refuse_symlink "$REQ" "$REQ/request" "$REQ/pending.yaml" "$REQ/pending-secrets.json" 2>/dev/null; then
  status failed "refused: a symbolic link in the settings request"
  exit 1
fi
work="$(mktemp -d)"
# shellcheck disable=SC2329 # run by the EXIT trap
cleanup() { rm -rf "$work"; rm -f "$REQ/request" "$REQ/pending.yaml" "$REQ/pending-secrets.json"; }
trap cleanup EXIT
status applying ""

[[ -f "$REQ/pending.yaml" ]] || { status failed "nothing to apply"; exit 1; }
dd if="$REQ/pending.yaml" of="$work/config.yaml" iflag=nofollow bs=1M count=1 status=none \
  || { status failed "could not read the settings request"; exit 1; }
chmod 0644 "$work/config.yaml"
# Merge new credentials into a copy of secrets.env (values never logged).
if ! python3 - "$ETC/secrets.env" "$REQ/pending-secrets.json" "$work/secrets.env" <<'PY'
import json, os, re, sys
src, req, out = sys.argv[1:]
lines = open(src).read().splitlines()
try:
    # No link is followed: the request directory belongs to uid 1000.
    with os.fdopen(os.open(req, os.O_RDONLY | os.O_NOFOLLOW), encoding="utf-8") as f:
        new = json.load(f)
except FileNotFoundError:
    new = {}
except Exception:
    sys.exit("bad credential request")
if not isinstance(new, dict):
    sys.exit("bad credential request")
# secrets.env holds camera USER/PASS pairs only (credEnv in the API names
# them <ID>_USER / <ID>_PASS): a new name must look like that; an existing
# one (a camera whose user_env was written by hand) may be changed.
ok = re.compile(r"^[A-Z0-9_]{1,59}_(USER|PASS)$")
have = {l.split("=", 1)[0] for l in lines if "=" in l}
for k, v in new.items():
    if not (ok.match(k) or k in have) or not isinstance(v, str) or "\n" in v or "\r" in v or "\0" in v or len(v) > 256:
        sys.exit("bad credential entry")
    lines = [l for l in lines if not l.startswith(k + "=")]
    lines.append(f"{k}={v}")
open(out, "w").write("\n".join(lines) + "\n")
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
    status applied "backup: config.yaml.bak-$ts"
    exit 0
  fi
  sleep 2
done
rollback
status failed "health check failed; rolled back"
exit 1
