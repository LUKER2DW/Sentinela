import { useCallback, useSyncExternalStore } from "react";
import type { SystemInfo } from "./api";
import { fmtDuration, fmtTime } from "./time";

import { t } from "./i18n";
// System notices (disk wear, recording gaps, vendor cloud, backups…). Each
// has a signature of what it says: marking it read hides it until the
// situation changes (a new gap, another disk), then it comes back unread.

export interface Notice {
  id: string;
  /** Changes when the content changes: a read notice with a new sig is unread again. */
  sig: string;
  level: "error" | "warn";
  title: string;
  text: string;
  /** System section that explains it. */
  section: string;
}

/** Reconnects in one hour that count as a storm (matches the recorder log). */
const STORM = 5;

export function buildNotices(s: SystemInfo, camName: (id: string) => string, cloudOn: string[]): Notice[] {
  const out: Notice[] = [];
  const names: Record<string, string> = { "/data": t("recordings"), "/srv/nas": t("Cloud"), "/srv/backup": "backups" };
  for (const [m, h] of Object.entries(s.disk_health?.mounts ?? {})) {
    if (m === "/" || h.state === "ok") continue;
    out.push({
      id: `disk:${m}`,
      sig: `${h.state}:${h.uncorrectable}:${h.pending}:${h.reallocated}`,
      level: h.state === "fail" ? "error" : "warn",
      title: t("Disk {v0} showing wear", { v0: names[m] ?? m }),
      text: t("{model}: {uncorrectable} read errors, {pending} pending sectors. Replace this disk when you can; recordings continue, but may lose clips if it gets worse.", { model: h.model, uncorrectable: h.uncorrectable, pending: h.pending }),
      section: "server",
    });
  }
  for (const [id, g] of Object.entries(s.gaps_24h ?? {})) {
    if (!g.count) continue;
    const last = g.recent[g.recent.length - 1];
    out.push({
      id: `gaps:${id}`,
      sig: `${g.count}:${last?.from ?? ""}`,
      level: "warn",
      title: t("{v0} stopped recording {count}× in the last 24 h", { v0: camName(id), count: g.count }),
      text: t("{v0}{v1}. Check that the camera is on and on the network; recording continues by itself when it comes back.", { v0: last ? t("The last one, from {v0} to {v1}", { v0: fmtTime(last.from), v1: fmtTime(last.to) }) : "", v1: g.longest_secs ? ` (maior: ${fmtDuration(g.longest_secs * 1000)})` : "" }),
      section: "recording",
    });
  }
  for (const r of s.recorders) {
    if (r.restarts_1h < STORM) continue;
    out.push({
      id: `storm:${r.camera_id}`,
      sig: String(Math.floor(r.restarts_1h / STORM)),
      level: "error",
      title: t("{v0} reconnected {restarts_1h}× in the last hour", { v0: camName(r.camera_id), restarts_1h: r.restarts_1h }),
      text: r.last_error || t("The camera keeps dropping and coming back. Check its network and power."),
      section: "recording",
    });
  }
  for (const id of cloudOn) {
    out.push({
      id: `cloud:${id}`,
      sig: "on",
      level: "warn",
      title: t("{v0} still uses the manufacturer's cloud", { v0: camName(id) }),
      text: t("It connects to XMEye's P2P service over the internet. Turn off \"Cloud\"/\"P2P\" in the camera's network settings and block its outgoing traffic on the router."),
      section: "cameras",
    });
  }
  const b = s.backup;
  if (b) {
    const stale = !b.db_at || new Date(s.now).getTime() - new Date(b.db_at).getTime() > 36 * 3_600_000;
    if (stale || !b.db_ok)
      out.push({
        id: "backup:db",
        sig: b.db_at || "never",
        level: "warn",
        title: t("The nightly database backup is out of date"),
        text: t("The database backup (every night) has not run in the last 36 h. Check the sentinela-backup service on the server."),
        section: "server",
      });
    if (b.restic === "failed" || b.restic === "disk_missing")
      out.push({
        id: "backup:restic",
        sig: b.restic,
        level: "warn",
        title: b.restic === "failed" ? t("The copy to the external disk failed") : t("The external backup disk is disconnected"),
        text: t("Recordings continue; only the external backup copy is stopped."),
        section: "server",
      });
  }
  if (s.go2rtc_error)
    out.push({
      id: "go2rtc",
      sig: "down",
      level: "error",
      title: t("Live video service is down"),
      text: t("Live does not open until go2rtc comes back. Recordings do not depend on it and continue."),
      section: "cameras",
    });
  return out;
}

// ---------- read state (per device) ----------

const KEY = "sentinela.noticesRead";
const subs = new Set<() => void>();
let cache: Record<string, string> | null = null;

function load(): Record<string, string> {
  if (cache) return cache;
  try {
    cache = JSON.parse(localStorage.getItem(KEY) ?? "{}") as Record<string, string>;
  } catch {
    cache = {};
  }
  return cache;
}

function save(next: Record<string, string>) {
  cache = next;
  try {
    localStorage.setItem(KEY, JSON.stringify(next));
  } catch {
    /* private mode: read state lasts until reload */
  }
  subs.forEach((f) => f());
}

export function isRead(n: Notice, read: Record<string, string>): boolean {
  return read[n.id] === n.sig;
}

/** Read state of the notices on this device, and setters. */
export function useNoticeReads() {
  const read = useSyncExternalStore(
    (cb) => {
      subs.add(cb);
      return () => subs.delete(cb);
    },
    load,
    load,
  );
  const mark = useCallback((list: Notice[], on = true) => {
    const next = { ...load() };
    for (const n of list) {
      if (on) next[n.id] = n.sig;
      else delete next[n.id];
    }
    save(next);
  }, []);
  return { read, mark };
}
