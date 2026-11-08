#!/usr/bin/env bash
# Automatic updates from the project's releases (ADR-0023). Run as root:
#   --scheduled  by sentinela-update.timer every hour; acts only when the
#                dashboard (System -> Atualizações) turned updates on and
#                this is the chosen hour;
#   --request    by sentinela-update.path when the dashboard asks: "check"
#                only looks the latest release up and reports it; "install"
#                installs it now, past the guards below (the person asked);
#   --now        by hand.
# Preferences come from <data>/updates/settings.json (written by the
# dashboard or setup-auto-update.sh); where releases come from only from the
# root-owned /etc/sentinela/auto-update.env. Nothing is built here: the
# release already holds the server image (built in CI) and deploy files.
#
#   1. Read the latest release of SENTINELA_UPDATE_REPO (GitHub API).
#   2. Skip when the running version is the same or newer, when it is not a
#      release (a build deployed from a computer) unless
#      SENTINELA_UPDATE_MANUAL=replace, or when the release is a major
#      version ahead and SENTINELA_UPDATE_MAJOR is not "yes".
#   3. Download SHA256SUMS, the deploy bundle and image.txt; check them.
#   4. Pull the image by digest, keep the current one as sentinela:previous,
#      install the deploy files, regenerate go2rtc's streams, restart.
#   5. Health check; on failure put the previous image and files back.
# A status file (/var/lib/sentinela/update.json) feeds the System page.
set -euo pipefail

ETC=/etc/sentinela
DIR="${SENTINELA_DIR:-/opt/sentinela}"
STATE=/var/lib/sentinela
STATUS="$STATE/update.json"
ENV_FILE="$ETC/auto-update.env"
[[ $EUID -eq 0 ]] || { echo "run with sudo" >&2; exit 1; }
# shellcheck disable=SC1090
[[ -f "$ENV_FILE" ]] && . "$ENV_FILE"
REPO="${SENTINELA_UPDATE_REPO:-LUKER2DW/Sentinela}"
UPDATES="${SENTINELA_DATA:-/data}/updates"
# Dashboard preferences (missing file = off).
read -r ENABLED AT MAJOR MANUAL < <(python3 - "$UPDATES/settings.json" <<'PY'
import json, re, sys
try:
    s = json.load(open(sys.argv[1]))
except Exception:
    s = {}
at = s.get("at") if isinstance(s.get("at"), str) and re.fullmatch(r"([01]\d|2[0-3]):[0-5]\d", s.get("at", "")) else "04:00"
print("yes" if s.get("enabled") is True else "no", at,
      "yes" if s.get("major") is True else "no",
      "replace" if s.get("replace_manual") is True else "keep")
PY
)
ACTION=install # what this run does: install, or only check and report
FORCE=no       # a person asked to install: past the manual-build and major guards
case "${1:---scheduled}" in
  --now) ;;
  --request)
    [[ -f "$UPDATES/request" ]] || exit 0
    read -r req _ < "$UPDATES/request" || true
    rm -f "$UPDATES/request"
    case "$req" in
      check) ACTION=check ;;
      install) FORCE=yes ;;
      *) ;; # an older dashboard's request: check and install with the guards
    esac
    ;;
  --scheduled)
    [[ "$ENABLED" == yes ]] || exit 0
    [[ "$(date +%H)" == "${AT%%:*}" ]] || exit 0
    ;;
  *) echo "usage: $0 [--scheduled|--request|--now]" >&2; exit 2 ;;
esac
[[ "$REPO" =~ ^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$ ]] || { echo "bad SENTINELA_UPDATE_REPO" >&2; exit 1; }

exec 9>/run/sentinela-update.lock
flock -n 9 || { echo "another update is running"; exit 0; }
install -d -m 0755 "$STATE"

log() { echo "update: $*"; }
status() { # state, current, latest, message
  printf '{"checked":"%s","state":"%s","current":"%s","latest":"%s","message":"%s"}\n' \
    "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$1" "$2" "$3" "${4//\"/\'}" > "$STATUS.tmp"
  chmod 0644 "$STATUS.tmp" && mv -f "$STATUS.tmp" "$STATUS"
}
PROGRESS="$STATE/update-progress.json"
# progress PERCENT STEP: what the dashboard's progress bar shows while an
# update installs (STEP is a fixed key the page translates).
progress() {
  printf '{"percent":%s,"step":"%s"}
' "$1" "$2" > "$PROGRESS.tmp"
  chmod 0644 "$PROGRESS.tmp" && mv -f "$PROGRESS.tmp" "$PROGRESS"
}
semver() { [[ "$1" =~ ^v?([0-9]+)\.([0-9]+)\.([0-9]+)$ ]] && printf '%d %d %d' "${BASH_REMATCH[1]}" "${BASH_REMATCH[2]}" "${BASH_REMATCH[3]}"; }
newer() { # newer A B: A > B
  local a b
  read -r -a a <<< "$(semver "$1")"
  read -r -a b <<< "$(semver "$2")"
  for i in 0 1 2; do
    ((a[i] > b[i])) && return 0
    ((a[i] < b[i])) && return 1
  done
  return 1
}

# The running server writes its version at start (instant); an older server
# does not, so ask the image (a few seconds).
current="$(head -c 64 "$UPDATES/version" 2>/dev/null | tr -d '[:space:]')"
[[ "$current" =~ ^[A-Za-z0-9._-]+$ ]] ||
  current="$(docker run --rm --network none --entrypoint /usr/local/bin/sentinela sentinela:local version 2>/dev/null || echo unknown)"
api="https://api.github.com/repos/$REPO/releases/latest"
release="$(curl -fsSL --max-time 30 -H 'Accept: application/vnd.github+json' -A "sentinela-updater/$current" "$api")" ||
  { status error "$current" "" "could not read $api"; exit 1; }
latest="$(printf '%s' "$release" | python3 -c 'import json,sys; print(json.load(sys.stdin)["tag_name"])')"
[[ -n "$(semver "$latest")" ]] || { status error "$current" "$latest" "latest tag is not a version"; exit 1; }

if [[ "$ACTION" == check ]]; then
  if [[ -z "$(semver "$current")" ]]; then
    status manual "$current" "$latest" "running a build deployed from a computer; Update now installs $latest"
  elif newer "$latest" "$current"; then
    status available "$current" "$latest" ""
  else
    status current "$current" "$latest" ""
  fi
  exit 0
fi
if [[ -z "$(semver "$current")" && "$MANUAL" != replace && "$FORCE" != yes ]]; then
  log "running $current, built on a computer: left alone (SENTINELA_UPDATE_MANUAL=replace to change)"
  status manual "$current" "$latest" "running a build deployed from a computer"
  exit 0
fi
if [[ -n "$(semver "$current")" ]] && ! newer "$latest" "$current"; then
  log "up to date ($current)"
  status current "$current" "$latest" ""
  exit 0
fi
if [[ -n "$(semver "$current")" && "$MAJOR" != yes && "$FORCE" != yes ]]; then
  read -r cmaj _ <<< "$(semver "$current")"
  read -r lmaj _ <<< "$(semver "$latest")"
  if ((lmaj > cmaj)); then
    log "$latest is a new major version: update by hand after reading the release notes"
    status major "$current" "$latest" "new major version: read the release notes, then update by hand"
    exit 0
  fi
fi

log "updating $current -> $latest"
status updating "$current" "$latest" ""
progress 5 download
work="$(mktemp -d)"
trap 'rm -rf "$work" "$PROGRESS" "$PROGRESS.tmp"' EXIT
asset() { # asset NAME -> download URL
  printf '%s' "$release" | python3 -c '
import json, sys
name = sys.argv[1]
for a in json.load(sys.stdin)["assets"]:
    if a["name"] == name:
        print(a["browser_download_url"])
' "$1"
}
bundle="sentinela_${latest}_deploy.tar.gz"
pct=10
for f in SHA256SUMS image.txt "$bundle"; do
  progress "$pct" download
  pct=$((pct + 8))
  url="$(asset "$f")"
  [[ "$url" == https://* ]] || { status error "$current" "$latest" "release has no $f"; exit 1; }
  curl -fsSL --proto '=https' --max-time 300 -o "$work/$f" "$url" || { status error "$current" "$latest" "download of $f failed"; exit 1; }
done
progress 35 verify
(cd "$work" && grep -E " \./(image\.txt|$bundle)$" SHA256SUMS | sha256sum -c --quiet -) ||
  { status error "$current" "$latest" "checksum mismatch"; exit 1; }
image="$(head -1 "$work/image.txt")"
[[ "$image" =~ ^ghcr\.io/[a-z0-9._/-]+@sha256:[0-9a-f]{64}$ ]] || { status error "$current" "$latest" "bad image reference"; exit 1; }

progress 40 image
if ! out="$(docker pull -q "$image" 2>&1)"; then
  # A package that is still private on ghcr.io answers "unauthorized".
  case "$out" in
    *unauthorized*|*denied*) status error "$current" "$latest" "the server image is not public on ghcr.io (package settings: change visibility to public)" ;;
    *) status error "$current" "$latest" "could not download the server image" ;;
  esac
  log "$out"
  exit 1
fi
progress 70 install
docker tag sentinela:local sentinela:previous
cp -a "$DIR/deploy" "$work/deploy.previous"
cp -a "$DIR/scripts" "$work/scripts.previous"

rollback() {
  log "rolling back to $current"
  docker tag sentinela:previous sentinela:local
  rm -rf "$DIR/deploy" "$DIR/scripts"
  cp -a "$work/deploy.previous" "$DIR/deploy"
  cp -a "$work/scripts.previous" "$DIR/scripts"
  (cd "$DIR/deploy" && docker compose up -d --no-build >/dev/null 2>&1) || true
}

docker tag "$image" sentinela:local
tar -xzf "$work/$bundle" -C "$DIR"
chmod +x "$DIR"/scripts/*.sh
"$DIR/scripts/gen-go2rtc-cameras.sh" >/dev/null || { rollback; status failed "$current" "$latest" "go2rtc config failed; rolled back"; exit 1; }
APPS_PROFILE=""
[[ -f "$ETC/apps-enabled" ]] && APPS_PROFILE=apps
if ! (cd "$DIR/deploy" && SENTINELA_VERSION="$latest" SENTINELA_DOCKER_GID="$(getent group docker | cut -d: -f3)" \
      COMPOSE_PROFILES="$APPS_PROFILE" docker compose up -d --no-build --remove-orphans >/dev/null); then
  rollback
  status failed "$current" "$latest" "compose up failed; rolled back"
  exit 1
fi
(cd "$DIR/deploy" && docker compose restart go2rtc >/dev/null) || true
progress 85 restart
for _ in $(seq 1 45); do
  if (cd "$DIR/deploy" && docker compose exec -T sentinela /usr/local/bin/sentinela healthcheck >/dev/null 2>&1); then
    log "updated to $latest"
    status updated "$latest" "$latest" "updated from $current"
    docker image prune -f >/dev/null || true
    exit 0
  fi
  sleep 2
done
rollback
status failed "$current" "$latest" "health check failed; rolled back"
exit 1
