#!/usr/bin/env bash
# Install / upgrade Sentinela on Ubuntu 22.04+ (also Debian 12/13).
#
#   git clone <repo> /opt/sentinela && cd /opt/sentinela
#   sudo ./scripts/install-server.sh
#
# Idempotent: re-run after `git pull` to rebuild and restart. It never
# overwrites an existing /etc/sentinela/config.yaml or secrets.env.
# The camera password is read silently and only written to secrets.env (0600).
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ETC=/etc/sentinela
DATA="${SENTINELA_DATA:-/data}"
UID_GID=1000:1000

say()  { printf '\n\033[1;33m==> %s\033[0m\n' "$*"; }
warn() { printf '\033[1;31m!! %s\033[0m\n' "$*" >&2; }

[[ $EUID -eq 0 ]] || { warn "run with sudo"; exit 1; }
# shellcheck source=/dev/null
. /etc/os-release
case "${ID}" in ubuntu|debian) ;; *) warn "untested distro ${ID}; continuing" ;; esac

say "1/6 Docker Engine + compose plugin"
if ! command -v docker >/dev/null || ! docker compose version >/dev/null 2>&1; then
  apt-get update -qq
  apt-get install -y -qq ca-certificates curl gnupg
  install -m 0755 -d /etc/apt/keyrings
  curl -fsSL "https://download.docker.com/linux/${ID}/gpg" -o /etc/apt/keyrings/docker.asc
  chmod a+r /etc/apt/keyrings/docker.asc
  echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/${ID} ${VERSION_CODENAME} stable" \
    > /etc/apt/sources.list.d/docker.list
  apt-get update -qq
  apt-get install -y -qq docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
  systemctl enable --now docker
else
  echo "docker $(docker --version | awk '{print $3}') already installed"
fi

say "2/6 Configuration in ${ETC}"
install -d -m 0750 "${ETC}"
if [[ ! -f "${ETC}/config.yaml" ]]; then
  install -m 0644 "${REPO_DIR}/deploy/config.example.yaml" "${ETC}/config.yaml"
  sed -i "s#^data_dir: .*#data_dir: /data#" "${ETC}/config.yaml"
  echo "created ${ETC}/config.yaml (edit camera name/host there if needed)"
else
  echo "keeping existing ${ETC}/config.yaml"
fi
if [[ ! -f "${ETC}/secrets.env" ]]; then
  read -r -p "Camera username [admin]: " CAM_USER; CAM_USER="${CAM_USER:-admin}"
  while :; do
    read -r -s -p "Camera password (not shown): " CAM_PASS; echo
    [[ -n "${CAM_PASS}" ]] || { warn "empty password"; continue; }
    [[ "${CAM_PASS}" != *[\&\?/\ \#%]* ]] || { warn "the camera's RTSP URL cannot carry & ? / # % or spaces; change the camera password first"; exit 1; }
    break
  done
  umask 077
  printf 'CAM1_USER=%s\nCAM1_PASS=%s\n' "${CAM_USER}" "${CAM_PASS}" > "${ETC}/secrets.env"
  unset CAM_PASS
  chmod 0600 "${ETC}/secrets.env"; chown root:root "${ETC}/secrets.env"
  echo "created ${ETC}/secrets.env (0600)"
else
  echo "keeping existing ${ETC}/secrets.env"
fi

say "3/6 Data directory ${DATA}"
install -d -m 0750 "${DATA}" "${DATA}/recordings" "${DATA}/db" "${DATA}/backup"
chown -R "${UID_GID}" "${DATA}"
if [[ "$(df --output=source "${DATA}" | tail -1)" == "$(df --output=source / | tail -1)" ]]; then
  warn "${DATA} is on the same filesystem as / . Recommended: a dedicated disk/partition mounted at ${DATA} (ext4, noatime)."
fi
df -h "${DATA}" | tail -1

say "4/6 Kernel write-back tuning for a single HDD"
cat > /etc/sysctl.d/90-sentinela.conf <<'EOF'
# Smooth continuous video writes on a spinning disk (docs/ARCHITECTURE.md §6).
vm.dirty_background_bytes = 16777216
vm.dirty_bytes = 67108864
EOF
sysctl -q --system || true

say "5/6 Build and start containers"
cd "${REPO_DIR}/deploy"
SENTINELA_VERSION="$(git -C "${REPO_DIR}" describe --always --dirty 2>/dev/null || echo dev)"
export SENTINELA_VERSION
docker compose build --pull
docker compose up -d --remove-orphans
docker image prune -f >/dev/null

say "6/6 Health"
for _ in $(seq 1 30); do
  if docker compose exec -T sentinela /usr/local/bin/sentinela healthcheck >/dev/null 2>&1; then break; fi
  sleep 2
done
docker compose ps
IP="$(hostname -I | awk '{print $1}')"
cat <<EOF

Sentinela is running.
  Open        http://${IP}:8080  (first visit creates the admin account)
  Logs        cd ${REPO_DIR}/deploy && sudo docker compose logs -f --tail=50
  Update      cd ${REPO_DIR} && git pull && sudo ./scripts/install-server.sh
  Reset pw    cd ${REPO_DIR}/deploy && sudo docker compose exec -T sentinela sentinela user passwd <user>

Firewall (recommended, keep ssh open first!):
  sudo ufw allow from 192.168.0.0/16 to any port 22 proto tcp
  sudo ufw allow from 192.168.0.0/16 to any port 8080 proto tcp
  sudo ufw allow from 192.168.0.0/16 to any port 8555
  sudo ufw enable
Do NOT forward any router port to this server (docs/SECURITY.md).
EOF
