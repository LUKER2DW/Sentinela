# shellcheck shell=bash
# LAN facts shared by the server scripts. Sourced, never run.
# Override with SENTINELA_LAN_IFACE / SENTINELA_LAN (CIDR) when the server
# has several interfaces or the default route is not the home LAN.

# lan_iface prints the interface of the default route (e.g. eno1).
lan_iface() {
  if [[ -n "${SENTINELA_LAN_IFACE:-}" ]]; then echo "$SENTINELA_LAN_IFACE"; return; fi
  ip -o route show default | awk '{for (i = 1; i < NF; i++) if ($i == "dev") { print $(i + 1); exit }}'
}

# lan_cidr prints the network of that interface (e.g. 192.168.1.0/24).
lan_cidr() {
  if [[ -n "${SENTINELA_LAN:-}" ]]; then echo "$SENTINELA_LAN"; return; fi
  local dev
  dev="$(lan_iface)"
  [[ -n "$dev" ]] || { echo "no default route: set SENTINELA_LAN" >&2; return 1; }
  ip -o -f inet route show dev "$dev" scope link | awk '$1 ~ /\// {print $1; exit}'
}

# deploy_target sets SSH=(ssh ... user@server) for the dev-machine scripts.
# The server and key come from the environment or from a file outside Git,
# ~/.sentinela/deploy.env (SENTINELA_HOST=user@server, optional
# SENTINELA_SSH_KEY=~/.ssh/key); nothing about the owner's network is
# hard-coded in the repository.
deploy_target() {
  local f="${SENTINELA_DEPLOY_ENV:-$HOME/.sentinela/deploy.env}"
  if [[ -f "$f" ]]; then
    local line k v
    while IFS= read -r line || [[ -n "$line" ]]; do
      [[ "$line" =~ ^(SENTINELA_HOST|SENTINELA_SSH_KEY)=(.*)$ ]] || continue
      k="${BASH_REMATCH[1]}" v="${BASH_REMATCH[2]}"
      v="${v%\"}" v="${v#\"}" v="${v/#\~/$HOME}"
      [[ -n "${!k:-}" ]] || printf -v "$k" '%s' "$v"
    done <"$f"
  fi
  [[ -n "${SENTINELA_HOST:-}" ]] || {
    echo "set SENTINELA_HOST=user@server (or put it in $f)" >&2
    return 1
  }
  SSH=(ssh -o BatchMode=yes -o ConnectTimeout=10)
  [[ -z "${SENTINELA_SSH_KEY:-}" ]] || SSH+=(-i "$SENTINELA_SSH_KEY")
  SSH+=("$SENTINELA_HOST")
}
