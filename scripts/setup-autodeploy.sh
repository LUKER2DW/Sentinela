#!/usr/bin/env bash
# One-time setup of the auto deployer (ADR-0009). Run on the server:
#   sudo /opt/sentinela/scripts/setup-autodeploy.sh [git@github.com:owner/repo.git]
#
# Creates a read-only deploy key (/etc/sentinela/deploy_key), prints its
# public half to add on GitHub (Settings → Deploy keys, no write access),
# clones the repo to /opt/sentinela/src and enables the 2-minute timer.
set -euo pipefail
[[ $EUID -eq 0 ]] || { echo "run with sudo" >&2; exit 1; }

REPO="${1:-git@github.com:LUKER2DW/Sentinela.git}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
KEY=/etc/sentinela/deploy_key
export GIT_SSH_COMMAND="ssh -i $KEY -o IdentitiesOnly=yes -o UserKnownHostsFile=/etc/sentinela/known_hosts -o StrictHostKeyChecking=accept-new"

command -v git >/dev/null || apt-get install -y -o DPkg::Lock::Timeout=600 git
install -d -m 0750 /etc/sentinela /var/lib/sentinela
if [[ ! -f $KEY ]]; then
  ssh-keygen -q -t ed25519 -N "" -C "sentinela-autodeploy@$(hostname)" -f "$KEY"
fi
chmod 0600 "$KEY"

if ! git ls-remote "$REPO" HEAD >/dev/null 2>&1; then
  echo "Add this public key to the repository as a read-only deploy key, then run again:"
  cat "$KEY.pub"
  exit 2
fi

if [[ ! -d /opt/sentinela/src/.git ]]; then
  git clone --quiet "$REPO" /opt/sentinela/src
fi

install -m 0755 "$HERE/scripts/autodeploy.sh" /usr/local/sbin/sentinela-autodeploy
install -m 0644 "$HERE/deploy/host/sentinela-autodeploy.service" "$HERE/deploy/host/sentinela-autodeploy.timer" /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now sentinela-autodeploy.timer
echo "Auto deploy on. Follow it with: journalctl -u sentinela-autodeploy -f"
