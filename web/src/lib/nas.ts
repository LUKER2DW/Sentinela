// Client for the Nuvem file API (/api/nas/*, ADR-0012).
import { ApiError, api, get, getCsrf } from "./api";

export interface NasEntry {
  name: string;
  dir: boolean;
  size: number;
  modified: string;
}

export interface NasListing {
  path: string;
  entries: NasEntry[];
  user: string;
  can_write: boolean;
  /** Whose each item is (personal folders, uploads to the shared folder). */
  owners?: Record<string, string>;
}

export const TRASH = ".lixeira";
export const SHARED = "Compartilhado";

export const joinPath = (dir: string, name: string) => (dir ? `${dir}/${name}` : name);
export const parentPath = (p: string) => p.split("/").slice(0, -1).join("/");
export const baseName = (p: string) => p.split("/").pop() ?? p;

/** Friendly folder labels: the bin and the user's own folder. */
export function label(name: string, user: string): string {
  if (name === TRASH) return "Lixeira";
  if (name === user) return "Meus arquivos";
  return name;
}

/** Breadcrumb trail for a path: [{name, path}], starting at the share. */
export function crumbs(p: string, user: string): { name: string; path: string }[] {
  const parts = p ? p.split("/") : [];
  const out = [{ name: "Nuvem", path: "" }];
  parts.forEach((part, i) => {
    // The bin's per-user level (.lixeira/<user>) is an implementation detail.
    if (i === 1 && parts[0] === TRASH) return;
    out.push({ name: label(part, user), path: parts.slice(0, i + 1).join("/") });
  });
  return out;
}

/** Where an item in the user's bin came from: .lixeira/<user>/<original>. */
export function restorePath(p: string, user: string): string | null {
  const prefix = `${TRASH}/${user}/`;
  return p.startsWith(prefix) && p.length > prefix.length ? p.slice(prefix.length) : null;
}

export const listDir = (p: string) => get<NasListing>(`/api/nas/list?path=${encodeURIComponent(p)}`);
export const fileUrl = (p: string, download = false) => `/api/nas/file?path=${encodeURIComponent(p)}${download ? "&download=1" : ""}`;
export const thumbUrl = (p: string) => `/api/nas/thumb?path=${encodeURIComponent(p)}`;
export const mkdir = (p: string) => api("POST", "/api/nas/mkdir", { path: p });
export const move = (from: string, to: string) => api("POST", "/api/nas/move", { from, to });
/** Overwrites a plain-text file (the viewer's editor). */
export async function saveText(p: string, text: string): Promise<void> {
  const res = await fetch(`/api/nas/text?path=${encodeURIComponent(p)}`, {
    method: "PUT",
    body: text,
    headers: { "X-CSRF-Token": getCsrf(), "Content-Type": "text/plain; charset=utf-8" },
    credentials: "same-origin",
  });
  if (!res.ok) {
    const data = (await res.json().catch(() => ({}))) as { error?: string };
    throw new ApiError(res.status, data.error ?? `HTTP ${res.status}`);
  }
}

/** Creates an empty file (a zero-byte upload). */
export const createEmpty = (p: string) => uploadFile(new File([], baseName(p)), p, () => {}, new AbortController().signal);

export type TypeFilter = "" | "folder" | "image" | "video" | "audio" | "doc" | "other";
export type DateFilter = "" | "today" | "7d" | "30d" | "year";

/** Explorer filters: type group and modification date. */
export function matchesFilters(e: NasEntry, type: TypeFilter, date: DateFilter, now = Date.now()): boolean {
  if (type) {
    const k = kindOf(e);
    const group = k === "pdf" || k === "text" ? "doc" : k === "archive" || k === "file" ? "other" : k;
    if (group !== type) return false;
  }
  if (date) {
    const t = new Date(e.modified).getTime();
    const d = new Date(now);
    const since =
      date === "today"
        ? new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime()
        : date === "year"
          ? new Date(d.getFullYear(), 0, 1).getTime()
          : now - (date === "7d" ? 7 : 30) * 86_400_000;
    if (t < since) return false;
  }
  return true;
}

/** Deletes for good from one's own bin (an item, or the whole bin). */
export const purge = (p: string) => api("DELETE", `/api/nas/trash?path=${encodeURIComponent(p)}`);

export const remove = (p: string) => api<{ trash: string }>("DELETE", `/api/nas/file?path=${encodeURIComponent(p)}`);

const IMAGE = /\.(jpe?g|png|gif|webp)$/i;
const THUMB = /\.(jpe?g|png)$/i;
const VIDEO = /\.(mp4|m4v|webm|mov)$/i;
const AUDIO = /\.(mp3|m4a|ogg|opus|wav)$/i;
const PDF = /\.pdf$/i;
const TEXT = /\.(txt|md|log|csv|tsv|json|xml|ya?ml|ini|conf|cfg|toml|sh|bat|ps1|py|js|ts|tsx|jsx|go|rs|java|c|h|cpp|cs|html?|css|sql|srt|vtt)$/i;
const ARCHIVE = /\.(zip|rar|7z|tar|gz|tgz|bz2|xz)$/i;
export const isImage = (n: string) => IMAGE.test(n);
export const hasThumb = (n: string) => THUMB.test(n);
export const isVideo = (n: string) => VIDEO.test(n);
export const isAudio = (n: string) => AUDIO.test(n);
export const isPdf = (n: string) => PDF.test(n);
export const isText = (n: string) => TEXT.test(n);

export type Kind = "folder" | "image" | "video" | "audio" | "pdf" | "text" | "archive" | "file";

/** What a Nuvem entry is, for icons, previews and the "Tipo" column. */
export function kindOf(e: { name: string; dir: boolean }): Kind {
  if (e.dir) return "folder";
  if (IMAGE.test(e.name)) return "image";
  if (VIDEO.test(e.name)) return "video";
  if (AUDIO.test(e.name)) return "audio";
  if (PDF.test(e.name)) return "pdf";
  if (TEXT.test(e.name)) return "text";
  if (ARCHIVE.test(e.name)) return "archive";
  return "file";
}

export const kindLabel: Record<Kind, string> = {
  folder: "Pasta",
  image: "Imagem",
  video: "Vídeo",
  audio: "Áudio",
  pdf: "PDF",
  text: "Texto",
  archive: "Compactado",
  file: "Arquivo",
};

/** True when `to` is `from` itself or inside it (a folder cannot move into itself). */
export function inside(to: string, from: string): boolean {
  return to === from || to.startsWith(`${from}/`);
}

export type SortKey = "name" | "modified" | "size";

/** Folders first, then by the chosen column. */
export function sortEntries(entries: NasEntry[], key: SortKey, asc: boolean): NasEntry[] {
  const dir = asc ? 1 : -1;
  return [...entries].sort((a, b) => {
    if (a.dir !== b.dir) return a.dir ? -1 : 1;
    let c = 0;
    if (key === "modified") c = a.modified.localeCompare(b.modified);
    else if (key === "size") c = a.size - b.size;
    if (c === 0) c = a.name.localeCompare(b.name, "pt-BR", { numeric: true, sensitivity: "base" });
    return c * dir;
  });
}

/**
 * Upload chunk sizes. The first chunk is small (quick first progress, cheap
 * retry on 4G); later ones grow so each request carries ~6 s of data, which
 * on a fast link means a few large requests instead of many round trips.
 */
export const CHUNK_MIN = 8 << 20;
export const CHUNK_MAX = 128 << 20;
const CHUNK_SECONDS = 6;

/** Next chunk size from the last one's speed: at most 4× growth, whole MiB. */
export function nextChunk(prev: number, bytes: number, ms: number): number {
  const rate = bytes / Math.max(ms, 1) * 1000;
  const want = Math.min(rate * CHUNK_SECONDS, prev * 4, CHUNK_MAX);
  return Math.max(CHUNK_MIN, Math.floor(want / (1 << 20)) * (1 << 20));
}

export interface UploadProgress {
  sent: number;
  total: number;
}

/**
 * Transfer speed over the last few seconds from (time, bytes so far)
 * samples. A stall counts until now, so the speed falls when bytes stop.
 */
export class SpeedMeter {
  private s: { t: number; b: number }[] = [];
  constructor(private windowMs = 5000) {}
  /** Records the bytes sent so far; a drop (cancelled file) starts over. */
  add(t: number, b: number): void {
    if (this.s.length && b < this.s[this.s.length - 1].b) this.s = [];
    this.s.push({ t, b });
    while (this.s.length > 2 && this.s[1].t <= t - this.windowMs) this.s.shift();
  }
  /** Bytes per second; 0 until a second of history exists. */
  rate(now: number): number {
    if (this.s.length < 2) return 0;
    const first = this.s[0];
    const dt = now - first.t;
    return dt < 1000 ? 0 : ((this.s[this.s.length - 1].b - first.b) * 1000) / dt;
  }
}

/**
 * One animation frame of a counter gliding toward its target: covers the
 * gap in ~tauMs (exponential ease), never overshoots, snaps when close.
 */
export function easeToward(shown: number, target: number, dtMs: number, tauMs = 400): number {
  const next = shown + (target - shown) * Math.min(1, dtMs / tauMs);
  return Math.abs(target - next) < 1 ? target : next;
}

/**
 * Byte counts for numbers that keep moving: two decimals below 100 and one
 * below 1000 of the unit (5,72 GB; 523,4 MB), so every few MB shows.
 */
export function fmtBytesFine(b: number): string {
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  let v = b;
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000;
    i++;
  }
  const d = i === 0 ? 0 : v < 100 ? 2 : 1;
  return `${v.toFixed(d).replace(".", ",")} ${units[i]}`;
}

/** "faltam ~7 min", like the big drives: rounded up, no seconds past a minute. */
export function fmtRemaining(ms: number): string {
  if (!isFinite(ms) || ms <= 0) return "";
  if (ms < 60_000) return "falta menos de 1 min";
  const m = Math.ceil(ms / 60_000);
  if (m < 60) return `faltam ~${m} min`;
  const h = Math.floor(m / 60);
  return `faltam ~${h} h${m % 60 ? ` ${m % 60} min` : ""}`;
}

/** How often a running chunk reports bytes on their way (re-renders). */
const PROGRESS_MS = 250;

/**
 * PUTs one chunk. XMLHttpRequest, not fetch: only XHR reports upload
 * progress, so the bar and the speed move while a 128 MiB chunk is sent.
 */
function putChunk(
  url: string,
  body: Blob,
  signal: AbortSignal,
  onBytes: (n: number) => void,
): Promise<{ status: number; data: { offset?: number; error?: string } }> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    let last = 0;
    const abort = () => xhr.abort();
    xhr.open("PUT", url);
    xhr.withCredentials = true;
    xhr.setRequestHeader("X-CSRF-Token", getCsrf());
    xhr.setRequestHeader("Content-Type", "application/octet-stream");
    xhr.upload.onprogress = (e) => {
      const now = performance.now();
      if (now - last >= PROGRESS_MS) {
        last = now;
        onBytes(e.loaded);
      }
    };
    xhr.onload = () => {
      signal.removeEventListener("abort", abort);
      let data = {};
      try {
        data = JSON.parse(xhr.responseText) as object;
      } catch {
        /* empty or proxy error page */
      }
      resolve({ status: xhr.status, data });
    };
    xhr.onerror = xhr.ontimeout = () => {
      signal.removeEventListener("abort", abort);
      reject(new TypeError("network error"));
    };
    xhr.onabort = () => reject(new DOMException("aborted", "AbortError"));
    signal.addEventListener("abort", abort, { once: true });
    xhr.send(body);
  });
}

/**
 * Uploads a file in chunks, resuming from what the server already has
 * (an earlier attempt that dropped). Retries each chunk a few times.
 */
export async function uploadFile(
  file: File,
  target: string,
  onProgress: (p: UploadProgress) => void,
  signal: AbortSignal,
): Promise<void> {
  const q = `path=${encodeURIComponent(target)}`;
  // Only a file bigger than one chunk can have a useful partial upload to
  // resume; small ones skip the extra round trip (a 409 still resyncs).
  let offset = file.size > CHUNK_MIN ? (await get<{ offset: number }>(`/api/nas/upload?${q}`)).offset : 0;
  if (offset > file.size) offset = 0;
  onProgress({ sent: offset, total: file.size });
  let chunk = CHUNK_MIN;
  let failures = 0;
  do {
    const end = Math.min(offset + chunk, file.size);
    const started = performance.now();
    try {
      if (signal.aborted) throw new DOMException("aborted", "AbortError");
      const from = offset;
      const res = await putChunk(`/api/nas/upload?${q}&offset=${offset}&total=${file.size}`, file.slice(offset, end), signal, (n) =>
        onProgress({ sent: from + n, total: file.size }),
      );
      const data = res.data;
      if (res.status === 409 && typeof data.offset === "number") {
        offset = data.offset; // server knows better where we are
        continue;
      }
      if (res.status < 200 || res.status >= 300) throw new ApiError(res.status, data.error ?? `HTTP ${res.status}`);
      chunk = nextChunk(chunk, end - offset, performance.now() - started);
      offset = data.offset ?? end;
      failures = 0;
      onProgress({ sent: offset, total: file.size });
    } catch (e) {
      if (signal.aborted || (e instanceof ApiError && e.status < 500 && e.status !== 408)) throw e;
      if (++failures > 4) throw e;
      chunk = CHUNK_MIN; // the link got worse: retry small
      await new Promise((r) => setTimeout(r, 1000 * 2 ** failures));
      // What reached the disk before the drop is kept: ask where to go on.
      offset = (await get<{ offset: number }>(`/api/nas/upload?${q}`).catch(() => ({ offset }))).offset;
      if (offset > file.size) offset = 0;
    }
  } while (offset < file.size);
}

/** A file picked or dropped, with its path inside the folder that was sent. */
export interface Picked {
  file: File;
  /** "Fotos/2026/a.jpg" for a folder, just the name for a loose file. */
  rel: string;
}

/** From an <input webkitdirectory> (webkitRelativePath) or a plain file list. */
export function pickedFromList(list: FileList | File[]): Picked[] {
  return Array.from(list).map((file) => ({ file, rel: file.webkitRelativePath || file.name }));
}

async function walk(entry: FileSystemEntry, prefix: string, out: Picked[]): Promise<void> {
  const rel = prefix ? `${prefix}/${entry.name}` : entry.name;
  if (entry.isFile) {
    const file = await new Promise<File>((ok, err) => (entry as FileSystemFileEntry).file(ok, err));
    out.push({ file, rel });
  } else if (entry.isDirectory) {
    const reader = (entry as FileSystemDirectoryEntry).createReader();
    // readEntries returns at most ~100 entries per call: read until empty.
    for (;;) {
      const batch = await new Promise<FileSystemEntry[]>((ok, err) => reader.readEntries(ok, err));
      if (!batch.length) break;
      for (const e of batch) await walk(e, rel, out);
    }
  }
}

/** Files dropped on the page, folders walked recursively (Chrome, Edge, Firefox, Safari). */
export async function pickedFromDrop(dt: DataTransfer): Promise<Picked[]> {
  const entries = Array.from(dt.items ?? [])
    .map((i) => (i.kind === "file" ? i.webkitGetAsEntry() : null))
    .filter((e): e is FileSystemEntry => !!e);
  if (!entries.some((e) => e.isDirectory)) return pickedFromList(dt.files);
  const out: Picked[] = [];
  for (const e of entries) await walk(e, "", out);
  return out;
}

/** Folders to create (parents first) for the picked files, relative to the target. */
export function foldersOf(picked: Picked[]): string[] {
  const dirs = new Set<string>();
  for (const p of picked) {
    const parts = p.rel.split("/").slice(0, -1);
    for (let i = 1; i <= parts.length; i++) dirs.add(parts.slice(0, i).join("/"));
  }
  return [...dirs].sort((a, b) => a.split("/").length - b.split("/").length || a.localeCompare(b));
}

/**
 * Sending a folder that already exists here goes into "Nome (2)" instead of
 * mixing with it (the server never overwrites files).
 */
export function renameTop(picked: Picked[], taken: Set<string>): Picked[] {
  const tops = new Set(picked.filter((p) => p.rel.includes("/")).map((p) => p.rel.split("/")[0]));
  const rename = new Map<string, string>();
  for (const t of tops) {
    if (!taken.has(t)) continue;
    let n = 2;
    while (taken.has(`${t} (${n})`)) n++;
    rename.set(t, `${t} (${n})`);
  }
  if (!rename.size) return picked;
  return picked.map((p) => {
    const [top, ...rest] = p.rel.split("/");
    return rest.length && rename.has(top) ? { ...p, rel: [rename.get(top), ...rest].join("/") } : p;
  });
}

/**
 * Files below this go up to four at a time: a folder of photos or phone
 * videos is limited by round trips, not bandwidth. Bigger files go alone.
 */
export const SMALL_UPLOAD = 32 << 20;
export const PARALLEL_UPLOADS = 4;
