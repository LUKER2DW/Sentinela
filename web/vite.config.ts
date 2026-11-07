import { defineConfig, type Plugin } from "vite";
import react from "@vitejs/plugin-react";
import { readFileSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";

// Stamps a build id into the service worker's cache name, so each release
// replaces the cached app shell (public/sw.js).
function stampServiceWorker(): Plugin {
  let outDir = "dist";
  return {
    name: "stamp-sw",
    apply: "build",
    configResolved(c) {
      outDir = c.build.outDir;
    },
    closeBundle() {
      const file = resolve(outDir, "sw.js");
      const id = Date.now().toString(36);
      writeFileSync(file, readFileSync(file, "utf8").replaceAll("__BUILD_ID__", id));
    },
  };
}

// In dev, the Go API runs on :8080 (SENTINELA_API overrides it); Vite
// proxies API, stream WS and health.
const API = process.env.SENTINELA_API ?? "http://127.0.0.1:8080";
export default defineConfig({
  plugins: [react(), stampServiceWorker()],
  server: {
    proxy: {
      "/api": { target: API },
      "/stream": { target: API.replace(/^http/, "ws"), ws: true },
      "/healthz": { target: API },
    },
  },
  build: { outDir: "dist", emptyOutDir: true, sourcemap: false, target: "es2022" },
});
