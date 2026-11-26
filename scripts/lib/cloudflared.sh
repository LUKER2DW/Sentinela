# shellcheck shell=bash disable=SC2034 # the names are read where this is sourced
# The Cloudflare Tunnel's host side (ADR-0025), shared by
# setup-cloudflared.sh (the units) and cloudflared-agent.sh (connect, status).
# Sourced by root scripts, never run: names and functions only.

CF_UNIT=sentinela-cloudflared.service
CF_UNIT_FILE=/etc/systemd/system/$CF_UNIT
CF_ENV=/etc/sentinela/cloudflared.env
CF_DNS_DROPIN=/etc/systemd/system/$CF_UNIT.d/dns.conf
CF_LOCK=/run/sentinela-cloudflared.lock

# cf_dns_dropin: the unit may reach no private address and nothing over
# IPv6, except the name servers this host uses (often the home router), so
# that cloudflared can still resolve Cloudflare's edge. Keeps $CF_DNS_DROPIN
# in step with /etc/resolv.conf (root-owned); status 0 when it changed.
cf_dns_dropin() {
  local want have=""
  want="$(python3 -I - /etc/resolv.conf <<'PY'
import ipaddress, sys
allow = []
try:
    lines = open(sys.argv[1], encoding="utf-8", errors="replace").read().split("\n")
except OSError:
    lines = []
for line in lines:
    f = line.split()
    if len(f) < 2 or f[0] != "nameserver":
        continue
    try:
        ip = ipaddress.ip_address(f[1].split("%", 1)[0])
    except ValueError:
        continue
    if not ip.is_loopback and str(ip) not in allow:
        allow.append(str(ip))
if allow:
    print("# Written by scripts/lib/cloudflared.sh: this host's name servers.")
    print("[Service]")
    print("IPAddressAllow=" + " ".join(allow))
PY
)"
  [[ -f "$CF_DNS_DROPIN" ]] && have="$(cat "$CF_DNS_DROPIN")"
  [[ "$want" != "$have" ]] || return 1
  if [[ -n "$want" ]]; then
    install -d -m 0755 "${CF_DNS_DROPIN%/*}"
    printf '%s\n' "$want" > "$CF_DNS_DROPIN.tmp" && chmod 0644 "$CF_DNS_DROPIN.tmp" && mv -f "$CF_DNS_DROPIN.tmp" "$CF_DNS_DROPIN"
  else
    rm -f "$CF_DNS_DROPIN"
  fi
  systemctl daemon-reload
}

# cf_ready: the running tunnel holds at least one connection to
# Cloudflare's edge (its metrics listener says so).
cf_ready() {
  local r
  r="$(curl -fsS --max-time 3 http://127.0.0.1:20241/ready 2>/dev/null)" || return 1
  [[ "$r" == *'"readyConnections":'[1-9]* ]]
}

# cf_wait_ready [SECONDS]: cf_ready within SECONDS (default 40; Cloudflare's
# edge accepts a tunnel in a few seconds).
cf_wait_ready() {
  local end=$((SECONDS + ${1:-40}))
  until cf_ready; do
    ((SECONDS < end)) || return 1
    sleep 2
  done
}

# The connector's reach on this host. The unit's sandbox allows only
# loopback and the internet, but its routes come from the Cloudflare
# account, and on loopback listen go2rtc's API and RTSP and whatever else
# runs here. So the connector runs as a static system user, and a small
# nftables table keyed on that user's uid refuses everything it sends
# through the loopback interface (127.0.0.0/8, ::1 and this host's own
# addresses) except the tunnel listener (remote.proxy.listen), a name server
# here (systemd-resolved) and the answers of its own metrics listener. It is
# a table of its own (inet sentinela_cloudflared), apart from ufw's and
# Docker's rules and matched by uid, not by cgroup (which nft would resolve
# once, at load time); the unit loads $CF_NFT before each start, so the
# tunnel never runs without it.
CF_USER=sentinela-cloudflared
CF_NFT=/etc/sentinela/cloudflared.nft
CF_METRICS=20241
CF_PROXY_DEFAULT=7444

# cf_user: the connector's system user (no home, no login), made once.
cf_user() {
  getent passwd "$CF_USER" >/dev/null ||
    useradd --system --user-group --no-create-home --home-dir /nonexistent --shell /usr/sbin/nologin "$CF_USER"
}

# cf_nft: nft, from the distribution's nftables package when missing.
cf_nft() {
  command -v nft >/dev/null && return 0
  { apt-get -o DPkg::Lock::Timeout=600 update -qq &&
      DEBIAN_FRONTEND=noninteractive apt-get -o DPkg::Lock::Timeout=600 install -y -qq nftables; } >/dev/null
}

# cf_ruleset UID [PORT]: the table for the connector's uid UID; PORT is the
# tunnel listener's (none: that listener is off). Loading it replaces the
# previous one in one transaction.
cf_ruleset() {
  local uid="$1" port="${2:-}" listener="# (off)"
  [[ "$uid" =~ ^[0-9]{1,10}$ && "$port" =~ ^([0-9]{1,5})?$ ]] || return 1
  [[ -z "$port" ]] || listener="tcp dport $port accept"
  cat <<NFT
# The Cloudflare Tunnel connector's reach on this host (ADR-0025): written by
# scripts/lib/cloudflared.sh, loaded by sentinela-cloudflared.service before
# each start. Rewritten, never edited.
table inet sentinela_cloudflared
delete table inet sentinela_cloudflared
table inet sentinela_cloudflared {
  chain output {
    type filter hook output priority 0; policy accept;
    meta skuid $uid oifname "lo" jump loopback
  }
  chain loopback {
    # remote.proxy.listen, the tunnel's one service
    $listener
    # answers from the connector's metrics listener (status, readiness)
    tcp sport $CF_METRICS accept
    # a name server on this host
    udp dport 53 accept
    tcp dport 53 accept
    meta l4proto tcp reject with tcp reset
    reject
  }
}
NFT
}

# cf_proxy_port: the port of remote.proxy.listen in the installed config,
# nothing when that listener is off. When the image cannot say (Docker not
# up, an older image), the port the current table allows, else the default.
cf_proxy_port() {
  local out
  if out="$(timeout 120 docker run --rm --network none -v /etc/sentinela/config.yaml:/etc/sentinela/config.yaml:ro \
        --entrypoint /usr/local/bin/sentinela sentinela:local proxy-port 2>/dev/null)" &&
      [[ "$out" =~ ^([0-9]{1,5})?$ ]]; then
    printf '%s' "$out"
  elif [[ -f "$CF_NFT" ]]; then
    sed -n '/^ *# remote\.proxy\.listen/{n;s/^ *tcp dport \([0-9]\{1,5\}\) accept$/\1/p;}' "$CF_NFT"
  else
    printf '%s' "$CF_PROXY_DEFAULT"
  fi
}

# cf_firewall: the user, nft and the table for the installed config, loaded
# now and kept in $CF_NFT for the unit's next start.
cf_firewall() {
  local uid port
  cf_user && cf_nft || return 1
  uid="$(id -u "$CF_USER")" || return 1
  port="$(cf_proxy_port)"
  if ! { cf_ruleset "$uid" "$port" > "$CF_NFT.tmp" && nft -f "$CF_NFT.tmp"; }; then
    rm -f "$CF_NFT.tmp"
    return 1
  fi
  chmod 0644 "$CF_NFT.tmp" && mv -f "$CF_NFT.tmp" "$CF_NFT"
}

# cf_firewall_kept: for a configured tunnel, the table as it should be:
# rewritten when config.yaml is newer (another proxy port), loaded again
# when something removed it (an "nft flush ruleset").
cf_firewall_kept() {
  [[ -f "$CF_ENV" ]] || return 0
  if [[ ! -f "$CF_NFT" || /etc/sentinela/config.yaml -nt "$CF_NFT" ]]; then
    cf_firewall
  elif ! nft list table inet sentinela_cloudflared >/dev/null 2>&1; then
    nft -f "$CF_NFT"
  fi
}

# cf_firewall_remove: no tunnel, no table.
cf_firewall_remove() {
  if command -v nft >/dev/null; then
    nft delete table inet sentinela_cloudflared 2>/dev/null || true
  fi
  rm -f "$CF_NFT"
}
