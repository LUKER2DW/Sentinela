import { useSyncExternalStore } from "react";

// Time helpers. Storage and API are UTC; the UI shows local time (pt-BR).

export const DAY_MS = 24 * 60 * 60 * 1000;

const timeFmt = new Intl.DateTimeFormat("pt-BR", { hour: "2-digit", minute: "2-digit", second: "2-digit" });
const hmFmt = new Intl.DateTimeFormat("pt-BR", { hour: "2-digit", minute: "2-digit" });
const dayFmt = new Intl.DateTimeFormat("pt-BR", { weekday: "long", day: "numeric", month: "long" });
const shortDayFmt = new Intl.DateTimeFormat("pt-BR", { day: "2-digit", month: "2-digit" });

export const fmtTime = (d: Date | string | number) => timeFmt.format(new Date(d));
export const fmtHM = (d: Date | string | number) => hmFmt.format(new Date(d));
export const fmtDay = (d: Date | string | number) => dayFmt.format(new Date(d));
export const fmtShortDay = (d: Date | string | number) => shortDayFmt.format(new Date(d));

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
  return `${Math.floor(h / 24)} dias`;
}

export function fmtBytes(b: number): string {
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  let v = b;
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000;
    i++;
  }
  return `${v.toFixed(v < 10 && i > 0 ? 1 : 0).replace(".", ",")} ${units[i]}`;
}

export function relative(d: Date | string, now = Date.now()): string {
  const diff = now - new Date(d).getTime();
  if (diff < 60_000) return "agora";
  if (diff < 3_600_000) return `há ${Math.floor(diff / 60_000)} min`;
  if (diff < DAY_MS) return `há ${Math.floor(diff / 3_600_000)} h`;
  return `há ${Math.floor(diff / DAY_MS)} dias`;
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

export const dayNightLabel: Record<string, string> = {
  auto: "Automático",
  color: "Colorido",
  black_white: "Preto e branco",
  intelligent: "Inteligente",
};

export const eventLabel: Record<string, string> = {
  motion: "Movimento",
  tamper: "Obstrução",
  line: "Linha cruzada",
  intrusion: "Invasão de área",
};

// ---------- shared clock ----------

let nowValue = Date.now();
const clockSubs = new Set<() => void>();
setInterval(() => {
  nowValue = Date.now();
  clockSubs.forEach((f) => f());
}, 1000);

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
