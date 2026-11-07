// The Android Firebase client config is kept with the signing key, outside
// git. The debug app has a different package and deliberately uses the direct
// notification transport; this local debug copy only lets Gradle build it.
// Firebase is optional (ADR-0020): without your own project's file the APK
// is built without it and notifications use the direct connection.
import { existsSync, readFileSync, writeFileSync, mkdirSync, rmSync } from "node:fs";
import { homedir } from "node:os";
import { join, resolve } from "node:path";

const source = process.env.SENTINELA_GOOGLE_SERVICES_JSON || join(homedir(), ".sentinela-android", "google-services.json");
const app = resolve("android", "app");
if (!existsSync(source)) {
  // A stale copy from another build must not sneak in.
  rmSync(join(app, "google-services.json"), { force: true });
  rmSync(join(app, "src", "debug", "google-services.json"), { force: true });
  console.log("No Firebase config: building without Firebase (direct notifications).");
  process.exit(0);
}
const client = JSON.parse(readFileSync(source, "utf8"));
const release = client.client?.find((entry) => entry.client_info?.android_client_info?.package_name === "casa.sentinela.app");
if (!release) throw new Error("Firebase config needs the casa.sentinela.app Android client");

writeFileSync(join(app, "google-services.json"), readFileSync(source));
const debug = structuredClone(client);
debug.client = [structuredClone(release)];
debug.client[0].client_info.android_client_info.package_name = "casa.sentinela.app.dev";
const debugDir = join(app, "src", "debug");
mkdirSync(debugDir, { recursive: true });
writeFileSync(join(debugDir, "google-services.json"), JSON.stringify(debug));
