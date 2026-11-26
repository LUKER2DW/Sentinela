#!/usr/bin/env bash
# Deploy from a dev machine to the server over SSH. The only deploy path
# (ADR-0010): the server never builds or pulls code by itself.
#
#   ./scripts/deploy.sh                 # server from ~/.sentinela/deploy.env
#   ./scripts/deploy.sh --services      # API/dashboard + apps; recorders keep running
#   SENTINELA_HOST=user@host ./scripts/deploy.sh
#
# Builds the image here (fast machine), streams it to the server with
# `docker save | ssh docker load`, ships deploy/ + scripts/ to /opt/sentinela,
# restarts the stack and waits for the health check. compose runs via sudo
# because it reads the root-only /etc/sentinela/secrets.env. The server never needs
# the source tree, Go or Node. First-time server setup: install-server.sh.
set -euo pipefail

SERVICES_ONLY=0
case "${1:-}" in
  "") ;;
  --services) SERVICES_ONLY=1 ;;
  *) echo "Usage: $0 [--services]" >&2; exit 2 ;;
esac
if (( $# > 1 )); then echo "Usage: $0 [--services]" >&2; exit 2; fi

# shellcheck source=scripts/lib/lan.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib/lan.sh"
deploy_target || exit 1
cd "$(dirname "${BASH_SOURCE[0]}")/.."

if (( SERVICES_ONLY )); then
  "${SSH[@]}" 'sudo test -f /etc/sentinela/apps-enabled' || { echo "!! services are not enabled on this server" >&2; exit 1; }
fi

VERSION="$(git describe --always --dirty 2>/dev/null || echo dev)"
if [[ "$VERSION" == *-dirty ]]; then
  echo "!! working tree has uncommitted changes; deploying them as ${VERSION}" >&2
fi

echo "==> build sentinela:${VERSION}"
docker build -q -t sentinela:local -t "sentinela:${VERSION}" --build-arg VERSION="${VERSION}" . >/dev/null

if (( ! SERVICES_ONLY )); then
  echo "==> build sentinela-detector"
  docker build -q -t sentinela-detector:local detector >/dev/null
fi

echo "==> ship deploy files"
# Root runs these files, so root owns them and only root can change them
# (not the SSH account): unpacked by root, without the archive's owners.
"${SSH[@]}" 'sudo install -d -o root -g root -m 0755 /opt/sentinela'
git ls-files -z deploy scripts | tar --null -T - -cf - | "${SSH[@]}" 'sudo tar --no-same-owner --no-same-permissions -m -x -C /opt/sentinela &&
  sudo chown -R root:root /opt/sentinela/deploy /opt/sentinela/scripts &&
  sudo chmod -R go-w /opt/sentinela/deploy /opt/sentinela/scripts && sudo chmod +x /opt/sentinela/scripts/*.sh'

echo "==> ship image ($(docker image inspect sentinela:local --format '{{.Size}}' | awk '{printf "%.0f MB", $1/1e6}'))"
docker save sentinela:local | gzip -1 | "${SSH[@]}" 'gunzip | docker load -q'

# The detector image is large (~500 MB) and rarely changes: ship it only
# when the server has a different build.
if (( ! SERVICES_ONLY )); then
  DET_ID="$(docker image inspect sentinela-detector:local --format '{{.Id}}')"
  if [[ "$("${SSH[@]}" "docker image inspect sentinela-detector:local --format '{{.Id}}' 2>/dev/null")" != "$DET_ID" ]]; then
    echo "==> ship detector image"
    docker save sentinela-detector:local | gzip -1 | "${SSH[@]}" 'gunzip | docker load -q'
  fi
fi

echo "==> restart stack"
"${SSH[@]}" 'sudo grep -q "^timezone:" /etc/sentinela/config.yaml' ||
  echo "!! /etc/sentinela/config.yaml has no timezone: schedules use UTC (add e.g. 'timezone: Europe/Lisbon')" >&2
# Camera streams for go2rtc come from config.yaml (any number, any vendor).
GO2RTC_CHANGED="$("${SSH[@]}" 'sudo /opt/sentinela/scripts/gen-go2rtc-cameras.sh')"
# The recorder writes only while /data holds its marker (volume_marker).
# /data belongs to uid 1000: root reaches it only through safe.sh, which
# follows no link and works through descriptors.
"${SSH[@]}" 'sudo bash -s' <<'REMOTE'
set -euo pipefail
source /opt/sentinela/scripts/lib/safe.sh
m=/data/.sentinela-volume
if mountpoint -q /data || [[ -e "$m" || -L "$m" ]]; then
  safe_touch /data "$m"
else
  echo "!! /data is not a mount point: recording stays paused until the disk is mounted" >&2
fi
safe_install_d /data 0750 /data/detector /data/apps-agent
REMOTE
# Host units behind System -> Atualizações (they do nothing until turned on).
"${SSH[@]}" 'sudo /opt/sentinela/scripts/setup-auto-update.sh install && sudo /opt/sentinela/scripts/setup-config-apply.sh && sudo /opt/sentinela/scripts/setup-tailscale.sh units && sudo /opt/sentinela/scripts/setup-cloudflared.sh units'
# The self-hosted services agent (ADR-0021) runs only while
# scripts/setup-apps.sh has left its marker; it joins the docker group.
# shellcheck disable=SC2016 # expanded on the server
APPS_ENV='SENTINELA_DOCKER_GID=$(getent group docker | cut -d: -f3) COMPOSE_PROFILES=$(sudo test -f /etc/sentinela/apps-enabled && echo apps)'
if (( SERVICES_ONLY )); then
  "${SSH[@]}" "cd /opt/sentinela/deploy && sudo SENTINELA_VERSION=${VERSION} ${APPS_ENV} docker compose up -d --no-build --no-deps sentinela apps"
else
  "${SSH[@]}" "cd /opt/sentinela/deploy && sudo docker compose pull -q go2rtc && sudo SENTINELA_VERSION=${VERSION} ${APPS_ENV} docker compose up -d --no-build --remove-orphans && docker image prune -f >/dev/null"
fi
if [[ "$GO2RTC_CHANGED" == changed ]]; then
  echo "==> camera list changed: restart go2rtc (live view only; recording continues)"
  "${SSH[@]}" 'cd /opt/sentinela/deploy && sudo docker compose restart go2rtc >/dev/null'
fi

echo "==> health"
for _ in $(seq 1 30); do
  if "${SSH[@]}" 'cd /opt/sentinela/deploy && sudo docker compose exec -T sentinela /usr/local/bin/sentinela healthcheck' >/dev/null 2>&1; then
    "${SSH[@]}" 'cd /opt/sentinela/deploy && sudo docker compose ps --format "table {{.Service}}\t{{.Status}}"'
    echo "Deployed ${VERSION}: http://${SENTINELA_HOST#*@}:8080"
    exit 0
  fi
  sleep 2
done
echo "!! health check failed; logs:" >&2
"${SSH[@]}" 'cd /opt/sentinela/deploy && sudo docker compose logs --tail=40' >&2
exit 1
