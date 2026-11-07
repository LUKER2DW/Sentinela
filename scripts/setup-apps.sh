#!/usr/bin/env bash
# Self-hosted services (ADR-0021). Run on the server:
#   sudo /opt/sentinela/scripts/setup-apps.sh enable|disable|status
# enable: state folder on the system disk, the token folder, the marker that
#   makes deploy.sh start the "apps" container, `apps: {enabled: true}` in
#   config.yaml (added if the section is missing), the services firewall
#   (DOCKER-USER rules + a path unit that follows the public ports), then
#   (re)starts sentinela and the agent.
# disable: stops every service and the agent, removes the firewall rules
#   and the marker. Services' files stay in /var/lib/sentinela-apps.
# Nothing here opens the router: a service on "Internet" also needs the
# owner to forward its port there.
set -euo pipefail
[[ $EUID -eq 0 ]] || { echo "run with sudo" >&2; exit 1; }
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MARK=/etc/sentinela/apps-enabled
CFG=/etc/sentinela/config.yaml
STATE=/var/lib/sentinela-apps
UNITS=(sentinela-apps-firewall.service sentinela-apps-firewall-reapply.service sentinela-apps-firewall.path)
compose() {
  (cd "$HERE/deploy" && SENTINELA_DOCKER_GID="$(getent group docker | cut -d: -f3)" COMPOSE_PROFILES=apps docker compose "$@")
}

case "${1:-status}" in
  enable)
    install -d -o 1000 -g 1000 -m 0750 "$STATE" /data/apps-agent
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
    systemctl enable --now sentinela-apps-firewall.service sentinela-apps-firewall.path >/dev/null
    # Config is read on process startup: an unchanged bind mount does not
    # make Compose restart the API after apps.enabled was changed.
    compose up -d --no-build --force-recreate --no-deps sentinela apps
    echo "enabled. Tailscale: allow tag:nvr:$(sed -n 's/^[[:space:]]*port_first:[[:space:]]*\([0-9]*\).*/\1/p' "$CFG" | head -1 | grep . || echo 20000)-$(sed -n 's/^[[:space:]]*port_last:[[:space:]]*\([0-9]*\).*/\1/p' "$CFG" | head -1 | grep . || echo 20099) in the ACL for devices that should open services."
    ;;
  disable)
    rm -f "$MARK"
    docker ps -q --filter label=sentinela.app | xargs -r docker stop >/dev/null
    compose stop apps >/dev/null 2>&1 || true
    compose rm -f apps >/dev/null 2>&1 || true
    systemctl disable --now sentinela-apps-firewall.path sentinela-apps-firewall.service >/dev/null 2>&1 || true
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
