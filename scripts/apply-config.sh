#!/usr/bin/env bash
# Apply settings changed in the dashboard (ADR-0024). Run as root by
# sentinela-config.service when the API leaves <data>/config/request:
#   pending.yaml           the new config.yaml (already checked by the API)
#   pending-secrets.json   camera credentials to add or change (0600)
# It checks the candidate again with the real secrets (sentinela
# check-config, no network), keeps backups, installs, regenerates go2rtc's
# streams, recreates the services and rolls back when the health check
# fails. The result goes to /var/lib/sentinela/config-apply.json.
set -euo pipefail

ETC=/etc/sentinela
DIR="${SENTINELA_DIR:-/opt/sentinela}"
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
work="$(mktemp -d)"
# shellcheck disable=SC2329 # run by the EXIT trap
cleanup() { rm -rf "$work"; rm -f "$REQ/request" "$REQ/pending.yaml" "$REQ/pending-secrets.json"; }
trap cleanup EXIT
status applying ""

[[ -f "$REQ/pending.yaml" ]] || { status failed "nothing to apply"; exit 1; }
install -m 0644 "$REQ/pending.yaml" "$work/config.yaml"
# Merge new credentials into a copy of secrets.env (values never logged).
python3 - "$ETC/secrets.env" "$REQ/pending-secrets.json" "$work/secrets.env" <<'PY'
import json, re, sys
src, req, out = sys.argv[1:]
lines = open(src).read().splitlines()
try:
    new = json.load(open(req))
except Exception:
    new = {}
ok = re.compile(r"^[A-Z0-9_]{1,64}$")
for k, v in new.items():
    if not ok.match(k) or not isinstance(v, str) or "\n" in v or len(v) > 256:
        sys.exit("bad credential entry")
    lines = [l for l in lines if not l.startswith(k + "=")]
    lines.append(f"{k}={v}")
open(out, "w").write("\n".join(lines) + "\n")
PY
chmod 0600 "$work/secrets.env"

if ! out="$(docker run --rm --network none --env-file "$work/secrets.env" \
      -v "$work/config.yaml:/etc/sentinela/config.yaml:ro" sentinela:local check-config 2>&1)"; then
  status failed "invalid settings: $(printf '%s' "$out" | tail -1)"
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
    status applied "backup: config.yaml.bak-$ts"
    exit 0
  fi
  sleep 2
done
rollback
status failed "health check failed; rolled back"
exit 1
