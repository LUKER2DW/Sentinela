#!/usr/bin/env bash
# Tailscale from the dashboard (welcome guide and System -> Server; ADR-0004).
# Run as root by sentinela-tailscale.path when the dashboard leaves a request
# in $DATA/tailscale, and by sentinela-tailscale.timer to refresh the status
# the dashboard shows (/var/lib/sentinela/tailscale.json).
#
# Requests (first word of $DATA/tailscale/request):
#   status      only refresh the status
#   connect     install Tailscale if missing, join the tailnet with the auth
#               key the person pasted ($DATA/tailscale/authkey: moved away
#               and deleted at once, given to tailscale as a file, never
#               logged), then publish the web UI over HTTPS
#   serve       publish the web UI again (after HTTPS certificates were
#               turned on in the admin console)
#   disconnect  stop publishing and log the server out of the tailnet
#
# Same choices as setup-tailscale.sh: no Tailscale SSH, no subnet routes, no
# exit node, host DNS untouched; nothing opens a port on the router.
set -euo pipefail
[[ $EUID -eq 0 ]] || { echo "run with sudo" >&2; exit 1; }
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DATA="${SENTINELA_DATA:-/data}"
REQ="$DATA/tailscale"
STATE=/var/lib/sentinela
OUT="$STATE/tailscale.json"
NAME="${SENTINELA_TAILSCALE_HOSTNAME:-sentinela}"
[[ "$NAME" =~ ^[a-z0-9][a-z0-9-]{0,62}$ ]] || NAME=sentinela

exec 9>/run/sentinela-tailscale.lock
flock -w 120 9 || exit 0
install -d -m 0755 "$STATE"

action=status
if [[ -f "$REQ/request" ]]; then
  read -r action _ < "$REQ/request" || true
  rm -f "$REQ/request"
fi
key=""
if [[ -f "$REQ/authkey" ]]; then
  key="$(mktemp /run/sentinela-tskey.XXXXXX)"   # root only (mktemp: 0600)
  head -c 512 "$REQ/authkey" | tr -d '[:space:]' > "$key"
  rm -f "$REQ/authkey"
  trap 'shred -u "$key" 2>/dev/null || rm -f "$key"' EXIT
fi

message=""   # what the dashboard shows after this run
enable_url=""

# status [BUSY]: writes what the dashboard reads.
status() {
  local st="{}" serve="{}" installed=false version=""
  if command -v tailscale >/dev/null; then
    installed=true
    version="$(tailscale version 2>/dev/null | head -1 || true)"
    st="$(timeout 10 tailscale status --json 2>/dev/null || echo '{}')"
    serve="$(timeout 10 tailscale serve status --json 2>/dev/null || echo '{}')"
  fi
  ST="$st" SERVE="$serve" python3 - "$OUT" "$installed" "$version" "${1:-false}" "$action" "$message" "$enable_url" <<'PY'
import json, os, sys, datetime
out, installed, version, busy, action, message, enable_url = sys.argv[1:8]
def load(v):
    try:
        return json.loads(v or "{}") or {}
    except Exception:
        return {}
st, serve = load(os.environ.get("ST")), load(os.environ.get("SERVE"))
me = st.get("Self") or {}
dns = (me.get("DNSName") or "").rstrip(".")
ips = me.get("TailscaleIPs") or []
web = (serve.get("Web") or {}).get(f"{dns}:443", {}) if dns else {}
proxy = ((web.get("Handlers") or {}).get("/") or {}).get("Proxy", "")
doc = {
    "checked": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
    "installed": installed == "true",
    "version": version,
    "state": st.get("BackendState", "") if installed == "true" else "missing",
    "dns_name": dns,
    "ip": next((i for i in ips if "." in i), ""),
    "tags": me.get("Tags") or [],
    "magic_dns": bool((st.get("CurrentTailnet") or {}).get("MagicDNSEnabled")),
    "https": bool(st.get("CertDomains")),
    "serve": proxy.rstrip("/") == "http://127.0.0.1:8080",
    "busy": busy == "true",
    "action": action,
    "message": message,
    "enable_url": enable_url,
}
if action == "status":  # a refresh keeps the last action's result
    try:
        old = json.load(open(out))
        for k in ("action", "message", "enable_url"):
            doc[k] = old.get(k, "")
    except Exception:
        pass
if doc["serve"] and doc["message"] == "https_disabled":
    doc["message"], doc["enable_url"] = "ok", ""
tmp = out + ".tmp"
with open(tmp, "w") as f:
    json.dump(doc, f)
os.chmod(tmp, 0o644)
os.replace(tmp, out)
PY
}

install_tailscale() {
  command -v tailscale >/dev/null && return 0
  message="installing Tailscale"
  status true
  # shellcheck disable=SC1091
  . /etc/os-release
  local base="https://pkgs.tailscale.com/stable/${ID}/${VERSION_CODENAME}"
  curl -fsSL --max-time 60 "${base}.noarmor.gpg" -o /usr/share/keyrings/tailscale-archive-keyring.gpg &&
    curl -fsSL --max-time 60 "${base}.tailscale-keyring.list" -o /etc/apt/sources.list.d/tailscale.list &&
    apt-get -o DPkg::Lock::Timeout=600 update -qq &&
    DEBIAN_FRONTEND=noninteractive apt-get -o DPkg::Lock::Timeout=600 install -y -qq tailscale >/dev/null
}

# publish: the web UI on https://<name>.<tailnet>.ts.net (renewed by tailscale).
publish() {
  local out
  if out="$(timeout 30 tailscale serve --bg --yes --https=443 http://127.0.0.1:8080 2>&1)"; then
    message="ok"
    return 0
  fi
  # Without HTTPS certificates, tailscale prints a link that turns them on.
  enable_url="$(printf '%s' "$out" | grep -oE 'https://login\.tailscale\.com/[A-Za-z0-9/?=&._-]+' | head -1 || true)"
  message="https_disabled"
  return 1
}

case "$action" in
  connect)
    if [[ -z "$key" ]] || ! grep -qE '^tskey-auth-[A-Za-z0-9_-]{10,200}$' "$key"; then
      message="bad_key"
    elif ! install_tailscale; then
      message="install_failed"
    else
      systemctl enable -q --now tailscaled
      "$DIR/scripts/firewall.sh" >/dev/null 2>&1 || true   # tailscale0: only 443/8555/22
      message="joining the tailnet"
      status true
      if ! timeout 90 tailscale up --reset --force-reauth --auth-key="file:$key" --hostname="$NAME" \
          --ssh=false --accept-dns=false --accept-routes=false --advertise-exit-node=false --timeout=60s >/dev/null 2>&1; then
        state="$(timeout 10 tailscale status --json 2>/dev/null | python3 -c 'import json,sys; print(json.load(sys.stdin).get("BackendState",""))' 2>/dev/null || true)"
        if [[ "$state" == NeedsMachineAuth ]]; then message="needs_approval"; else message="key_refused"; fi
      else
        publish || true
      fi
    fi
    ;;
  serve)
    if command -v tailscale >/dev/null; then publish || true; else message="not_installed"; fi
    ;;
  disconnect)
    if command -v tailscale >/dev/null; then
      timeout 30 tailscale serve reset >/dev/null 2>&1 || true
      timeout 30 tailscale logout >/dev/null 2>&1 || true
      message="disconnected"
    fi
    ;;
  *) action=status ;;
esac
status false
