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

say()  { printf '\n\033[1;33m==> %s\033[0m\n' "$*"; }
warn() { printf '\033[1;31m!! %s\033[0m\n' "$*" >&2; }

[[ $EUID -eq 0 ]] || { warn "run with sudo"; exit 1; }
# shellcheck source=/dev/null
. /etc/os-release
case "${ID}" in ubuntu|debian) ;; *) warn "untested distro ${ID}; continuing" ;; esac

say "1/6 Docker Engine + compose plugin"
export DEBIAN_FRONTEND=noninteractive
if ! command -v docker >/dev/null || ! docker compose version >/dev/null 2>&1; then
  apt-get -o DPkg::Lock::Timeout=600 update -qq
  apt-get -o DPkg::Lock::Timeout=600 install -y -qq ca-certificates curl gnupg
  install -m 0755 -d /etc/apt/keyrings
  curl -fsSL "https://download.docker.com/linux/${ID}/gpg" -o /etc/apt/keyrings/docker.asc
  chmod a+r /etc/apt/keyrings/docker.asc
  echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/${ID} ${VERSION_CODENAME} stable" \
    > /etc/apt/sources.list.d/docker.list
  apt-get -o DPkg::Lock::Timeout=600 update -qq
  apt-get -o DPkg::Lock::Timeout=600 install -y -qq docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
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

[[ -f "${ETC}/go2rtc-cameras.yaml" ]] || echo "# cameras added by scripts/add-camera.sh" | install -m 0644 /dev/stdin "${ETC}/go2rtc-cameras.yaml"

say "3/6 Data directory ${DATA}"
if ! mountpoint -q "${DATA}"; then
  warn "${DATA} is not a mount point: recordings would land on the system disk."
  warn "Mount the recordings disk at ${DATA} first (docs/ARCHITECTURE.md §6), or set SENTINELA_ALLOW_ROOT_DATA=1."
  [[ "${SENTINELA_ALLOW_ROOT_DATA:-0}" == 1 ]] || exit 1
fi
install -d -m 0750 -o 1000 -g 1000 "${DATA}/recordings" "${DATA}/db" "${DATA}/backup" "${DATA}/detector"
chown 1000:1000 "${DATA}"   # top level only: never walk every recording
df -h "${DATA}" | tail -1

say "4/6 Kernel, firewall and LAN file server"
printf '%s\n' '# Smooth continuous video writes on a spinning disk (docs/ARCHITECTURE.md §6).' \
  'vm.dirty_background_bytes = 16777216' 'vm.dirty_bytes = 67108864' > /etc/sysctl.d/90-sentinela.conf
sysctl -q --system || true
# LAN time server: the camera loses internet NTP once its WAN is blocked
# (docs/CAMERA.md §4); point its NTP at this server.
apt-get -o DPkg::Lock::Timeout=600 install -y -qq chrony >/dev/null
printf '%s
' '# Serve time to the LAN (camera NTP -> this server).' 'allow 192.168.1.0/24' > /etc/chrony/conf.d/lan.conf
systemctl restart chrony
# Hourly SMART snapshot shown on the System page.
install -d -m 0755 /var/lib/sentinela
install -m 0644 "${REPO_DIR}/deploy/host/sentinela-disk-health.service" "${REPO_DIR}/deploy/host/sentinela-disk-health.timer" /etc/systemd/system/
systemctl daemon-reload
systemctl enable -q --now sentinela-disk-health.timer
systemctl start sentinela-disk-health.service || true
"${REPO_DIR}/scripts/firewall.sh" >/dev/null && echo "firewall: LAN-only rules applied"
if mountpoint -q /srv/nas; then "${REPO_DIR}/scripts/setup-nas.sh"; else echo "no /srv/nas mount: skipping the LAN file server"; fi
# Nightly DB/config backup + restic (ADR-0014) needs its own disk at /srv/backup.
if grep -qE '[[:space:]]/srv/(backup|nas/Extra)[[:space:]]' /etc/fstab; then "${REPO_DIR}/scripts/setup-backup.sh"; else echo "no /srv/backup disk in fstab: skipping backups"; fi

say "5/6 Start containers"
cd "${REPO_DIR}/deploy"
SENTINELA_VERSION="$(git -C "${REPO_DIR}" describe --always --dirty 2>/dev/null || echo dev)"
export SENTINELA_VERSION
docker compose pull -q go2rtc
if docker image inspect sentinela:local >/dev/null 2>&1 && [[ "${SENTINELA_BUILD:-0}" != 1 ]]; then
  echo "using existing image sentinela:local (SENTINELA_BUILD=1 rebuilds here)"
  docker image inspect sentinela-detector:local >/dev/null 2>&1 || docker compose build detector
  docker compose up -d --no-build --remove-orphans
else
  docker compose up -d --build --remove-orphans
fi
docker image prune -f >/dev/null

say "6/6 Health"
for _ in $(seq 1 30); do
  if docker compose exec -T sentinela /usr/local/bin/sentinela healthcheck >/dev/null 2>&1; then break; fi
  sleep 2
done
docker compose ps
IP="$(hostname -I | awk '{print $1}')"
echo
echo "Sentinela is running."
echo "  Open        http://${IP}:8080  (first visit creates the admin account)"
printf '  Nuvem       \\\\%s\\Nuvem  (set its password once: sudo smbpasswd -a ana)\n' "${IP}"
echo "  Logs        cd ${REPO_DIR}/deploy && sudo docker compose logs -f --tail=50"
echo "  Update      from the dev PC: ./scripts/deploy.sh"
echo "  Reset pw    cd ${REPO_DIR}/deploy && sudo docker compose exec -T sentinela sentinela user passwd <user>"
echo "Do NOT forward any router port to this server (docs/SECURITY.md)."
