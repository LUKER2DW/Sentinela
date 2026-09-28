import { useSyncExternalStore } from "react";

// Tiny hash router: #/live, #/recordings?cam=cam1&t=2026-09-28T13:00:00Z, ...

export interface Route {
  page: string;
  params: URLSearchParams;
}

function parse(): Route {
  const raw = window.location.hash.replace(/^#\/?/, "");
  const [page, query = ""] = raw.split("?");
  return { page: page || "live", params: new URLSearchParams(query) };
}

let current = parse();
const listeners = new Set<() => void>();
window.addEventListener("hashchange", () => {
  current = parse();
  listeners.forEach((l) => l());
});

export function useRoute(): Route {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l);
      return () => listeners.delete(l);
    },
    () => current,
  );
}

export function href(page: string, params?: Record<string, string>): string {
  const q = params ? new URLSearchParams(params).toString() : "";
  return `#/${page}${q ? `?${q}` : ""}`;
}

export function navigate(page: string, params?: Record<string, string>) {
  window.location.hash = href(page, params);
}
