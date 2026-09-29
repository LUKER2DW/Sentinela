import type { CapacitorConfig } from "@capacitor/cli";

// The app loads the Sentinela server itself over Tailscale (ADR-0011,
// option a): same origin, same cookie/CSRF auth, nothing bundled but the
// "connect Tailscale" page shown when the server cannot be reached.
// Build for another tailnet with SENTINELA_URL=https://… scripts/build-apk.sh
const url = process.env.SENTINELA_URL ?? "https://sentinela.tail0.ts.net";

const config: CapacitorConfig = {
  appId: "casa.sentinela.app",
  appName: "Sentinela",
  webDir: "www",
  server: {
    url,
    errorPath: "offline.html",
    cleartext: false,
    androidScheme: "https",
  },
  android: {
    appendUserAgent: "SentinelaApp/1",
    allowMixedContent: false,
    // Left unset: Capacitor enables WebView debugging only in debuggable
    // (debug) builds, so "Sentinela (dev)" shows up in chrome://inspect and
    // the release APK does not.
  },
};

export default config;
