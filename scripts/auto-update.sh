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
# root-owned /etc/sentinela/auto-update.env, and the running version only
# from the installed image (never from the data disk, which the containers
# can write). Nothing is built here: the release already holds the server
# image (built in CI) and deploy files.
#
#   1. Read the latest release of SENTINELA_UPDATE_REPO (GitHub API).
#   2. Skip when the running version is the same or newer, when it is not a
#      release (a build deployed from a computer) unless
#      SENTINELA_UPDATE_MANUAL=replace, or when the release is a major
#      version ahead and SENTINELA_UPDATE_MAJOR is not "yes".
#   3. Download SHA256SUMS, the deploy bundle and image.txt; check them.
#   4. Pull the image by digest, keep the current one as sentinela:previous,
#      install the deploy files (owned by root), regenerate go2rtc's streams,
#      restart.
#   5. Health check; on failure put the previous image and files back. On
#      success, reinstall the host units from the new files, as deploy.sh does
#      (a setup that fails is named in the status).
# Every run first checks that the host units match the deploy files (heal):
# a release installed by an older auto-update.sh gets them on the next run.
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
# shellcheck source=scripts/lib/safe.sh
source "$DIR/scripts/lib/safe.sh"
REPO="${SENTINELA_UPDATE_REPO:-LUKER2DW/Sentinela}"
DATA="${SENTINELA_DATA:-/data}"
UPDATES="$DATA/updates"
# Dashboard preferences (missing file = off). <data>/updates belongs to uid
# 1000: its files are read only through safe.sh (no link followed, a small
# plain file), never by name.
PREFS='
import json, re, sys
try:
    s = json.loads(sys.stdin.read())
except Exception:
    s = {}
if not isinstance(s, dict):
    s = {}
at = s.get("at") if isinstance(s.get("at"), str) and re.fullmatch(r"([01]\d|2[0-3]):[0-5]\d", s.get("at", "")) else "04:00"
print("yes" if s.get("enabled") is True else "no", at,
      "yes" if s.get("major") is True else "no",
      "replace" if s.get("replace_manual") is True else "keep")
'
read -r ENABLED AT MAJOR MANUAL < <({ safe_read "$DATA" "$UPDATES/settings.json" 4096 2>/dev/null || true; } | python3 -I -c "$PREFS")
ACTION=install # what this run does: install, or only check and report
FORCE=no       # a person asked to install: past the manual-build and major guards
MODE="${1:---scheduled}"
case "$MODE" in
  --now) ;;
  --request)
    rc=0
    req="$(safe_take "$DATA" "$UPDATES/request" 64)" || rc=$?
    case "$rc" in 0) ;; 2) exit 0 ;; *) exit 1 ;; esac
    read -r req _ <<< "$req" || true
    case "$req" in
      check) ACTION=check ;;
      install) FORCE=yes ;;
      *) ;; # an older dashboard's request: check and install with the guards
    esac
    ;;
  --scheduled) ;; # acts only when turned on and in the chosen hour (below)
  *) echo "usage: $0 [--scheduled|--request|--now]" >&2; exit 2 ;;
esac

exec 9>/run/sentinela-update.lock
flock -n 9 || { echo "another update is running"; exit 0; }
install -d -m 0755 "$STATE"

log() { echo "update: $*"; }
# root_owned: what root runs (scripts, compose file, units) belongs to root
# and only root may change it, whatever the archive or an earlier install
# recorded as the owner.
root_owned() {
  chown -R root:root "$DIR/deploy" "$DIR/scripts" && chmod -R go-w "$DIR/deploy" "$DIR/scripts" &&
    chown root:root "$DIR" && chmod go-w "$DIR" && chmod +x "$DIR"/scripts/*.sh
}
# The host units come with the deploy files: deploy.sh installs them with
# the setup scripts below, and so does an update after its health check.
UNITS_STAMP="$STATE/host-units.sha256"
units_sum() { # the files the host units and their setups come from
  (cd "$DIR" && find deploy/host scripts/lib scripts/setup-*.sh -type f -print0 | sort -z | xargs -0 sha256sum) |
    sha256sum | cut -c1-64
}
# install_host: runs the setups, records the stamp, prints those that failed.
install_host() {
  local setup failed=""
  for setup in "setup-auto-update.sh install" setup-config-apply.sh "setup-tailscale.sh units" "setup-cloudflared.sh units"; do
    # shellcheck disable=SC2086 # the script and its argument
    if ! "$DIR"/scripts/$setup >/dev/null 2>&1; then
      log "warning: $setup failed" >&2
      failed+="${failed:+, }${setup%% *}"
    fi
  done
  if units_sum > "$UNITS_STAMP.tmp"; then
    mv -f "$UNITS_STAMP.tmp" "$UNITS_STAMP"
  fi
  printf '%s' "$failed"
}
# heal: these deploy files may have been installed by an older
# auto-update.sh, which reinstalled no host unit and left deploy/ and
# scripts/ owned as unpacked (or by deploy.sh, which records no stamp). The
# first run of this script after that, whatever started it, does what an
# update does after its health check, once per set of host files.
heal() {
  local want
  want="$(units_sum)" || return 0
  [[ "$(cat "$UNITS_STAMP" 2>/dev/null)" != "$want" ]] || return 0
  log "host units older than these deploy files: installing them"
  root_owned || log "warning: could not make root own the deploy files"
  install_host >/dev/null
}
heal

if [[ "$MODE" == --scheduled ]]; then
  [[ "$ENABLED" == yes ]] || exit 0
  [[ "$(date +%H)" == "${AT%%:*}" ]] || exit 0
fi
[[ "$REPO" =~ ^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$ ]] || { echo "bad SENTINELA_UPDATE_REPO" >&2; exit 1; }
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

# The running version comes from the installed image itself (a few seconds),
# never from <data>/updates/version: the API writes that file, and anything
# that runs as uid 1000 could make root believe it is up to date.
current="$(docker run --rm --network none --entrypoint /usr/local/bin/sentinela sentinela:local version 2>/dev/null | head -1 | tr -d '[:space:]')" || current=""
[[ "$current" =~ ^[A-Za-z0-9._-]{1,64}$ ]] || current=unknown
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
# The bundle holds deploy/ and scripts/ only: plain files and directories.
python3 -I - "$work/$bundle" <<'PY' || { status error "$current" "$latest" "unexpected file in the deploy bundle"; exit 1; }
import re, sys, tarfile
name = re.compile(r"(deploy|scripts)(/[A-Za-z0-9._-]+)+/?")
seen = set()
with tarfile.open(sys.argv[1], "r:gz") as t:
    for m in t:
        if not (m.isfile() or m.isdir()) or not name.fullmatch(m.name) or {".", ".."} & set(m.name.split("/")):
            sys.exit(f"unexpected member {m.name!r}")
        seen.add(m.name)
if not {"deploy/docker-compose.yml", "scripts/auto-update.sh"} <= seen:
    sys.exit("incomplete bundle")
PY

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
  root_owned || true
  (cd "$DIR/deploy" && docker compose up -d --no-build >/dev/null 2>&1) || true
}

docker tag "$image" sentinela:local
# --no-same-owner: root's tar would otherwise give every file the owner
# recorded in the archive (the build machine's account).
if ! tar --no-same-owner --no-same-permissions -xzf "$work/$bundle" -C "$DIR" || ! root_owned; then
  rollback
  status failed "$current" "$latest" "could not install the deploy files; rolled back"
  exit 1
fi
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
    # The release's host units take effect now (a changed unit, such as the
    # tunnel's sandbox); a setup that failed is named in the status.
    progress 95 restart
    failed="$(install_host)"
    status updated "$latest" "$latest" "updated from $current${failed:+; host setup failed: $failed, see the journal}"
    docker image prune -f >/dev/null || true
    exit 0
  fi
  sleep 2
done
rollback
status failed "$current" "$latest" "health check failed; rolled back"
exit 1
