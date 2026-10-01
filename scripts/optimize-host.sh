#!/usr/bin/env bash
# Trim the Ubuntu server to what the NVR needs and make it come back by
# itself after a power cut, a crash or a hang. Idempotent. On the server:
#   sudo /opt/sentinela/scripts/optimize-host.sh
#
# Kept: docker/containerd, tailscaled, ssh, ufw, fail2ban, chrony, Samba
# (smbd/nmbd), wsdd, avahi, smartmontools, cron, rsyslog, unattended-upgrades
# and the sentinela-* timers.
# Removed (bare-metal NVR, never used): snapd + LXD, cloud-init, VMware
# tools, multipath/iSCSI, fwupd, apport/popularity-contest, udisks2, certbot
# (Tailscale issues the certificates); leftovers of earlier projects
# (2026-10-01 review): ansible, nginx, npm/Node, OpenCV/NumPy, VA-API dev
# tools, flashrom libs, cloud-image/Landscape tools, mdadm and LVM (no RAID
# or LVM here). Everything Sentinela runs is in containers. Masked:
# packagekit, GPU manager, MOTD news, update-notifier and timers that do
# nothing here (fstrim on HDDs, e2scrub without LVM, man-db).
set -euo pipefail
[[ $EUID -eq 0 ]] || { echo "run with sudo" >&2; exit 1; }
export DEBIAN_FRONTEND=noninteractive
APT=(apt-get -o DPkg::Lock::Timeout=600 -y -qq)

echo "==> keep what the ubuntu-server metapackages pull in"
# Purging some packages below also removes the ubuntu-server(-minimal)
# metapackages; marking their other dependencies manual stops a later
# autoremove from taking ssh, netplan, etc. with them.
for meta in ubuntu-server ubuntu-server-minimal ubuntu-minimal ubuntu-standard; do
  dpkg -s "$meta" >/dev/null 2>&1 || continue
  apt-cache depends --installed --important "$meta" 2>/dev/null |
    awk '/Depends:|Recommends:/ {print $2}' | grep -v '^<' | xargs -r apt-mark manual >/dev/null || true
done

echo "==> remove unused services"
if command -v snap >/dev/null; then
  for s in lxd core20 core22 snapd; do snap remove --purge "$s" >/dev/null 2>&1 || true; done
fi
purge=()
for p in snapd lxd-agent-loader cloud-init open-vm-tools multipath-tools open-iscsi fwupd fwupd-signed \
  apport apport-symptoms popularity-contest udisks2 certbot python3-certbot python3-certbot-dns-cloudflare \
  ansible-core nginx nginx-common npm python3-opencv python3-numpy python3-pip libva-dev vainfo libflashrom1 \
  libftdi1-2 docker bcache-tools cloud-guest-utils cloud-initramfs-copymods cloud-initramfs-dyn-netconf \
  overlayroot landscape-common pollinate sosreport byobu ubuntu-drivers-common mdadm lvm2; do
  dpkg -s "$p" >/dev/null 2>&1 && purge+=("$p")
done
# Tools these removals would otherwise take along, still wanted (disk work).
apt-mark manual fdisk gdisk bc finalrd >/dev/null 2>&1 || true
((${#purge[@]})) && "${APT[@]}" purge "${purge[@]}" >/dev/null
# Obsolete Docker packages can delete the active Engine's shared state in
# their purge hooks. Keep their residual configuration when Docker CE is
# installed; "rc" packages contain no running service or installed binary.
dpkg -l | awk '/^rc/ && $2 !~ /^(docker.io|containerd)$/ {print $2}' |
  xargs -r "${APT[@]}" purge >/dev/null
rm -rf /snap /var/snap /var/lib/snapd /root/snap /home/*/snap
# Keep the netplan file cloud-init wrote (it is the network config).
for u in packagekit.service gpu-manager.service motd-news.timer update-notifier-download.timer \
  update-notifier-motd.timer pollinate.service fstrim.timer e2scrub_all.timer e2scrub_reap.service \
  man-db.timer ua-timer.timer thermald.service; do
  systemctl disable --now "$u" >/dev/null 2>&1 || true
  systemctl mask "$u" >/dev/null 2>&1 || true
done

echo "==> updates"
"${APT[@]}" update
"${APT[@]}" full-upgrade >/dev/null
"${APT[@]}" autoremove --purge >/dev/null
"${APT[@]}" clean

echo "==> start in the right order after a reboot"
# /data (recordings, DB) and /srv/nas are separate disks mounted `nofail`.
# Without this, a slow or failed disk would let the recorder write into the
# system disk and the API create an empty database: better not to start.
install -d /etc/systemd/system/docker.service.d /etc/systemd/system/smbd.service.d
cat >/etc/systemd/system/docker.service.d/sentinela-mounts.conf <<'EOF'
# Sentinela: containers bind-mount these disks (scripts/optimize-host.sh).
[Unit]
RequiresMountsFor=/data /srv/nas
EOF
cat >/etc/systemd/system/smbd.service.d/sentinela-mounts.conf <<'EOF'
[Unit]
RequiresMountsFor=/srv/nas /data
EOF

echo "==> recover from hangs and kernel panics"
# Intel TCO hardware watchdog (H55): Ubuntu blacklists it, and
# modules-load.d honours the blacklist, so an early unit loads it explicitly.
# systemd opens /dev/watchdog as soon as it appears and pets it; if the
# kernel or PID 1 freezes, the board resets itself.
rm -f /etc/modules-load.d/sentinela-watchdog.conf
cat >/etc/systemd/system/sentinela-watchdog.service <<'EOF'
[Unit]
Description=Load the hardware watchdog driver (Sentinela)
DefaultDependencies=no
Before=sysinit.target

[Service]
Type=oneshot
ExecStart=-/sbin/modprobe iTCO_wdt

[Install]
WantedBy=sysinit.target
EOF
systemctl daemon-reload
systemctl enable sentinela-watchdog.service >/dev/null
modprobe iTCO_wdt 2>/dev/null || true
install -d /etc/systemd/system.conf.d
cat >/etc/systemd/system.conf.d/sentinela-watchdog.conf <<'EOF'
[Manager]
RuntimeWatchdogSec=30s
RebootWatchdogSec=5min
EOF
cat >/etc/sysctl.d/90-sentinela.conf <<'EOF'
# Reboot 10 s after a kernel panic or oops instead of hanging forever.
kernel.panic = 10
kernel.panic_on_oops = 1
vm.swappiness = 10
EOF
sysctl -q --system

echo "==> bounded logs"
install -d /etc/systemd/journald.conf.d
cat >/etc/systemd/journald.conf.d/sentinela.conf <<'EOF'
[Journal]
SystemMaxUse=200M
EOF
systemctl daemon-reload
systemctl daemon-reexec
systemctl restart systemd-journald

echo "==> enabled at boot"
for u in docker containerd tailscaled ssh ufw fail2ban chrony smbd nmbd wsdd avahi-daemon smartmontools \
  sentinela-watchdog.service sentinela-backup.timer sentinela-disk-health.timer sentinela-trash.timer; do
  systemctl enable "$u" >/dev/null 2>&1 || echo "!! $u missing"
  printf '%-28s %s\n' "$u" "$(systemctl is-enabled "$u" 2>/dev/null)"
done
[[ -f /var/run/reboot-required ]] && echo "!! reboot required (kernel/libc update)"
exit 0
