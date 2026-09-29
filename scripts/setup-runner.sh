#!/usr/bin/env bash
# Install the GitHub Actions self-hosted runner that deploys on push (ADR-0009).
# The one-time registration token is read from stdin, never from arguments or
# files, so it stays out of shell history. From the dev PC:
#
#   gh api -X POST repos/LUKER2DW/Sentinela/actions/runners/registration-token --jq .token \
#     | ssh -i ~/.ssh/id_ed25519_sentinela ana@192.168.1.10 'sudo /opt/sentinela/scripts/setup-runner.sh'
#
# Needs setup-autodeploy.sh first (deploy key, source clone, deployer).
set -euo pipefail
[[ $EUID -eq 0 ]] || { echo "run with sudo" >&2; exit 1; }

REPO_URL="${SENTINELA_REPO_URL:-https://github.com/LUKER2DW/Sentinela}"
VER=2.337.0
SHA=70920811a4f8ad4328818682bca5c6469c1c942fab52448868071d0063816613 # from the release notes
DIR=/opt/actions-runner
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

read -r TOKEN || true
[[ -n "${TOKEN:-}" ]] || { echo "pipe the registration token on stdin (see the header)" >&2; exit 1; }
[[ -x /usr/local/sbin/sentinela-autodeploy ]] || { echo "run setup-autodeploy.sh first" >&2; exit 1; }

# The deployer from this checkout (adds --wait for the runner).
install -m 0755 "$HERE/scripts/autodeploy.sh" /usr/local/sbin/sentinela-autodeploy
install -m 0644 "$HERE/deploy/host/sentinela-autodeploy.timer" /etc/systemd/system/
systemctl daemon-reload
systemctl restart sentinela-autodeploy.timer

# Unprivileged runner account: no docker group, no shell, one sudo rule.
id gh-runner >/dev/null 2>&1 || useradd --system --create-home --home-dir "$DIR" --shell /usr/sbin/nologin gh-runner
cat >/etc/sudoers.d/91-gh-runner <<'EOF'
gh-runner ALL=(root) NOPASSWD: /usr/local/sbin/sentinela-autodeploy --wait
EOF
chmod 0440 /etc/sudoers.d/91-gh-runner
visudo -cf /etc/sudoers.d/91-gh-runner >/dev/null

cd "$DIR"
if [[ ! -x ./config.sh ]]; then
  curl -fsSL -o runner.tgz "https://github.com/actions/runner/releases/download/v${VER}/actions-runner-linux-x64-${VER}.tar.gz"
  echo "${SHA}  runner.tgz" | sha256sum -c --quiet -
  tar xzf runner.tgz && rm runner.tgz
  ./bin/installdependencies.sh >/dev/null
  chown -R gh-runner: "$DIR"
fi

if [[ -f .runner ]]; then
  ./svc.sh stop >/dev/null 2>&1 || true
  ./svc.sh uninstall >/dev/null 2>&1 || true
fi
sudo -u gh-runner ./config.sh --unattended --replace --url "$REPO_URL" --token "$TOKEN" \
  --name sentinela --labels sentinela --work _work --disableupdate >/dev/null
./svc.sh install gh-runner >/dev/null
./svc.sh start >/dev/null
echo "Runner online. Pushes to main now deploy immediately (see the repo's Actions tab)."
