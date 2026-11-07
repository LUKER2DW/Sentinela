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

# shellcheck source=scripts/lib/lan.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib/lan.sh"
deploy_target || exit 1
cd "$(dirname "${BASH_SOURCE[0]}")/.."

APK=mobile/build/sentinela.apk
[[ -f "$APK" ]] || { echo "no $APK: run ./scripts/build-apk.sh first" >&2; exit 1; }
# The version is read from the APK itself (CI builds take it from the tag).
if [[ -n "${LOCALAPPDATA:-}" ]]; then : "${ANDROID_HOME:=$LOCALAPPDATA/Android/Sdk}"; fi
aapt2="$(find "${ANDROID_HOME:?set ANDROID_HOME}/build-tools" -name 'aapt2*' -type f 2>/dev/null | sort -V | tail -1)"
[[ -n "$aapt2" ]] || { echo "aapt2 not found in $ANDROID_HOME/build-tools" >&2; exit 1; }
badging="$("$aapt2" dump badging "$APK" | head -1)"
code="$(sed -n "s/.*versionCode='\([0-9]*\)'.*/\1/p" <<<"$badging")"
name="$(sed -n "s/.*versionName='\([^']*\)'.*/\1/p" <<<"$badging")"
size="$(wc -c <"$APK" | tr -d ' ')"
sha="$(sha256sum "$APK" | cut -d' ' -f1)"
notes="${1:-}"
[[ -n "$code" && -n "$name" ]] || { echo "version not found in $APK" >&2; exit 1; }

json="$(printf '{"version_code":%s,"version_name":"%s","size":%s,"sha256":"%s","notes":"%s"}\n' \
  "$code" "$name" "$size" "$sha" "${notes//\"/\'}")"

echo "==> publish app ${name} (${code}), $((size / 1024 / 1024)) MB"
"${SSH[@]}" 'sudo install -d -o 1000 -g 1000 -m 0755 /data/app'
"${SSH[@]}" 'sudo install -o 1000 -g 1000 -m 0644 /dev/stdin /data/app/sentinela.apk' <"$APK"
printf '%s' "$json" | "${SSH[@]}" 'sudo install -o 1000 -g 1000 -m 0644 /dev/stdin /data/app/app.json'
echo "Published. Phones on an older version see \"Atualizar o app\"."
