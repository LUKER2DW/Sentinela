import { useSyncExternalStore } from "react";

// Tiny hash router: #/live, #/recordings?cam=cam1&t=2026-09-28T13:00:00Z, ...

export interface Route {
  page: string;
  params: URLSearchParams;
}

// Page names before 2026-11 (Portuguese): bookmarks, invites already sent
// and notifications that still carry them open the same page.
const OLD_NAMES: Record<string, string> = { nuvem: "cloud", servicos: "services", conta: "account", relogio: "clock", convite: "invite" };

function parse(): Route {
  const raw = window.location.hash.replace(/^#\/?/, "");
  const [page, query = ""] = raw.split("?");
  return { page: OLD_NAMES[page] ?? (page || "live"), params: new URLSearchParams(query) };
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

/**
 * Goes to a page. Staying on the same page (a new time on the timeline, a
 * folder in the Cloud…) replaces the history entry, so the phone's back
 * button returns to the previous page instead of every pick.
 */
export function navigate(page: string, params?: Record<string, string>, opts?: { push?: boolean }) {
  const to = href(page, params);
  if (!opts?.push && page === current.page) {
    history.replaceState(history.state, "", to);
    current = parse();
    listeners.forEach((l) => l());
  } else {
    window.location.hash = to;
  }
}
