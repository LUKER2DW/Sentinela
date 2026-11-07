import { useSyncExternalStore } from "react";
import { fmtNum, getLang, plural, t, translated } from "./i18n";

// Time helpers. Storage and API are UTC; the UI shows local time in the UI
// language.

export const DAY_MS = 24 * 60 * 60 * 1000;

const formats = {
  time: { hour: "2-digit", minute: "2-digit", second: "2-digit" },
  hm: { hour: "2-digit", minute: "2-digit" },
  day: { weekday: "long", day: "numeric", month: "long" },
  shortDay: { day: "2-digit", month: "2-digit" },
  date: { day: "numeric", month: "short", year: "numeric" },
} satisfies Record<string, Intl.DateTimeFormatOptions>;

const cache = new Map<string, Intl.DateTimeFormat>();
function fmt(kind: keyof typeof formats): Intl.DateTimeFormat {
  const key = `${getLang()}:${kind}`;
  let f = cache.get(key);
  if (!f) {
    f = new Intl.DateTimeFormat(getLang(), formats[kind]);
    cache.set(key, f);
  }
  return f;
}

export const fmtTime = (d: Date | string | number) => fmt("time").format(new Date(d));
export const fmtHM = (d: Date | string | number) => fmt("hm").format(new Date(d));
export const fmtDay = (d: Date | string | number) => fmt("day").format(new Date(d));
export const fmtShortDay = (d: Date | string | number) => fmt("shortDay").format(new Date(d));
/** "29 de set. de 2026": file dates in the Cloud. */
export const fmtDate = (d: Date | string | number) => fmt("date").format(new Date(d));
/** Date and time in the UI language. */
export const fmtDateTime = (d: Date | string | number, style: "short" | "medium" = "medium") =>
  new Date(d).toLocaleString(getLang(), { dateStyle: style, timeStyle: style });

/** Local midnight of the day containing d. */
export function startOfDay(d: Date | number): Date {
  const x = new Date(d);
  x.setHours(0, 0, 0, 0);
  return x;
}

export function addDays(d: Date, n: number): Date {
  const x = new Date(d);
  x.setDate(x.getDate() + n);
  return x;
}

export function sameDay(a: Date, b: Date): boolean {
  return startOfDay(a).getTime() === startOfDay(b).getTime();
}

/** "2 h 05 min", "45 s" */
export function fmtDuration(ms: number): string {
  const s = Math.max(0, Math.round(ms / 1000));
  if (s < 60) return `${s} s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m} min${s % 60 ? ` ${String(s % 60).padStart(2, "0")} s` : ""}`;
  const h = Math.floor(m / 60);
  if (h < 48) return `${h} h ${String(m % 60).padStart(2, "0")} min`;
  return plural(Math.floor(h / 24), "{n} day", "{n} days");
}

/** Stopwatch text: 0:07, 12:45, 1:02:03. */
export function fmtClock(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000));
  const p = (n: number) => String(n).padStart(2, "0");
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  return h ? `${h}:${p(m)}:${p(s % 60)}` : `${m}:${p(s % 60)}`;
}

export function fmtBytes(b: number): string {
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  let v = b;
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000;
    i++;
  }
  const digits = v < 10 && i > 0 ? 1 : 0;
  return `${fmtNum(v, digits, digits)} ${units[i]}`;
}

export function relative(d: Date | string, now = serverNow()): string {
  const diff = now - new Date(d).getTime();
  if (diff < 60_000) return t("now");
  if (diff < 3_600_000) return t("{n} min ago", { n: Math.floor(diff / 60_000) });
  if (diff < DAY_MS) return t("{n} h ago", { n: Math.floor(diff / 3_600_000) });
  return plural(Math.floor(diff / DAY_MS), "{n} day ago", "{n} days ago");
}

/** Merge touching/overlapping [start,end] ranges (ms), gap tolerance in ms. */
export function mergeRanges(ranges: [number, number][], tolerance = 15_000): [number, number][] {
  const sorted = [...ranges].sort((a, b) => a[0] - b[0]);
  const out: [number, number][] = [];
  for (const r of sorted) {
    const last = out[out.length - 1];
    if (last && r[0] <= last[1] + tolerance) last[1] = Math.max(last[1], r[1]);
    else out.push([r[0], r[1]]);
  }
  return out;
}

export const dayNightLabel: Record<string, string> = translated({
  auto: "Automatic",
  color: "Color",
  black_white: "Black and white",
  intelligent: "Smart",
});

export const eventLabel: Record<string, string> = translated({
  motion: "Motion",
  human: "Person",
  person: "Person",
  vehicle: "Vehicle",
  animal: "Animal",
  tamper: "Tampering",
  line: "Line crossed",
  intrusion: "Area intrusion",
});

// ---------- shared clock ----------

// The UI clock follows the server (NTP-synced), not the device: a phone or PC
// that is a few seconds off would otherwise make the live view look late next
// to the camera's on-screen clock.
let offset = 0;
let nowValue = Date.now();
const clockSubs = new Set<() => void>();
setInterval(() => {
  nowValue = Date.now() + offset;
  clockSubs.forEach((f) => f());
}, 1000);

/** Record the server time seen by a request sent at t0 and answered at t1 (device ms). */
export function syncServerClock(serverIso: string, t0: number, t1: number) {
  const server = new Date(serverIso).getTime();
  if (!Number.isFinite(server) || t1 - t0 > 2000) return; // slow round trip: skip
  offset = server - (t0 + t1) / 2;
}

/** Device time corrected to the server clock. */
export function serverNow(): number {
  return Date.now() + offset;
}

/** Current time, updated every second (one shared timer). */
export function useNow(): number {
  return useSyncExternalStore(
    (f) => {
      clockSubs.add(f);
      return () => clockSubs.delete(f);
    },
    () => nowValue,
  );
}

/** Days between the oldest start and the newest end (one decimal); null if empty. */
export function spanDays(ranges: { oldest: string; newest: string }[]): number | null {
  if (ranges.length === 0) return null;
  const oldest = Math.min(...ranges.map((x) => new Date(x.oldest).getTime()));
  const newest = Math.max(...ranges.map((x) => new Date(x.newest).getTime()));
  return Math.max(0, Math.round(((newest - oldest) / 86_400_000) * 10) / 10);
}
