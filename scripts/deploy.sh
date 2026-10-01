#!/usr/bin/env bash
# Deploy from a dev machine to the server over SSH. The only deploy path
# (ADR-0010): the server never builds or pulls code by itself.
#
#   ./scripts/deploy.sh                 # uses ana@192.168.1.10
#   SENTINELA_HOST=user@host ./scripts/deploy.sh
#
# Builds the image here (fast machine), streams it to the server with
# `docker save | ssh docker load`, ships deploy/ + scripts/ to /opt/sentinela,
# restarts the stack and waits for the health check. compose runs via sudo
# because it reads the root-only /etc/sentinela/secrets.env. The server never needs
# the source tree, Go or Node. First-time server setup: install-server.sh.
set -euo pipefail

HOST="${SENTINELA_HOST:-ana@192.168.1.10}"
KEY="${SENTINELA_SSH_KEY:-$HOME/.ssh/id_ed25519_sentinela}"
SSH=(ssh -i "$KEY" -o BatchMode=yes -o ConnectTimeout=10 "$HOST")
cd "$(dirname "${BASH_SOURCE[0]}")/.."

VERSION="$(git describe --always --dirty 2>/dev/null || echo dev)"
if [[ "$VERSION" == *-dirty ]]; then
  echo "!! working tree has uncommitted changes; deploying them as ${VERSION}" >&2
fi

echo "==> build sentinela:${VERSION}"
docker build -q -t sentinela:local -t "sentinela:${VERSION}" --build-arg VERSION="${VERSION}" . >/dev/null

echo "==> build sentinela-detector"
docker build -q -t sentinela-detector:local detector >/dev/null

echo "==> ship deploy files"
# shellcheck disable=SC2016 # $USER expands on the server
"${SSH[@]}" 'sudo install -d -o "$USER" -g "$USER" /opt/sentinela'
git ls-files -z deploy scripts | tar --null -T - -cf - | "${SSH[@]}" 'tar -m -x -C /opt/sentinela && chmod +x /opt/sentinela/scripts/*.sh'

echo "==> ship image ($(docker image inspect sentinela:local --format '{{.Size}}' | awk '{printf "%.0f MB", $1/1e6}'))"
docker save sentinela:local | gzip -1 | "${SSH[@]}" 'gunzip | docker load -q'

# The detector image is large (~500 MB) and rarely changes: ship it only
# when the server has a different build.
DET_ID="$(docker image inspect sentinela-detector:local --format '{{.Id}}')"
if [[ "$("${SSH[@]}" "docker image inspect sentinela-detector:local --format '{{.Id}}' 2>/dev/null")" != "$DET_ID" ]]; then
  echo "==> ship detector image"
  docker save sentinela-detector:local | gzip -1 | "${SSH[@]}" 'gunzip | docker load -q'
fi

echo "==> restart stack"
"${SSH[@]}" 'sudo install -d -o 1000 -g 1000 -m 0750 /data/detector /data/apps-agent'
# go2rtc mounts the extra-cameras file: it must exist (else docker makes a directory).
"${SSH[@]}" 'sudo test -f /etc/sentinela/go2rtc-cameras.yaml || echo "# cameras added by scripts/add-camera.sh" | sudo install -m 0644 /dev/stdin /etc/sentinela/go2rtc-cameras.yaml'
# The self-hosted services agent (ADR-0021) runs only while
# scripts/setup-apps.sh has left its marker; it joins the docker group.
# shellcheck disable=SC2016 # expanded on the server
APPS_ENV='SENTINELA_DOCKER_GID=$(getent group docker | cut -d: -f3) COMPOSE_PROFILES=$(test -f /etc/sentinela/apps-enabled && echo apps)'
"${SSH[@]}" "cd /opt/sentinela/deploy && sudo docker compose pull -q go2rtc && sudo SENTINELA_VERSION=${VERSION} ${APPS_ENV} docker compose up -d --no-build --remove-orphans && docker image prune -f >/dev/null"

echo "==> health"
for _ in $(seq 1 30); do
  if "${SSH[@]}" 'cd /opt/sentinela/deploy && sudo docker compose exec -T sentinela /usr/local/bin/sentinela healthcheck' >/dev/null 2>&1; then
    "${SSH[@]}" 'cd /opt/sentinela/deploy && sudo docker compose ps --format "table {{.Service}}\t{{.Status}}"'
    echo "Deployed ${VERSION}: http://${HOST#*@}:8080"
    exit 0
  fi
  sleep 2
done
echo "!! health check failed; logs:" >&2
"${SSH[@]}" 'cd /opt/sentinela/deploy && sudo docker compose logs --tail=40' >&2
exit 1
