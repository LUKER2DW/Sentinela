/// <reference types="vitest/config" />
import { defineConfig, type Plugin } from "vite";
import react from "@vitejs/plugin-react";
import { copyFileSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
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

// The H.265 converter's worker and WebAssembly (ADR-0026), served at /hevc/
// as plain files: they are loaded only by browsers that cannot decode H.265.
const HEVC_FILES: Record<string, string> = {
  "transcode-worker.js": "node_modules/@hevcjs/core/dist/transcode-worker.js",
  "hevc-decode.js": "node_modules/@hevcjs/core/dist/wasm/hevc-decode.js",
  "hevc-decode.wasm": "node_modules/@hevcjs/core/dist/wasm/hevc-decode.wasm",
};
function hevcAssets(): Plugin {
  let outDir = "dist";
  return {
    name: "hevc-assets",
    configResolved(c) {
      outDir = c.build.outDir;
    },
    configureServer(server) {
      server.middlewares.use((req, res, next) => {
        const name = req.url?.match(/^\/hevc\/([\w.-]+)(?:\?.*)?$/)?.[1];
        const src = name && HEVC_FILES[name];
        if (!src) return next();
        res.setHeader("Content-Type", name.endsWith(".wasm") ? "application/wasm" : "text/javascript");
        res.end(readFileSync(resolve(src)));
      });
    },
    closeBundle() {
      mkdirSync(resolve(outDir, "hevc"), { recursive: true });
      for (const [name, src] of Object.entries(HEVC_FILES)) copyFileSync(resolve(src), resolve(outDir, "hevc", name));
    },
  };
}

// In dev, the Go API runs on :8080 (SENTINELA_API overrides it); Vite
// proxies API, stream WS and health.
const API = process.env.SENTINELA_API ?? "http://127.0.0.1:8080";
export default defineConfig({
  plugins: [react(), stampServiceWorker(), hevcAssets()],
  server: {
    // The changelog lives at the repository root (Changelog.tsx).
    fs: { allow: [".."] },
    proxy: {
      "/api": { target: API },
      "/stream": { target: API.replace(/^http/, "ws"), ws: true },
      "/healthz": { target: API },
    },
  },
  build: { outDir: "dist", emptyOutDir: true, sourcemap: false, target: "es2022" },
  test: { setupFiles: ["./src/test-setup.ts"] },
});
