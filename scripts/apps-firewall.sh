#!/usr/bin/env bash
# Host firewall for self-hosted services (ADR-0021). Run as root by
# sentinela-apps-firewall.service at boot, after Docker, and again by its
# .path unit whenever the agent rewrites the list of internet-exposed ports.
#
#   apps-firewall.sh apply|remove|unready|check|status
#
# Docker publishes ports ahead of ufw, so these rules live in DOCKER-USER
# (a chain Docker leaves alone), in our own chain SENTINELA-APPS:
#  - services (bridges "sn-*") may reach the internet, but no private
#    range: not the camera, the router, other LAN hosts, the tailnet. The
#    one exception is port 53 of the host's own DNS servers when they are
#    private (the router, usually): Docker's embedded DNS forwards a
#    service's lookups from the service's network namespace. Those rules
#    come before the drops; nothing else that lets a service out may;
#  - published service ports answer private addresses (LAN, tailnet), and
#    the internet only on the ports listed in the agent's public-ports file.
# Services reaching this host itself go through INPUT, where ufw's default
# deny already applies (no rule allows the Docker subnets).
#
# Runs are serialized with a lock. Only after an apply has checked that
# DOCKER-USER jumps first to the complete chain does it write the marker
# $READY (on /run, so a reboot clears it), which the agent requires before
# it starts any service: the boot id, then the ports opened to the internet.
# A failed apply, `unready` (the unit's stop, also when Docker stops),
# `remove` and a failed `check` (a timer, every 2 minutes: rules flushed
# within a boot) delete the marker.
set -euo pipefail
# shellcheck source=scripts/lib/safe.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/safe.sh"
CHAIN=SENTINELA-APPS
PUBLIC=${SENTINELA_APPS_PUBLIC:-/var/lib/sentinela-apps/public-ports}
RUN_DIR=${SENTINELA_APPS_RUN:-/run/sentinela-apps}
READY="$RUN_DIR/firewall-ready"
PRIVATE=(10.0.0.0/8 172.16.0.0/12 192.168.0.0/16 100.64.0.0/10 169.254.0.0/16 127.0.0.0/8)
# Where Docker's embedded DNS finds the upstream servers (systemd-resolved's
# list when /etc/resolv.conf names only its stub).
RESOLV=${SENTINELA_APPS_RESOLV:-/run/systemd/resolve/resolv.conf /etc/resolv.conf}
APPLIED=0

# lock: one run at a time (the boot unit and the path unit can overlap).
# The folder is root's, on /run; it is mounted read-only into the agent, so
# it is never removed or replaced (only the files in it are).
lock() {
  install -d -o root -g root -m 0755 "$RUN_DIR"
  exec 9>"$RUN_DIR/.lock"
  flock -w 120 9 || { echo "another run of $0 holds the lock" >&2; exit 1; }
}

unready() { rm -f "$READY" "$READY.tmp"; }

# public_ports prints the valid lines of the agent's list. The file is in
# the agent's (uid 1000) folder: it is read through datafs.py, so a link,
# FIFO or oversized file there is refused (no public port) instead of being
# followed or holding the lock.
public_ports() {
  local body rc=0 port proto
  body="$(safe_read "$(dirname "$PUBLIC")" "$PUBLIC" 65536 2>/dev/null)" || rc=$?
  if (( rc != 0 )); then
    (( rc == 2 )) || echo "public ports not read: $PUBLIC refused" >&2
    return 0
  fi
  while IFS=/ read -r port proto; do
    [[ "$port" =~ ^[0-9]{1,5}$ && "$proto" =~ ^(tcp|udp)$ ]] || continue
    (( port >= 1024 && port <= 65535 )) || continue
    echo "$port/$proto"
  done <<<"$body"
}

# resolvers prints the host's IPv4 DNS servers in a private range (public
# ones need no rule; loopback ones are reached from the host's namespace),
# at most 4. Always succeeds (it runs under set -e and pipefail).
resolvers() {
  local f key ip
  for f in $RESOLV; do
    [[ -f "$f" && -r "$f" ]] || continue
    while read -r key ip _; do
      if [[ "$key" == nameserver ]] && private_v4 "$ip"; then echo "$ip"; fi
    done < <(head -c 65536 "$f")
  done | awk '!seen[$0]++ && n++ < 4'
}

# private_v4 IP: IP is a plain dotted IPv4 address in one of PRIVATE's
# ranges other than loopback.
private_v4() {
  local o='(0|[1-9][0-9]{0,2})' a b c d
  [[ "$1" =~ ^$o\.$o\.$o\.$o$ ]] || return 1
  a=${BASH_REMATCH[1]} b=${BASH_REMATCH[2]} c=${BASH_REMATCH[3]} d=${BASH_REMATCH[4]}
  (( a <= 255 && b <= 255 && c <= 255 && d <= 255 )) || return 1
  (( a == 10 )) || (( a == 172 && b >= 16 && b <= 31 )) || (( a == 192 && b == 168 )) ||
    (( a == 100 && b >= 64 && b <= 127 )) || (( a == 169 && b == 254 ))
}

# verify: DOCKER-USER's first rule jumps to the chain, which holds every
# private-range drop, lets nothing from a service out before them but DNS
# to the host's resolvers, and ends with the drop of new inbound connections.
verify() {
  local first
  iptables -C FORWARD -j DOCKER-USER >/dev/null 2>&1 || { echo "FORWARD does not jump to DOCKER-USER" >&2; return 1; }
  first="$(iptables -S DOCKER-USER | sed -n 2p)"
  [[ "$first" == "-A DOCKER-USER -j $CHAIN" ]] || { echo "DOCKER-USER does not start with the jump to $CHAIN" >&2; return 1; }
  for net in "${PRIVATE[@]}"; do
    iptables -C "$CHAIN" -i sn-+ -d "$net" -j DROP >/dev/null 2>&1 || { echo "$CHAIN lacks the drop of $net" >&2; return 1; }
  done
  [[ "$(iptables -S "$CHAIN" | tail -1)" == "-A $CHAIN -o sn-+ -m conntrack --ctstate NEW -j DROP" ]] ||
    { echo "$CHAIN does not end with the drop of new connections" >&2; return 1; }
  # Rule order: up to the first private-range drop, only replies, traffic
  # between services and DNS to a current resolver of the host.
  local line dns re
  dns=" $(resolvers | tr '\n' ' ')"
  re="^-A $CHAIN -d ([0-9.]+)/32 -i sn-\+ -p (udp|tcp) -m (udp|tcp) --dport 53 -j RETURN\$"
  while IFS= read -r line; do
    case "$line" in
      "-N $CHAIN" | "-A $CHAIN -m conntrack --ctstate RELATED,ESTABLISHED -j RETURN" | "-A $CHAIN -i sn-+ -o sn-+ -j RETURN") continue ;;
      "-A $CHAIN -d "*" -i sn-+ -j DROP") return 0 ;;
    esac
    if [[ "$line" =~ $re && "$dns" == *" ${BASH_REMATCH[1]} "* ]]; then
      continue
    fi
    echo "$CHAIN lets services out before the private-range drops: $line" >&2
    return 1
  done < <(iptables -S "$CHAIN")
  echo "$CHAIN has no private-range drop" >&2
  return 1
}

apply() {
  lock
  # A failed or interrupted run leaves no marker: services do not start.
  trap '[[ $APPLIED == 1 ]] || unready' EXIT
  if ! iptables -n -L DOCKER-USER >/dev/null 2>&1; then
    echo "DOCKER-USER chain missing: is Docker running?" >&2
    exit 1
  fi
  # The rules may be gone (a Docker or iptables reset) while a marker of
  # this boot remains: until they are back, there is no marker.
  iptables -C DOCKER-USER -j "$CHAIN" >/dev/null 2>&1 || unready
  local opened dns
  mapfile -t opened < <(public_ports)
  mapfile -t dns < <(resolvers)
  # Build the new rules in a fresh chain, then swap it in, so there is no
  # moment without them.
  local next="${CHAIN}-N"
  while iptables -D DOCKER-USER -j "$next" 2>/dev/null; do :; done # left by an interrupted run
  iptables -N "$next" 2>/dev/null || iptables -F "$next"
  iptables -A "$next" -m conntrack --ctstate RELATED,ESTABLISHED -j RETURN
  iptables -A "$next" -i sn-+ -o sn-+ -j RETURN
  local r
  for r in "${dns[@]}"; do
    iptables -A "$next" -i sn-+ -d "$r/32" -p udp --dport 53 -j RETURN
    iptables -A "$next" -i sn-+ -d "$r/32" -p tcp --dport 53 -j RETURN
  done
  for net in "${PRIVATE[@]}"; do
    iptables -A "$next" -i sn-+ -d "$net" -j DROP
  done
  for net in "${PRIVATE[@]}"; do
    iptables -A "$next" -o sn-+ -s "$net" -j RETURN
  done
  local p
  for p in "${opened[@]}"; do
    iptables -A "$next" -o sn-+ -p "${p#*/}" -m conntrack --ctorigdstport "${p%/*}" --ctdir ORIGINAL -j RETURN
  done
  iptables -A "$next" -o sn-+ -m conntrack --ctstate NEW -j DROP
  iptables -I DOCKER-USER 1 -j "$next"
  # Drop the previous chain (if any) and rename the new one.
  while iptables -D DOCKER-USER -j "$CHAIN" 2>/dev/null; do :; done
  if iptables -n -L "$CHAIN" >/dev/null 2>&1; then
    iptables -F "$CHAIN"
    iptables -X "$CHAIN"
  fi
  iptables -E "$next" "$CHAIN"
  verify
  local boot
  boot="$(cat /proc/sys/kernel/random/boot_id)"
  [[ "$boot" =~ ^[0-9a-f-]{36}$ ]] || { echo "unexpected boot id" >&2; exit 1; }
  {
    echo "boot $boot"
    for p in "${opened[@]}"; do echo "public $p"; done
  } >"$READY.tmp"
  chmod 0644 "$READY.tmp"
  mv -f "$READY.tmp" "$READY"
  APPLIED=1
  echo "services firewall applied (${#opened[@]} public port(s), ${#dns[@]} private DNS server(s))"
}

remove() {
  lock
  unready
  while iptables -D DOCKER-USER -j "$CHAIN" 2>/dev/null; do :; done
  for c in "$CHAIN" "${CHAIN}-N"; do
    if iptables -n -L "$c" >/dev/null 2>&1; then
      iptables -F "$c"
      iptables -X "$c"
    fi
  done
}

[[ $EUID -eq 0 ]] || { echo "run as root" >&2; exit 1; }
case "${1:-status}" in
  apply) apply ;;
  remove) remove; echo "services firewall removed" ;;
  # The marker goes first: a run holding the lock must not delay it.
  unready) unready; lock; unready; echo "services firewall marker removed: services wait" ;;
  check)
    lock
    if [[ -f "$READY" ]] && ! verify; then
      unready
      echo "services firewall rules changed: marker removed, services wait for the next apply" >&2
    fi
    ;;
  status)
    iptables -S DOCKER-USER 2>/dev/null | grep -q -- "-j $CHAIN" && echo "hooked into DOCKER-USER: yes" || echo "hooked into DOCKER-USER: no"
    [[ -f "$READY" ]] && verify 2>/dev/null && echo "verified for services: yes" || echo "verified for services: no"
    iptables -S "$CHAIN" 2>/dev/null || echo "chain $CHAIN: missing"
    echo "public ports: $(public_ports | tr '\n' ' ')"
    ;;
  *) echo "usage: $0 apply|remove|unready|check|status" >&2; exit 2 ;;
esac
