#!/usr/bin/env bash
# Build the signed Sentinela Android APK (ADR-0011) on the dev PC.
#
#   ./scripts/build-apk.sh                       # -> mobile/build/sentinela.apk
#   SENTINELA_DOMAIN_URL=https://<public-ip>:<port>   # start screen default (not in git)
#   SENTINELA_PUBLIC_CERT_SHA256=<hex>   # self-signed public certificate the app pins (setup-public.sh status)
#   ~/.sentinela-android/google-services.json must match casa.sentinela.app
#   (or set SENTINELA_GOOGLE_SERVICES_JSON to the file's absolute path).
#
# Needs JDK 21 and the Android SDK (defaults: the per-user installs under
# %LOCALAPPDATA% on Windows; set JAVA_HOME / ANDROID_HOME elsewhere).
# The signing key lives OUTSIDE the repo in ~/.sentinela-android/ (created
# on first run with a random password in a 0600 file). Back that folder up:
# without it, updates cannot be installed over the existing app.
#
# Android Studio: `cd mobile && npm run studio` syncs and opens the project;
# "Run" installs the debug build "Sentinela (dev)" (own app id, WebView at
# chrome://inspect); Build > Build APK(s) on the release variant signs with
# the same key as this script (mobile/android/app/build.gradle).
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/../mobile"

if [[ -n "${LOCALAPPDATA:-}" ]]; then
  : "${JAVA_HOME:=$LOCALAPPDATA/Programs/jdk-21}"
  : "${ANDROID_HOME:=$LOCALAPPDATA/Android/Sdk}"
fi
export JAVA_HOME ANDROID_HOME
[[ -x "$JAVA_HOME/bin/java" || -x "$JAVA_HOME/bin/java.exe" ]] || { echo "JDK 21 not found (JAVA_HOME)" >&2; exit 1; }
[[ -d "$ANDROID_HOME/build-tools" ]] || { echo "Android SDK not found (ANDROID_HOME)" >&2; exit 1; }
BT="$ANDROID_HOME/build-tools/$(ls "$ANDROID_HOME/build-tools" | sort -V | tail -1)"
ext=""; [[ -f "$BT/apksigner.bat" ]] && ext=".bat"

# Java tools on Windows need native paths (Git Bash gives /c/...).
native() { if command -v cygpath >/dev/null; then cygpath -w "$1"; else echo "$1"; fi; }

KS_DIR="${SENTINELA_KEYSTORE_DIR:-$HOME/.sentinela-android}"
KS="$KS_DIR/release.jks"
PASS="$KS_DIR/keystore.pass"
if [[ ! -f "$KS" ]]; then
  mkdir -p "$KS_DIR" && chmod 700 "$KS_DIR"
  (umask 077 && head -c 32 /dev/urandom | base64 | tr -d '/+=\n' >"$PASS")
  "$JAVA_HOME/bin/keytool" -genkeypair -keystore "$(native "$KS")" -storetype PKCS12 -alias sentinela \
    -keyalg RSA -keysize 4096 -validity 10000 -dname "CN=Sentinela, O=Casa" \
    -storepass:file "$(native "$PASS")" -keypass:file "$(native "$PASS")" >/dev/null
  echo "==> new signing key in $KS_DIR: back it up (needed for every future update)"
fi

echo "==> web wrapper (start screen, address ${SENTINELA_DOMAIN_URL:-none})"
npm ci --no-audit --no-fund --silent
npm run sync --silent >/dev/null

echo "==> gradle assembleRelease (signed by Gradle with the key above)"
(cd android && ./gradlew assembleRelease --no-daemon -q)

mkdir -p build
cp android/app/build/outputs/apk/release/app-release.apk build/sentinela.apk
"$BT/apksigner$ext" verify build/sentinela.apk
echo "APK: mobile/build/sentinela.apk ($(du -h build/sentinela.apk | cut -f1)). Copy it to the phone and open it to install."
