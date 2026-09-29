import { useCallback, useSyncExternalStore } from "react";
import type { SystemInfo } from "./api";
import { fmtDuration, fmtTime } from "./time";

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
  const names: Record<string, string> = { "/data": "gravações", "/srv/nas": "Nuvem", "/srv/backup": "backups" };
  for (const [m, h] of Object.entries(s.disk_health?.mounts ?? {})) {
    if (m === "/" || h.state === "ok") continue;
    out.push({
      id: `disk:${m}`,
      sig: `${h.state}:${h.uncorrectable}:${h.pending}:${h.reallocated}`,
      level: h.state === "fail" ? "error" : "warn",
      title: `Disco de ${names[m] ?? m} com sinais de desgaste`,
      text: `${h.model}: ${h.uncorrectable} leituras com erro, ${h.pending} setores pendentes. Troque esse disco quando puder; as gravações continuam, mas podem perder trechos se ele piorar.`,
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
      title: `${camName(id)} ficou sem gravar ${g.count}× nas últimas 24 h`,
      text: `${last ? `A última de ${fmtTime(last.from)} a ${fmtTime(last.to)}` : ""}${g.longest_secs ? ` (maior: ${fmtDuration(g.longest_secs * 1000)})` : ""}. Veja se a câmera está ligada e na rede; a gravação continua sozinha quando ela volta.`,
      section: "recording",
    });
  }
  for (const r of s.recorders) {
    if (r.restarts_1h < STORM) continue;
    out.push({
      id: `storm:${r.camera_id}`,
      sig: String(Math.floor(r.restarts_1h / STORM)),
      level: "error",
      title: `${camName(r.camera_id)} reconectou ${r.restarts_1h}× na última hora`,
      text: r.last_error || "A câmera está caindo e voltando. Verifique a rede e a alimentação dela.",
      section: "recording",
    });
  }
  for (const id of cloudOn) {
    out.push({
      id: `cloud:${id}`,
      sig: "on",
      level: "warn",
      title: `${camName(id)} ainda usa a nuvem do fabricante`,
      text: "Ela se conecta ao serviço P2P da XMEye pela internet. Desligue “Nuvem”/“P2P” nas configurações de rede da câmera e bloqueie a saída dela no roteador.",
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
        title: "A cópia noturna do banco não está em dia",
        text: "O backup do banco de dados (todas as noites) não rodou nas últimas 36 h. Veja o serviço sentinela-backup no servidor.",
        section: "server",
      });
    if (b.restic === "failed" || b.restic === "disk_missing")
      out.push({
        id: "backup:restic",
        sig: b.restic,
        level: "warn",
        title: b.restic === "failed" ? "A cópia para o disco externo falhou" : "O disco externo de backup está desconectado",
        text: "As gravações continuam; só a cópia de segurança externa está parada.",
        section: "server",
      });
  }
  if (s.go2rtc_error)
    out.push({
      id: "go2rtc",
      sig: "down",
      level: "error",
      title: "Serviço de vídeo ao vivo fora do ar",
      text: "O ao vivo não abre até o go2rtc voltar. As gravações não dependem dele e continuam.",
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
