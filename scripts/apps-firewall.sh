#!/usr/bin/env bash
# Host firewall for self-hosted services (ADR-0021). Run as root by
# sentinela-apps-firewall.service at boot, after Docker, and again by its
# .path unit whenever the agent rewrites the list of internet-exposed ports.
#
#   apps-firewall.sh apply|remove|status
#
# Docker publishes ports ahead of ufw, so these rules live in DOCKER-USER
# (a chain Docker leaves alone), in our own chain SENTINELA-APPS:
#  - services (bridges "sn-*") may reach the internet and DNS, but no
#    private range: not the camera, the router, other LAN hosts, the tailnet;
#  - published service ports answer private addresses (LAN, tailnet), and
#    the internet only on the ports listed in the agent's public-ports file.
# Services reaching this host itself go through INPUT, where ufw's default
# deny already applies (no rule allows the Docker subnets).
set -euo pipefail
CHAIN=SENTINELA-APPS
PUBLIC=${SENTINELA_APPS_PUBLIC:-/var/lib/sentinela-apps/public-ports}
PRIVATE=(10.0.0.0/8 172.16.0.0/12 192.168.0.0/16 100.64.0.0/10 169.254.0.0/16 127.0.0.0/8)

apply() {
  if ! iptables -n -L DOCKER-USER >/dev/null 2>&1; then
    echo "DOCKER-USER chain missing: is Docker running?" >&2
    exit 1
  fi
  # Build the new rules in a fresh chain, then swap it in, so there is no
  # moment without them.
  local next="${CHAIN}-N"
  while iptables -D DOCKER-USER -j "$next" 2>/dev/null; do :; done # left by an interrupted run
  iptables -N "$next" 2>/dev/null || iptables -F "$next"
  iptables -A "$next" -m conntrack --ctstate RELATED,ESTABLISHED -j RETURN
  iptables -A "$next" -i sn-+ -o sn-+ -j RETURN
  iptables -A "$next" -i sn-+ -p udp --dport 53 -j RETURN
  iptables -A "$next" -i sn-+ -p tcp --dport 53 -j RETURN
  for net in "${PRIVATE[@]}"; do
    iptables -A "$next" -i sn-+ -d "$net" -j DROP
  done
  for net in "${PRIVATE[@]}"; do
    iptables -A "$next" -o sn-+ -s "$net" -j RETURN
  done
  if [[ -f "$PUBLIC" ]]; then
    while IFS=/ read -r port proto; do
      [[ "$port" =~ ^[0-9]{1,5}$ && "$proto" =~ ^(tcp|udp)$ ]] || continue
      (( port >= 1024 && port <= 65535 )) || continue
      iptables -A "$next" -o sn-+ -p "$proto" -m conntrack --ctorigdstport "$port" --ctdir ORIGINAL -j RETURN
    done < "$PUBLIC"
  fi
  iptables -A "$next" -o sn-+ -m conntrack --ctstate NEW -j DROP
  iptables -I DOCKER-USER 1 -j "$next"
  # Drop the previous chain (if any) and rename the new one.
  while iptables -D DOCKER-USER -j "$CHAIN" 2>/dev/null; do :; done
  if iptables -n -L "$CHAIN" >/dev/null 2>&1; then
    iptables -F "$CHAIN"
    iptables -X "$CHAIN"
  fi
  iptables -E "$next" "$CHAIN"
}

remove() {
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
  apply) apply; echo "services firewall applied ($(grep -c . "$PUBLIC" 2>/dev/null || echo 0) public port(s))" ;;
  remove) remove; echo "services firewall removed" ;;
  status)
    iptables -S DOCKER-USER 2>/dev/null | grep -q -- "-j $CHAIN" && echo "hooked into DOCKER-USER: yes" || echo "hooked into DOCKER-USER: no"
    iptables -S "$CHAIN" 2>/dev/null || echo "chain $CHAIN: missing"
    if [[ -f "$PUBLIC" ]]; then
      echo "public ports: $(tr '\n' ' ' < "$PUBLIC")"
    else
      echo "public ports: none"
    fi
    ;;
  *) echo "usage: $0 apply|remove|status" >&2; exit 2 ;;
esac
