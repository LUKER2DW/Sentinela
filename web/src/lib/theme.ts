import { useSyncExternalStore } from "react";

// Light/dark theme on <html data-theme>. First visit follows the OS; an
// explicit choice is remembered per device (localStorage, best effort).
// Two extras on top (round 6): "black" is the dark theme on true black for
// OLED phones (<html data-black>), and a colour palette for the accent and
// the drawings (<html data-palette>).

export type Theme = "light" | "dark";
const KEY = "sentinela-theme";
const BLACK_KEY = "sentinela-black";
const PALETTE_KEY = "sentinela-palette";

export const PALETTES = ["mar", "oceano", "floresta", "areia", "lavanda", "grafite"] as const;
export type Palette = (typeof PALETTES)[number];

function read(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

function write(key: string, v: string | null) {
  try {
    if (v === null) localStorage.removeItem(key);
    else localStorage.setItem(key, v);
  } catch {
    /* private mode: the choice still applies for this visit */
  }
}

function stored(): Theme | null {
  const v = read(KEY);
  return v === "light" || v === "dark" ? v : null;
}

function system(): Theme {
  return window.matchMedia?.("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}

let current: Theme = stored() ?? system();
let black = read(BLACK_KEY) === "1";
let palette: Palette = (PALETTES as readonly string[]).includes(read(PALETTE_KEY) ?? "") ? (read(PALETTE_KEY) as Palette) : "mar";
const subs = new Set<() => void>();
const notify = () => subs.forEach((f) => f());

function apply(t: Theme) {
  const root = document.documentElement;
  root.dataset.theme = t;
  if (t === "dark" && black) root.dataset.black = "1";
  else delete root.dataset.black;
  if (palette === "mar") delete root.dataset.palette;
  else root.dataset.palette = palette;
  const bar = t === "dark" ? (black ? "#000000" : "#181918") : "#f5f3ee";
  document.querySelector('meta[name="theme-color"]')?.setAttribute("content", bar);
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
  if (remember) write(KEY, t);
  notify();
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

export type ThemeMode = Theme | "black" | "system";

/** The choice on the Aparência card: a fixed theme, true black, or follow the device. */
export function useThemeMode(): ThemeMode {
  useTheme(); // re-render on changes
  const s = stored();
  return s === "dark" && black ? "black" : (s ?? "system");
}

export function setThemeMode(m: ThemeMode) {
  black = m === "black";
  write(BLACK_KEY, black ? "1" : null);
  if (m === "system") {
    write(KEY, null);
    return setTheme(system(), false);
  }
  setTheme(m === "black" ? "dark" : m);
}

export function usePalette(): Palette {
  useTheme();
  return useSyncExternalStore(
    (f) => {
      subs.add(f);
      return () => subs.delete(f);
    },
    () => palette,
  );
}

export function setPalette(p: Palette) {
  palette = p;
  write(PALETTE_KEY, p === "mar" ? null : p);
  apply(current);
  notify();
}
