import { useSyncExternalStore } from "react";

// Collapsed/expanded sidebar, remembered per device (best effort).
const KEY = "sentinela-sidebar";
let collapsed = (() => {
  try {
    return localStorage.getItem(KEY) === "collapsed";
  } catch {
    return false;
  }
})();
const subs = new Set<() => void>();

export function toggleSidebar() {
  collapsed = !collapsed;
  try {
    localStorage.setItem(KEY, collapsed ? "collapsed" : "expanded");
  } catch {
    /* ignore */
  }
  subs.forEach((f) => f());
}

export function useSidebarCollapsed(): boolean {
  return useSyncExternalStore(
    (f) => {
      subs.add(f);
      return () => subs.delete(f);
    },
    () => collapsed,
  );
}
