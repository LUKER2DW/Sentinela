#!/usr/bin/env bash
# Publish mobile/build/sentinela.apk on the server for in-app updates
# (GET /api/app/latest, the app's "Atualizar" button). Run after
# build-apk.sh. The version comes from mobile/android/app/build.gradle.
#
#   ./scripts/publish-apk.sh ["what changed, one line"]
#   SENTINELA_HOST=user@host ./scripts/publish-apk.sh
#
# The APK goes first and app.json last, and the server offers a release
# only when the APK's size matches app.json, so a half-copied file is never
# offered. The app also checks the SHA-256 before installing.
set -euo pipefail

HOST="${SENTINELA_HOST:-ana@192.168.1.10}"
KEY="${SENTINELA_SSH_KEY:-$HOME/.ssh/id_ed25519_sentinela}"
SSH=(ssh -i "$KEY" -o BatchMode=yes -o ConnectTimeout=10 "$HOST")
cd "$(dirname "${BASH_SOURCE[0]}")/.."

APK=mobile/build/sentinela.apk
GRADLE=mobile/android/app/build.gradle
[[ -f "$APK" ]] || { echo "no $APK: run ./scripts/build-apk.sh first" >&2; exit 1; }
code="$(sed -n 's/^ *versionCode \([0-9]*\).*/\1/p' "$GRADLE" | head -1)"
name="$(sed -n 's/^ *versionName "\([^"]*\)".*/\1/p' "$GRADLE" | head -1)"
size="$(wc -c <"$APK" | tr -d ' ')"
sha="$(sha256sum "$APK" | cut -d' ' -f1)"
notes="${1:-}"
[[ -n "$code" && -n "$name" ]] || { echo "version not found in $GRADLE" >&2; exit 1; }

json="$(printf '{"version_code":%s,"version_name":"%s","size":%s,"sha256":"%s","notes":"%s"}\n' \
  "$code" "$name" "$size" "$sha" "${notes//\"/\'}")"

echo "==> publish app ${name} (${code}), $((size / 1024 / 1024)) MB"
"${SSH[@]}" 'sudo install -d -o 1000 -g 1000 -m 0755 /data/app'
"${SSH[@]}" 'sudo install -o 1000 -g 1000 -m 0644 /dev/stdin /data/app/sentinela.apk' <"$APK"
printf '%s' "$json" | "${SSH[@]}" 'sudo install -o 1000 -g 1000 -m 0644 /dev/stdin /data/app/app.json'
echo "Published. Phones on an older version see \"Atualizar o app\"."
