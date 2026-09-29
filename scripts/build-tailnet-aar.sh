#!/usr/bin/env bash
# Build the embedded Tailscale node for the Android app (ADR-0019 P2):
# mobile/tailnet (Go, tsnet) -> mobile/android/app/libs/tailnet.aar with gomobile.
#
#   ./scripts/build-tailnet-aar.sh
#
# Needs Go, the Android SDK and the Android NDK (sdkmanager "ndk;<version>";
# set ANDROID_NDK_HOME or it takes the newest under $ANDROID_HOME/ndk).
# gomobile is installed into $(go env GOPATH)/bin on first run. Only arm64
# and arm are built (phones); the AAR is not committed.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ -n "${LOCALAPPDATA:-}" ]]; then
  : "${JAVA_HOME:=$LOCALAPPDATA/Programs/jdk-21}"
  : "${ANDROID_HOME:=$LOCALAPPDATA/Android/Sdk}"
fi
export JAVA_HOME ANDROID_HOME
if [[ -z "${ANDROID_NDK_HOME:-}" ]]; then
  ndk="$(ls -d "$ANDROID_HOME"/ndk/* 2>/dev/null | sort -V | tail -1 || true)"
  [[ -n "$ndk" ]] || { echo "Android NDK not found: install it with the SDK manager (sdkmanager \"ndk;27.2.12479018\")" >&2; exit 1; }
  export ANDROID_NDK_HOME="$ndk"
fi

GOBIN="$(go env GOPATH)/bin"
export PATH="$GOBIN:$PATH"
if ! command -v gomobile >/dev/null; then
  echo "==> installing gomobile"
  (cd "$ROOT/mobile/tailnet" && go install golang.org/x/mobile/cmd/gomobile golang.org/x/mobile/cmd/gobind)
  gomobile init
fi

mkdir -p "$ROOT/mobile/android/app/libs"
echo "==> gomobile bind (NDK $(basename "$ANDROID_NDK_HOME"))"
cd "$ROOT/mobile/tailnet"
gomobile bind -target=android/arm64,android/arm -androidapi 24 -javapkg casa.sentinela \
  -trimpath -ldflags "-s -w" -o "$ROOT/mobile/android/app/libs/tailnet.aar" .
rm -f "$ROOT/mobile/android/app/libs/tailnet-sources.jar"
echo "AAR: mobile/android/app/libs/tailnet.aar ($(du -h "$ROOT/mobile/android/app/libs/tailnet.aar" | cut -f1))"
