#!/usr/bin/env bash
# Cloudflare Tunnel (ADR-0025): installs the host units that let the
# dashboard (System -> Remote access) install cloudflared, run the tunnel and
# show its status. Run by install-server.sh, deploy.sh and auto-update.sh;
# idempotent:
#   sudo /opt/sentinela/scripts/setup-cloudflared.sh units
#   sudo /opt/sentinela/scripts/setup-cloudflared.sh firewall   # after a config change
# Nothing is installed from Cloudflare and no tunnel runs until the owner
# pastes a tunnel token in the dashboard. The connector runs as its own
# system user, confined on loopback by an nftables table
# (scripts/lib/cloudflared.sh).
#
# A tunnel that is running when its unit changes is restarted into the new
# unit (its sandbox) at once, with its name servers' drop-in in place first.
# If it was connected and does not reconnect within about 40 s, the previous
# unit is put back and restarted, and this script exits non-zero so that the
# updater reports it.
set -euo pipefail
[[ $EUID -eq 0 ]] || { echo "run with sudo" >&2; exit 1; }
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/lib/safe.sh
source "$HERE/scripts/lib/safe.sh"
# shellcheck source=scripts/lib/cloudflared.sh
source "$HERE/scripts/lib/cloudflared.sh"
case "${1:-units}" in
  units)
    # One change at a time with the dashboard's agent (connect, disconnect).
    exec 9>"$CF_LOCK"
    flock -w 120 9 || { echo "the tunnel agent is busy; try again" >&2; exit 1; }
    rc=0 changed=no backup="" units=()
    trap 'rm -f "$backup"' EXIT
    # A configured tunnel needs its user and loopback table before it starts
    # in the new unit, which loads that table and does not run without it.
    # When they cannot be set up (no nftables package to be had), the tunnel
    # keeps the unit it has.
    if [[ -f "$CF_ENV" ]] && ! cf_firewall; then
      echo "could not set up the tunnel's loopback firewall (nftables)" >&2
      rc=1
    fi
    if [[ "$rc" == 0 || ! -f "$CF_UNIT_FILE" ]]; then
      units+=("$HERE/deploy/host/$CF_UNIT")
      if ! cmp -s "$HERE/deploy/host/$CF_UNIT" "$CF_UNIT_FILE"; then
        changed=yes
        if [[ -f "$CF_UNIT_FILE" ]]; then
          backup="$(mktemp)"
          cp -p "$CF_UNIT_FILE" "$backup"
        fi
      fi
    fi
    install -m 0644 "${units[@]}" "$HERE"/deploy/host/sentinela-cloudflared-agent.service \
      "$HERE"/deploy/host/sentinela-cloudflared-agent.path "$HERE"/deploy/host/sentinela-cloudflared-agent.timer /etc/systemd/system/
    safe_install_d "${SENTINELA_DATA:-/data}" 0700 "${SENTINELA_DATA:-/data}/cloudflared"
    install -d -m 0755 /var/lib/sentinela
    touch /var/lib/sentinela/cloudflared-units   # tells the dashboard the agent exists
    # Its name servers stay reachable: where they are the router (no
    # systemd-resolved), the sandbox alone would leave cloudflared unable to
    # resolve Cloudflare's edge.
    if [[ -f "$CF_ENV" ]] && cf_dns_dropin; then
      changed=yes
    fi
    systemctl daemon-reload
    if [[ "$changed" == yes ]] && systemctl is-active -q "$CF_UNIT"; then
      was_ready=no
      cf_ready && was_ready=yes
      systemctl restart "$CF_UNIT" || true
      if [[ "$was_ready" == yes ]] && ! cf_wait_ready 40; then
        echo "the tunnel did not reconnect in its new unit" >&2
        if [[ -n "$backup" ]]; then
          echo "putting the previous unit back" >&2
          install -m 0644 "$backup" "$CF_UNIT_FILE"
          systemctl daemon-reload
          systemctl restart "$CF_UNIT" || true
        fi
        rc=1
      fi
    fi
    systemctl enable -q --now sentinela-cloudflared-agent.path sentinela-cloudflared-agent.timer
    systemctl start --no-block sentinela-cloudflared-agent.service || true   # first status
    exit "$rc"
    ;;
  firewall)
    # The tunnel listener's port follows the settings (apply-config.sh runs
    # this after it installs a config): the table is rewritten and loaded,
    # live, for a configured tunnel.
    [[ -f "$CF_ENV" ]] || exit 0
    exec 9>"$CF_LOCK"
    flock -w 120 9 || { echo "the tunnel agent is busy; try again" >&2; exit 1; }
    cf_firewall
    ;;
  *) echo "usage: $0 units|firewall" >&2; exit 2 ;;
esac
