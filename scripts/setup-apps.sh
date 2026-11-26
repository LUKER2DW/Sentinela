#!/usr/bin/env bash
# Self-hosted services (ADR-0021). Run on the server:
#   sudo /opt/sentinela/scripts/setup-apps.sh enable|disable|status
# enable: state folder on the system disk, the token folder, the marker that
#   makes deploy.sh start the "apps" container, `apps: {enabled: true}` in
#   config.yaml (added if the section is missing), the services firewall
#   (DOCKER-USER rules + a path unit that follows the public ports; applied
#   again now, so a run after an update installs the current units), then
#   (re)starts sentinela and the agent. The agent starts no service until
#   the firewall has verified its rules (scripts/apps-firewall.sh).
# disable: closes that start gate, stops the agent, then every service, and
#   removes the firewall rules only once no service container runs.
#   Services' files stay in /var/lib/sentinela-apps.
# Nothing here opens the router: a service on "Internet" also needs the
# owner to forward its port there.
set -euo pipefail
[[ $EUID -eq 0 ]] || { echo "run with sudo" >&2; exit 1; }
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/lib/safe.sh
source "$HERE/scripts/lib/safe.sh"
MARK=/etc/sentinela/apps-enabled
CFG=/etc/sentinela/config.yaml
STATE=/var/lib/sentinela-apps
UNITS=(sentinela-apps-firewall.service sentinela-apps-firewall-reapply.service sentinela-apps-firewall.path
  sentinela-apps-firewall-check.service sentinela-apps-firewall-check.timer)
compose() {
  (cd "$HERE/deploy" && SENTINELA_DOCKER_GID="$(getent group docker | cut -d: -f3)" COMPOSE_PROFILES=apps docker compose "$@")
}

case "${1:-status}" in
  enable)
    install -d -o 1000 -g 1000 -m 0750 "$STATE"
    safe_install_d /data 0750 /data/apps-agent
    touch "$MARK"
    if ! grep -q '^apps:' "$CFG"; then
      cp -a "$CFG" "$CFG.bak.$(date +%Y%m%d%H%M%S)"
      printf '\n# Self-hosted services (ADR-0021), added by setup-apps.sh.\napps:\n  enabled: true\n' >> "$CFG"
      echo "config: apps section added to $CFG"
    elif ! sed -n '/^apps:/,/^[^[:space:]#]/p' "$CFG" | grep -q '^[[:space:]]*enabled:[[:space:]]*true'; then
      echo "config: set apps.enabled: true in $CFG, then run this again" >&2
      exit 1
    fi
    for u in "${UNITS[@]}"; do install -m 0644 "$HERE/deploy/host/$u" /etc/systemd/system/; done
    systemctl daemon-reload
    systemctl enable sentinela-apps-firewall.service sentinela-apps-firewall.path >/dev/null
    systemctl restart sentinela-apps-firewall.service
    systemctl start sentinela-apps-firewall.path
    systemctl enable --now sentinela-apps-firewall-check.timer >/dev/null
    # Config is read on process startup: an unchanged bind mount does not
    # make Compose restart the API after apps.enabled was changed.
    compose up -d --no-build --force-recreate --no-deps sentinela apps
    echo "enabled. Tailscale: allow tag:nvr:$(sed -n 's/^[[:space:]]*port_first:[[:space:]]*\([0-9]*\).*/\1/p' "$CFG" | head -1 | grep . || echo 20000)-$(sed -n 's/^[[:space:]]*port_last:[[:space:]]*\([0-9]*\).*/\1/p' "$CFG" | head -1 | grep . || echo 20099) in the ACL for devices that should open services."
    ;;
  disable)
    rm -f "$MARK"
    # 1. The agent starts nothing without the firewall's marker: from here
    #    on it cannot restart a service it sees stopped.
    "$HERE/scripts/apps-firewall.sh" unready
    # 2. The supervisor goes before the services (it never stops them
    #    itself), so nothing restarts them while they stop.
    compose stop apps >/dev/null
    compose rm -f apps >/dev/null
    if [[ -n "$(compose ps -q apps 2>/dev/null)" ]]; then
      echo "the apps agent is still running: firewall kept; run disable again" >&2
      exit 1
    fi
    # 3. Every service and helper container, until none runs (they have no
    #    restart policy, so they stay stopped).
    for _ in 1 2 3; do
      ids="$(docker ps -q --filter label=sentinela.app)"
      [[ -n "$ids" ]] || break
      xargs -r docker stop <<<"$ids" >/dev/null || true
    done
    if [[ -n "$(docker ps -q --filter label=sentinela.app)" ]]; then
      echo "service containers are still running: firewall kept; stop them (docker ps --filter label=sentinela.app) and run disable again" >&2
      exit 1
    fi
    # 4. Only now the rules.
    systemctl disable --now sentinela-apps-firewall.path sentinela-apps-firewall-check.timer sentinela-apps-firewall.service >/dev/null 2>&1 || true
    # A re-apply the agent queued before it stopped must not put them back.
    systemctl stop sentinela-apps-firewall-reapply.service >/dev/null 2>&1 || true
    "$HERE/scripts/apps-firewall.sh" remove
    echo "disabled. Set apps.enabled: false in $CFG and redeploy to hide the page."
    ;;
  status) ;;
  *) echo "usage: $0 enable|disable|status" >&2; exit 2 ;;
esac

[[ -f "$MARK" ]] && echo "marker: on" || echo "marker: off"
docker ps --filter name=sentinela-apps-1 --format 'agent: {{.Status}}' | grep . || echo "agent: not running"
ss -tln | grep -q '127.0.0.1:7091 ' && echo "agent API: listening on 127.0.0.1:7091" || echo "agent API: not listening"
echo "services: $(docker ps -q --filter label=sentinela.app | wc -l) container(s) running"
"$HERE/scripts/apps-firewall.sh" status | sed -n '1,3p'
du -sh "$STATE" 2>/dev/null | sed 's/^/state: /' || true
