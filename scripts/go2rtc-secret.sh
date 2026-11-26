#!/usr/bin/env bash
# Make sure secrets.env holds go2rtc's API password, GO2RTC_API_PASSWORD
# (run as root on the server). go2rtc's API answers only callers that send
# it, loopback ones too (deploy/go2rtc.yaml), and only `sentinela serve`
# reads it: the detector, which decodes camera video next to it on the host
# network, gets no secrets.env. Compose refuses to start go2rtc without it.
#
#   sudo scripts/go2rtc-secret.sh [secrets.env]
#
# Idempotent: a valid password is kept. Prints "changed" when it writes one;
# `docker compose up -d` then recreates the services that read the file.
# install-server.sh and gen-go2rtc-cameras.sh (every update, deploy and
# camera change) run it. The value is never printed.
set -euo pipefail
SECRETS="${1:-/etc/sentinela/secrets.env}"
[[ $EUID -eq 0 ]] || { echo "run with sudo" >&2; exit 1; }
[[ -f "$SECRETS" && ! -L "$SECRETS" ]] || { echo "no $SECRETS (or not a plain file)" >&2; exit 1; }

# 32 or more letters and digits: safe inside go2rtc's YAML and Basic auth.
if grep -qE '^GO2RTC_API_PASSWORD=[A-Za-z0-9]{32,256}$' "$SECRETS"; then
  exit 0
fi
umask 077
tmp="$(mktemp "${SECRETS}.XXXXXX")"
trap 'rm -f "$tmp"' EXIT
# Every other line stays as it is; a malformed entry is replaced.
grep -v '^GO2RTC_API_PASSWORD=' "$SECRETS" > "$tmp" || true
printf 'GO2RTC_API_PASSWORD=%s\n' "$(od -An -tx1 -N32 /dev/urandom | tr -d ' \n')" >> "$tmp"
chown root:root "$tmp"
chmod 0600 "$tmp"
mv -f "$tmp" "$SECRETS"
trap - EXIT
echo changed
