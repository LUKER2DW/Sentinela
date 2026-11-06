import { syncServerClock } from "./time";
// Typed client for the Sentinela API. The CSRF token comes from /api/me or
// /api/login and is sent on every unsafe request.

export type Role = "admin" | "operator" | "viewer";

export interface User {
  id: number;
  username: string;
  role: Role;
  created_at: string;
  /** Cameras a non-admin may see; empty = all. */
  cameras: string[];
  /** Two-step login (TOTP) is on: required from outside (ADR-0019). */
  totp: boolean;
  /** Switched off by an admin: cannot sign in. */
  disabled?: boolean;
  /** Optional second sign-in name. */
  email?: string;
  /** Authorized, waiting for the first access (invite). */
  pending?: boolean;
  invite_expires?: string;
}

export interface Camera {
  id: string;
  name: string;
  recording: boolean;
  record_audio: boolean;
  retention_days: number;
}

export interface Preset {
  id: number;
  name: string;
}

export type DayNight = "auto" | "color" | "black_white" | "intelligent";

export interface ControlState {
  model: string;
  firmware: string;
  white_light: boolean;
  day_night: DayNight | "";
  presets: Preset[];
  approved: boolean;
  /** Vendor cloud (XMEye P2P) switch; absent when unreadable. */
  cloud_p2p?: boolean;
}

export interface Segment {
  id: number;
  camera_id: string;
  start: string;
  end: string;
  bytes: number;
  protected: boolean;
}

export interface CamEvent {
  id: number;
  camera_id: string;
  type: "motion" | "tamper" | "line" | "intrusion" | string;
  start: string;
  end?: string;
}

/** A group of events (internal/review): what the Eventos page lists. */
export interface ReviewItem {
  id: number;
  camera_id: string;
  start: string;
  end?: string;
  types: string[];
  severity: "alert" | "detection";
  events: number;
  snapshot_event?: number;
  seen: boolean;
}

export interface ReviewList {
  items: ReviewItem[];
  unseen: number;
}

export interface Timeline {
  segments: Segment[];
  events: CamEvent[];
}

export interface RecorderStatus {
  camera_id: string;
  state: string;
  since: string;
  heartbeat: string;
  restarts: number;
  /** Reconnects in the last hour; ≥ 5 is a reconnect storm. */
  restarts_1h: number;
  last_error: string;
  current_file: string;
}

/** Nightly host backup (scripts/backup.sh, ADR-0014). */
export interface BackupStatus {
  updated: string;
  db_ok: boolean;
  db_at: string;
  db_bytes: number;
  restic: "ok" | "off" | "disk_missing" | "failed";
  restic_at: string;
}

export interface SystemInfo {
  version: string;
  started: string;
  now: string;
  host: {
    cpu_pct: number;
    mem_total: number;
    mem_available: number;
    uptime_ns: number;
    load1: number;
    proc_rss: number;
    num_cpu: number;
    supported: boolean;
  };
  disk: { total: number; free: number; used: number; pct: number };
  max_disk_pct: number;
  segment_secs: number;
  storage: { camera_id: string; count: number; bytes: number; oldest: string; newest: string }[];
  daily_bytes: Record<string, number>;
  /** Recording holes > 30 s in the last 24 h, per camera. */
  gaps_24h: Record<string, { count: number; longest_secs: number; recent: { from: string; to: string }[] }>;
  recorders: RecorderStatus[];
  go2rtc: Record<string, { online: boolean; consumers: number }> | null;
  go2rtc_error: string;
  volumes: Volume[];
  nas_host: string;
  disk_health: { updated: string; mounts: Record<string, DiskHealth> } | null;
  backup: BackupStatus | null;
  /** ONVIF event subscription per camera: motion events (and the detector) depend on it. */
  event_watch?: Record<string, { ok: boolean; since: string; error?: string }>;
  detector?: {
    healthy: boolean;
    last_seen?: string;
    status?: {
      inference_ms: number;
      inferences: number;
      model?: string;
      cameras: Record<string, { connected: boolean; analysing: boolean; last_analysis: number | null; parked?: number; cropped?: boolean }>;
    };
  };
}

export interface DiskHealth {
  device: string;
  model: string;
  state: "ok" | "warn" | "fail";
  reallocated: number;
  pending: number;
  uncorrectable: number;
  temperature: number | null;
  power_on_hours: number | null;
  last_selftest: string;
}

export interface Volume {
  id: string;
  name: string;
  share?: string;
  kind: "recordings" | "nas" | string;
  total: number;
  free: number;
  used: number;
  pct: number;
  mounted: boolean;
}

export interface AuditEntry {
  id: number;
  at: string;
  username: string;
  action: string;
  target: string;
  ok: boolean;
  detail: string;
}

export class ApiError extends Error {
  status: number;
  /** The server wants the two-step code (public zones, ADR-0019). */
  mfa: boolean;
  constructor(status: number, message: string, mfa = false) {
    super(message);
    this.status = status;
    this.mfa = mfa;
  }
}

let csrf = "";
export const setCsrf = (t: string) => {
  csrf = t;
};
export const getCsrf = () => csrf;

type Listener = () => void;
const unauthorizedListeners = new Set<Listener>();
export const onUnauthorized = (l: Listener) => {
  unauthorizedListeners.add(l);
  return () => unauthorizedListeners.delete(l);
};

export async function api<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {};
  // A Blob (a profile picture) goes as it is; anything else as JSON.
  const raw = body instanceof Blob;
  if (body !== undefined) headers["Content-Type"] = raw ? body.type || "application/octet-stream" : "application/json";
  if (method !== "GET" && csrf) headers["X-CSRF-Token"] = csrf;
  const res = await fetch(path, {
    method,
    headers,
    body: body === undefined ? undefined : raw ? body : JSON.stringify(body),
    credentials: "same-origin",
  });
  const text = await res.text();
  let data: unknown;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    data = null;
  }
  if (!res.ok) {
    if (res.status === 401 && !path.startsWith("/api/login")) unauthorizedListeners.forEach((l) => l());
    const msg = (data as { error?: string } | null)?.error ?? `HTTP ${res.status}`;
    throw new ApiError(res.status, msg, (data as { mfa?: boolean } | null)?.mfa === true);
  }
  const maybe = data as { csrf?: string } | null;
  if (maybe && typeof maybe.csrf === "string") csrf = maybe.csrf;
  return data as T;
}

export const get = <T>(path: string) => api<T>("GET", path);

/** Binary upload with session, CSRF, readable errors and byte progress. */
export function apiUpload<T>(method: string, path: string, file: Blob, progress?: (percent: number) => void): Promise<T> {
  return new Promise((resolve, reject) => {
    const req = new XMLHttpRequest();
    req.open(method, path);
    req.withCredentials = true;
    req.setRequestHeader("Content-Type", file.type || "application/octet-stream");
    if (csrf) req.setRequestHeader("X-CSRF-Token", csrf);
    req.upload.onprogress = (e) => { if (e.lengthComputable) progress?.(Math.round(e.loaded / e.total * 100)); };
    req.onerror = () => reject(new Error("O envio foi interrompido. Confira a conexão e tente novamente."));
    req.onabort = () => reject(new Error("Envio cancelado."));
    req.onload = () => {
      let data: unknown;
      try { data = JSON.parse(req.responseText); } catch { data = null; }
      if (req.status < 200 || req.status >= 300) {
        if (req.status === 401) unauthorizedListeners.forEach((l) => l());
        reject(new ApiError(req.status, (data as { error?: string } | null)?.error ?? `HTTP ${req.status}`));
      } else resolve(data as T);
    };
    req.send(file);
  });
}

/** GET /api/system, also syncing the UI clock to the server's. */
export async function fetchSystem(): Promise<SystemInfo> {
  const t0 = Date.now();
  const s = await get<SystemInfo>("/api/system");
  syncServerClock(s.now, t0, Date.now());
  return s;
}
