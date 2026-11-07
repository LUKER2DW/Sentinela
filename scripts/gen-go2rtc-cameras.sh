#!/usr/bin/env bash
# Regenerate go2rtc's camera streams from config.yaml (run as root on the
# server). Every camera, any vendor; credentials stay ${VAR} placeholders
# that go2rtc expands from secrets.env, so this needs no secret.
#
#   sudo scripts/gen-go2rtc-cameras.sh [config.yaml] [output.yaml]
#
# Prints "changed" when the output differs from the installed file, so
# callers know go2rtc must restart to pick it up.
set -euo pipefail
ETC=/etc/sentinela
CFG="${1:-$ETC/config.yaml}"
OUT="${2:-$ETC/go2rtc-cameras.yaml}"
IMAGE="${SENTINELA_IMAGE:-sentinela:local}"
[[ $EUID -eq 0 ]] || { echo "run with sudo" >&2; exit 1; }
[[ -f "$CFG" ]] || { echo "no $CFG" >&2; exit 1; }

tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT
docker run --rm --network none -v "$CFG:/etc/sentinela/config.yaml:ro" "$IMAGE" go2rtc-config >"$tmp"
if [[ -f "$OUT" ]] && cmp -s "$tmp" "$OUT"; then
  exit 0
fi
install -m 0644 -o root -g root "$tmp" "$OUT"
echo changed
