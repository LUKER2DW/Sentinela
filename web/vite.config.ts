import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// In dev, the Go API runs on :8080; Vite proxies API, stream WS and health.
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      "/api": { target: "http://127.0.0.1:8080" },
      "/stream": { target: "ws://127.0.0.1:8080", ws: true },
      "/healthz": { target: "http://127.0.0.1:8080" },
    },
  },
  build: { outDir: "dist", emptyOutDir: true, sourcemap: false, target: "es2022" },
});
