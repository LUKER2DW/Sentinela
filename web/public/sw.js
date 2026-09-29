// App-shell cache only. API, stream, recordings and Nuvem files always go to
// the network: video and user files must never be cached on the phone.
// The build id in CACHE is stamped per build (vite.config.ts): every release gets a
// fresh cache and the old one is dropped on activate.
const CACHE = "sentinela-shell-__BUILD_ID__";
const SHELL = ["/", "/index.html", "/manifest.webmanifest", "/icon.svg", "/icon-192.png"];

self.addEventListener("install", (e) => {
  e.waitUntil(caches.open(CACHE).then((c) => c.addAll(SHELL)).then(() => self.skipWaiting()));
});

self.addEventListener("activate", (e) => {
  e.waitUntil(
    caches.keys().then((keys) => Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k)))).then(() => self.clients.claim()),
  );
});

self.addEventListener("fetch", (e) => {
  const url = new URL(e.request.url);
  if (e.request.method !== "GET" || url.origin !== location.origin) return;
  if (url.pathname.startsWith("/api/") || url.pathname.startsWith("/stream/")) return;
  if (url.pathname.startsWith("/assets/")) {
    // Fingerprinted: cache-first.
    e.respondWith(
      caches.match(e.request).then(
        (hit) => hit || fetch(e.request).then((res) => {
          const copy = res.clone();
          caches.open(CACHE).then((c) => c.put(e.request, copy));
          return res;
        }),
      ),
    );
    return;
  }
  // Navigations: network first, shell when offline.
  if (e.request.mode === "navigate") {
    e.respondWith(fetch(e.request).catch(() => caches.match("/index.html")));
  }
});
