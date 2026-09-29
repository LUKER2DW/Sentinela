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

/** 8 MiB: small enough for smooth progress and cheap retries on 4G. */
export const CHUNK = 8 << 20;

export interface UploadProgress {
  sent: number;
  total: number;
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
  let offset = (await get<{ offset: number }>(`/api/nas/upload?${q}`)).offset;
  if (offset > file.size) offset = 0;
  onProgress({ sent: offset, total: file.size });
  let failures = 0;
  do {
    const end = Math.min(offset + CHUNK, file.size);
    try {
      const res = await fetch(`/api/nas/upload?${q}&offset=${offset}&total=${file.size}`, {
        method: "PUT",
        body: file.slice(offset, end),
        headers: { "X-CSRF-Token": getCsrf(), "Content-Type": "application/octet-stream" },
        credentials: "same-origin",
        signal,
      });
      const data = (await res.json().catch(() => ({}))) as { offset?: number; error?: string };
      if (res.status === 409 && typeof data.offset === "number") {
        offset = data.offset; // server knows better where we are
        continue;
      }
      if (!res.ok) throw new ApiError(res.status, data.error ?? `HTTP ${res.status}`);
      offset = data.offset ?? end;
      failures = 0;
      onProgress({ sent: offset, total: file.size });
    } catch (e) {
      if (signal.aborted || (e instanceof ApiError && e.status < 500 && e.status !== 408)) throw e;
      if (++failures > 4) throw e;
      await new Promise((r) => setTimeout(r, 1000 * 2 ** failures));
    }
  } while (offset < file.size);
}
