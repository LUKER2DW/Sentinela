#!/usr/bin/env bash
# Add a camera to Sentinela. Run on the server (for XMEye cameras, after
# the System page's "Adicionar câmera" test passed):
#
#   sudo /opt/sentinela/scripts/add-camera.sh cam2 192.168.1.21 "Fundo"
#   sudo VENDOR=hikvision /opt/sentinela/scripts/add-camera.sh garagem 192.168.1.30
#   sudo VENDOR=generic RTSP_MAIN=/stream1 RTSP_SUB=/stream2 \
#     /opt/sentinela/scripts/add-camera.sh quintal 192.168.1.31
#
# Optional env: VENDOR (xmeye|hikvision|dahua|generic), CHANNEL (1),
# RTSP_MAIN / RTSP_SUB (stream paths; RTSP_MAIN required for generic),
# RTSP_PORT (554), DVRIP_PORT (34567), ONVIF_PORT (8899 for xmeye, else 80),
# RETENTION_DAYS (0 = until the disk is full), INVERT_PAN_TILT (false).
#
# The password is read silently and written only to /etc/sentinela/secrets.env
# (0600). The new config (credentials included) is validated with
# `sentinela check-config` before it replaces the old one (backups:
# *.bak-<timestamp>). Then go2rtc's camera file is regenerated and the
# stack is recreated: recording pauses for a few seconds.
set -euo pipefail

ETC=/etc/sentinela
DIR="${SENTINELA_DIR:-/opt/sentinela}"
say()  { printf '\n\033[1;33m==> %s\033[0m\n' "$*"; }
warn() { printf '\033[1;31m!! %s\033[0m\n' "$*" >&2; }

[[ $EUID -eq 0 ]] || { warn "run with sudo"; exit 1; }
[[ $# -ge 2 ]] || { sed -n '2,19p' "$0"; exit 1; }
ID="$1" HOST="$2" NAME="${3:-$1}"
VENDOR="${VENDOR:-xmeye}" CHANNEL="${CHANNEL:-1}" RTSP_MAIN="${RTSP_MAIN:-}" RTSP_SUB="${RTSP_SUB:-}"
DEF_ONVIF=80
[[ "$VENDOR" != xmeye ]] || DEF_ONVIF=8899
RTSP_PORT="${RTSP_PORT:-554}" DVRIP_PORT="${DVRIP_PORT:-34567}" ONVIF_PORT="${ONVIF_PORT:-$DEF_ONVIF}"
RETENTION_DAYS="${RETENTION_DAYS:-0}" INVERT_PAN_TILT="${INVERT_PAN_TILT:-false}"
[[ "$VENDOR" =~ ^(xmeye|hikvision|dahua|generic)$ ]] || { warn "VENDOR: xmeye, hikvision, dahua or generic"; exit 1; }
for p in "$RTSP_MAIN" "$RTSP_SUB"; do [[ -z "$p" || "$p" =~ ^/[A-Za-z0-9._~/?\&=%{}:-]*$ ]] || { warn "RTSP paths start with / (letters, digits and / . _ - ? & = %)"; exit 1; }; done

[[ "$ID" =~ ^[a-z0-9][a-z0-9_-]{0,31}$ ]] || { warn "id must match ^[a-z0-9][a-z0-9_-]{0,31}\$"; exit 1; }
[[ "$HOST" =~ ^[0-9]{1,3}(\.[0-9]{1,3}){3}$ ]] || { warn "host must be an IPv4 address"; exit 1; }
[[ "$NAME" != *[\"\\]* && ${#NAME} -le 40 ]] || { warn "name: up to 40 characters, no quotes or backslashes"; exit 1; }
for p in "$CHANNEL" "$RTSP_PORT" "$DVRIP_PORT" "$ONVIF_PORT" "$RETENTION_DAYS"; do [[ "$p" =~ ^[0-9]+$ ]] || { warn "ports/days must be numbers"; exit 1; }; done
[[ "$INVERT_PAN_TILT" == true || "$INVERT_PAN_TILT" == false ]] || { warn "INVERT_PAN_TILT: true or false"; exit 1; }
if grep -qE "^[[:space:]]*-[[:space:]]*id:[[:space:]]*${ID}([[:space:]]|$)" "$ETC/config.yaml"; then warn "camera id $ID already exists"; exit 1; fi
if grep -qE "^[[:space:]]*host:[[:space:]]*${HOST//./\\.}([[:space:]]|$)" "$ETC/config.yaml"; then warn "a camera at $HOST already exists"; exit 1; fi

ENV="$(printf '%s' "${ID^^}" | tr -- '-' '_')"
read -r -p "Username of $HOST [admin]: " CAM_USER; CAM_USER="${CAM_USER:-admin}"
read -r -s -p "Password (not shown): " CAM_PASS; echo
[[ -n "$CAM_PASS" ]] || { warn "empty password"; exit 1; }
# Characters the camera's RTSP URL cannot carry are refused by check-config
# below (they differ per vendor).
if [[ "$CAM_USER$CAM_PASS" == *[\'\"\\]* ]]; then
  warn "credentials cannot carry quotes or backslashes: change the camera password first"; exit 1
fi

TMP="$(mktemp -d -p "$ETC")"
trap 'rm -rf "$TMP"; unset CAM_PASS' EXIT
chmod 0700 "$TMP"

say "1/4 Candidate config"
# Insert the camera after the last indented line of the top-level `cameras:` block.
awk -v entry="$(cat <<EOF
  - id: ${ID}
    name: "${NAME}"
    host: ${HOST}
    vendor: ${VENDOR}
    channel: ${CHANNEL}${RTSP_MAIN:+
    rtsp_main: "${RTSP_MAIN}"}${RTSP_SUB:+
    rtsp_sub: "${RTSP_SUB}"}
    user_env: ${ENV}_USER
    password_env: ${ENV}_PASS
    rtsp_port: ${RTSP_PORT}
    dvrip_port: ${DVRIP_PORT}
    onvif_port: ${ONVIF_PORT}
    retention_days: ${RETENTION_DAYS}
    invert_pan_tilt: ${INVERT_PAN_TILT}
EOF
)" '
  { lines[NR] = $0 }
  /^cameras:/ { inblock = 1; last = NR; next }
  inblock && /^[^[:space:]]/ { inblock = 0 }
  inblock && /^[[:space:]]+[^[:space:]]/ { last = NR }
  END {
    if (!last) { print "no cameras: block" > "/dev/stderr"; exit 1 }
    for (i = 1; i <= NR; i++) { print lines[i]; if (i == last) print entry }
  }' "$ETC/config.yaml" > "$TMP/config.yaml"
cp "$ETC/secrets.env" "$TMP/secrets.env"
printf '%s_USER=%s\n%s_PASS=%s\n' "$ENV" "$CAM_USER" "$ENV" "$CAM_PASS" >> "$TMP/secrets.env"
unset CAM_PASS

say "2/4 Validate"
chmod 0644 "$TMP/config.yaml"  # bind-mounted for uid 1000; secrets.env is read by the docker CLI (root)
if ! CAMS="$(docker run --rm --network none --env-file "$TMP/secrets.env" \
    -v "$TMP/config.yaml:/etc/sentinela/config.yaml:ro" sentinela:local check-config)"; then
  warn "the new config is invalid; nothing was changed"; exit 1
fi
printf '%s\n' "$CAMS"

say "3/4 Install (backups: *.bak-$(date +%Y%m%d%H%M%S))"
TS="$(date +%Y%m%d%H%M%S)"
cp -p "$ETC/config.yaml" "$ETC/config.yaml.bak-$TS"
cp -p "$ETC/secrets.env" "$ETC/secrets.env.bak-$TS"
install -m 0644 -o root -g root "$TMP/config.yaml" "$ETC/config.yaml"
install -m 0600 -o root -g root "$TMP/secrets.env" "$ETC/secrets.env"
chmod 0600 "$ETC/secrets.env.bak-$TS"

# go2rtc streams for every camera, from the new config.yaml.
"$DIR/scripts/gen-go2rtc-cameras.sh" >/dev/null

say "4/4 Recreate the stack (recording pauses a few seconds)"
cd "$DIR/deploy"
docker compose up -d --no-build --force-recreate go2rtc recorder sentinela
for _ in $(seq 1 30); do
  if docker compose exec -T sentinela /usr/local/bin/sentinela healthcheck >/dev/null 2>&1; then
    echo "Camera ${ID} added. Open the Ao vivo page; recording starts within a minute."
    exit 0
  fi
  sleep 2
done
warn "health check failed; to roll back: cp $ETC/config.yaml.bak-$TS $ETC/config.yaml; cp $ETC/secrets.env.bak-$TS $ETC/secrets.env; then docker compose up -d --force-recreate"
exit 1
