# Sentinela web UI

React 19, Vite and TypeScript SPA, with TanStack Query and plain CSS tokens.
The Go binary serves the production build through `web/embed.go`.
The same SPA runs in desktop/mobile browsers and the Android WebView.

## Development

```bash
npm ci
npm run dev
```

The API must run separately. Vite proxies `/api`, `/stream` and
`/healthz` to `http://127.0.0.1:8080`; use `SENTINELA_API` to change
that target. `npm run build` runs TypeScript and builds `dist/`.
`npm run lint` and `npm test` are part of the repository's `make check`.

## Structure

- `src/App.tsx`: boot/auth state, navigation and responsive shell.
- `src/pages/`: Ao vivo, Gravações, Eventos, Nuvem, Sistema and Minha conta.
- `src/components/`: players, timeline/ruler, camera controls, file browser,
  account cards and notification inbox.
- `src/lib/`: API/CSRF, routing, native bridges, time, media and shared state.
- `src/styles.css`: layout, palette tokens, responsive rules and safe areas.
- `public/sw.js`: cache only the shell; API and stream traffic stays online.

The visual direction is warm paper/graphite, ocean drawings and handwritten
icons. Aparência offers six palettes and light/dark/black/automatic modes.

## Mobile behavior

The bottom navigation uses Mais for account/system settings. Camera
configuration, users and audit stay on desktop. Recording controls fit the
phone width; folders and subpages expose a back button. System-bar spacing
uses both browser safe-area values and Capacitor's injected values.

Nuvem has list/grid modes, previews, filtering and resumable uploads.
Folder selection is desktop-only; it preserves paths of files and their
containing folders. Empty directories are not represented by the file
picker. Video previews decode on the viewing device, not the server.

## Authentication and native functions

All API calls use same-origin cookies and CSRF. Live access uses short-lived
tickets issued by Sentinela; the browser does not call go2rtc directly.
Viewer accounts have no Nuvem access; operators use personal/shared folders.

The `SentinelaNotify` bridge chooses Firebase notifications or the direct
fallback. Firebase receives an opaque id; the app fetches details with its
session. Browsers retain Web Push. The `SentinelaUpdate` bridge checks the
published APK's hash and opens Android's installer.

Native APK builds and the certificate-pin configuration are documented in
[the repository README](../README.md#build-and-publish-android).