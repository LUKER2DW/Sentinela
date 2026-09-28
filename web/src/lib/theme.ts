import { useSyncExternalStore } from "react";

// Light/dark theme on <html data-theme>. First visit follows the OS; an
// explicit choice is remembered per device (localStorage, best effort).

export type Theme = "light" | "dark";
const KEY = "sentinela-theme";

function stored(): Theme | null {
  try {
    const v = localStorage.getItem(KEY);
    return v === "light" || v === "dark" ? v : null;
  } catch {
    return null;
  }
}

function system(): Theme {
  return window.matchMedia?.("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}

let current: Theme = stored() ?? system();
const subs = new Set<() => void>();

function apply(t: Theme) {
  document.documentElement.dataset.theme = t;
  document.querySelector('meta[name="theme-color"]')?.setAttribute("content", t === "dark" ? "#161a1c" : "#f5f2ec");
}

/** Call once before first render to avoid a flash of the wrong theme. */
export function initTheme() {
  apply(current);
  window.matchMedia?.("(prefers-color-scheme: dark)").addEventListener("change", () => {
    if (!stored()) setTheme(system(), false);
  });
}

export function setTheme(t: Theme, remember = true) {
  current = t;
  apply(t);
  if (remember) {
    try {
      localStorage.setItem(KEY, t);
    } catch {
      /* private mode: theme still applies for this visit */
    }
  }
  subs.forEach((f) => f());
}

export function useTheme(): Theme {
  return useSyncExternalStore(
    (f) => {
      subs.add(f);
      return () => subs.delete(f);
    },
    () => current,
  );
}
