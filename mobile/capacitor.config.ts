import type { CapacitorConfig } from "@capacitor/cli";

// The app loads the Sentinela server itself (ADR-0011): same origin, same
// cookie/CSRF auth. Which server URL is picked at run time on the bundled
// start screen (www/index.html) among the ADR-0019 trial paths; MainActivity
// puts it in server.url before the bridge loads (Connection.java). Build
// defaults: SENTINELA_TAILNET_HOST, SENTINELA_LAN_URL, SENTINELA_DOMAIN_URL
// (scripts/build-apk.sh, app/build.gradle).
const config: CapacitorConfig = {
  appId: "casa.sentinela.app",
  appName: "Sentinela",
  webDir: "www",
  server: {
    errorPath: "offline.html",
    androidScheme: "https",
  },
  android: {
    appendUserAgent: "SentinelaApp/1",
    allowMixedContent: false,
  },
};

export default config;
