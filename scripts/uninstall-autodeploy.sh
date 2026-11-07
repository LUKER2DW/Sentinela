#!/usr/bin/env bash
# Remove the retired auto deployer (ADR-0009 → ADR-0010) from the server.
# Idempotent. Run on the server:  sudo /opt/sentinela/scripts/uninstall-autodeploy.sh
# Afterwards revoke the deploy key in GitHub → Settings → Deploy keys.
set -euo pipefail
[[ $EUID -eq 0 ]] || { echo "run with sudo" >&2; exit 1; }

systemctl disable --now sentinela-autodeploy.timer sentinela-autodeploy.service 2>/dev/null || true
rm -f /etc/systemd/system/sentinela-autodeploy.{service,timer}
systemctl daemon-reload

# Self-hosted runner (only if it was ever registered).
if [[ -d /opt/actions-runner ]]; then
  (cd /opt/actions-runner && ./svc.sh stop >/dev/null 2>&1; ./svc.sh uninstall >/dev/null 2>&1) || true
  rm -rf /opt/actions-runner
fi
id gh-runner >/dev/null 2>&1 && userdel gh-runner
rm -f /etc/sudoers.d/91-gh-runner

rm -f /usr/local/sbin/sentinela-autodeploy /run/sentinela-autodeploy.lock \
  /var/lib/sentinela/autodeploy.state \
  /etc/sentinela/deploy_key /etc/sentinela/deploy_key.pub /etc/sentinela/known_hosts
rm -rf /opt/sentinela/src
docker image rm sentinela:previous >/dev/null 2>&1 || true
echo "Auto deploy removed. Revoke the 'sentinela-autodeploy' deploy key on GitHub."
