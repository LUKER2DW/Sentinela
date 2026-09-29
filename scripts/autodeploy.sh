#!/usr/bin/env bash
# Auto deploy (ADR-0009). Run as root by the GitHub Actions self-hosted runner
# on every push (`--wait`, via a single sudoers rule) and by a slow fallback
# timer (sentinela-autodeploy.timer) in case the runner is down.
#
# Polls GitHub; when the branch head changes it builds the image on this
# server, ships deploy/ + scripts/ to /opt/sentinela, restarts the stack and
# waits for the health check. If the new version is unhealthy it rolls back
# to the previous image. Each commit is tried once: a failed commit is not
# rebuilt every poll, the next push is.
#
# Installed copy: /usr/local/sbin/sentinela-autodeploy (setup-autodeploy.sh).
set -euo pipefail

SRC=/opt/sentinela/src
APP=/opt/sentinela
BRANCH="${SENTINELA_BRANCH:-main}"
STATE=/var/lib/sentinela/autodeploy.state
KEY=/etc/sentinela/deploy_key
export GIT_SSH_COMMAND="ssh -i $KEY -o IdentitiesOnly=yes -o UserKnownHostsFile=/etc/sentinela/known_hosts -o StrictHostKeyChecking=accept-new -o ConnectTimeout=15"

exec 9>/run/sentinela-autodeploy.lock
if [[ "${1:-}" == "--wait" ]]; then
  flock 9 # runner: wait for a timer run in progress, then check again
else
  flock -n 9 || exit 0
fi

log() { echo "autodeploy: $*"; }
compose() { (cd "$APP/deploy" && docker compose "$@"); }

healthy() {
  for _ in $(seq 1 45); do
    if compose exec -T sentinela /usr/local/bin/sentinela healthcheck >/dev/null 2>&1; then return 0; fi
    sleep 2
  done
  return 1
}

git -C "$SRC" fetch --quiet origin "$BRANCH"
new="$(git -C "$SRC" rev-parse "origin/$BRANCH")"
last="$(cat "$STATE" 2>/dev/null || true)"
[[ "$new" == "$last" ]] && exit 0

short="${new:0:7}"
log "new commit $short on $BRANCH (last handled: ${last:0:7})"
# Mark it handled first: a broken commit is tried once, not every 2 minutes.
echo "$new" >"$STATE"

git -C "$SRC" -c advice.detachedHead=false checkout --quiet --force "$new"
git -C "$SRC" clean -qfdx

log "building sentinela:$short (recording keeps running)"
if ! docker build -q -t "sentinela:$short" --build-arg VERSION="$short" "$SRC" >/dev/null; then
  log "build failed for $short; still running the previous version"
  exit 1
fi

log "shipping deploy files"
install -d "$APP"
git -C "$SRC" ls-files -z deploy scripts | (cd "$SRC" && tar --null -T - -cf -) | tar -m -x -C "$APP"
chmod +x "$APP"/scripts/*.sh
owner="$(stat -c %U "$APP")"
chown -R "$owner:" "$APP/deploy" "$APP/scripts"

if docker image inspect sentinela:local >/dev/null 2>&1; then
  docker tag sentinela:local sentinela:previous
fi
docker tag "sentinela:$short" sentinela:local

log "restarting stack"
compose pull -q go2rtc || true
SENTINELA_VERSION="$short" compose up -d --no-build --remove-orphans

if healthy; then
  log "deployed $short"
  docker image prune -f >/dev/null
  # Keep only the running and the previous version.
  docker images --format '{{.Repository}}:{{.Tag}}' sentinela |
    grep -vE ':(local|previous)$' | grep -v ":$short$" | xargs -r docker rmi -f >/dev/null 2>&1 || true
  exit 0
fi

log "health check failed for $short; rolling back"
compose logs --tail=40 sentinela || true
if docker image inspect sentinela:previous >/dev/null 2>&1; then
  docker tag sentinela:previous sentinela:local
  compose up -d --no-build
  healthy && log "rolled back to the previous version" || log "rollback also unhealthy; check the server"
fi
exit 1
