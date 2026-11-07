// Client for the self-hosted services API (/api/hosting/*, ADR-0021). The
// API forwards to the apps agent; admins only, changes from home/tailnet.
import { useQuery } from "@tanstack/react-query";
import { api, apiUpload, get } from "./api";

import { getLang, t } from "./i18n";
/** Optional parts of the dashboard for this user (GET /api/me features). */
function useFeatures() {
  return useQuery({
    queryKey: ["me", "features"],
    queryFn: () => get<{ features?: { hosting?: boolean; cloud?: boolean; setup_guide?: boolean } }>("/api/me"),
    staleTime: 5 * 60_000,
  });
}

/** Whether this user may open the Serviços page (admin, platform on). */
export function useHostingEnabled(): boolean {
  return !!useFeatures().data?.features?.hosting;
}

/**
 * Whether the server has the Cloud (a disk at /srv/nas). Skipped in the setup
 * guide = not set up = the tab stays hidden. True while loading, so the page
 * does not flicker away on a reload.
 */
export function useCloudEnabled(): boolean {
  return useFeatures().data?.features?.cloud !== false;
}

/** Whether the first-run setup guide should open (admin of a new install). */
export function useSetupGuidePending(): boolean {
  return !!useFeatures().data?.features?.setup_guide;
}

export type Source = "github" | "local" | "zip";
export type Mode = "script" | "dockerfile" | "compose";
export type Exposure = "local" | "lan" | "tailnet" | "public";
export type State = "never" | "deploying" | "running" | "partial" | "stopped" | "exited" | "crashloop" | "waiting" | "failed";

export interface EnvVar {
  key: string;
  value: string;
  secret?: boolean;
}

export interface Port {
  service?: string;
  container: number;
  host: number;
  proto: "tcp" | "udp";
}

export interface Deployment {
  commit: string;
  message?: string;
  ref: string;
  version: string;
  image: string;
  release: string;
  at: string;
  services?: string[];
}

export interface Job {
  kind: "deploy" | "recreate";
  phase: "queued" | "fetch" | "build" | "start" | "done" | "failed";
  started: string;
  finished?: string;
  version?: string;
  error?: string;
}

export interface AppConfig {
  id: string;
  name: string;
  source: Source;
  repo?: string;
  ref?: string;
  path?: string;
  dir?: string;
  mode: Mode;
  runtime?: string;
  compose_file?: string;
  setup?: string;
  start?: string;
  env: EnvVar[];
  ports: Port[];
  exposure: Exposure;
  memory_mb: number;
  cpus: number;
  autostart: boolean;
  root: boolean;
}

export interface Service extends AppConfig {
  managed: boolean;
  desired: "running" | "stopped";
  deployed?: Deployment;
  needs_deploy: boolean;
  created_at: string;
  updated_at: string;
  status: {
    state: State;
    since?: string;
    exit_code?: number;
    oom_killed?: boolean;
    error?: string;
    job?: Job;
    running?: number;
    total?: number;
    cpu_pct: number;
    mem_bytes: number;
    mem_limit: number;
    net_rx: number;
    net_tx: number;
    pids: number;
    disk_bytes: number;
    last_exit?: { at: string; code: number; oom: boolean; reason: string; container?: string };
    restarts: number;
    retry_at?: string;
    events?: { at: string; kind: string; message: string }[];
  };
  links: { kind: "lan" | "tailnet" | "public" | "local" | "internal"; url: string; service?: string }[];
}

export interface HostingInfo {
  docker: string;
  docker_error: string;
  hosts: { lan: string; tailnet: string; public: string };
  port_first: number;
  port_last: number;
  runtimes: Record<string, string>;
  local_root: string;
  limits: {
    memory_mb: number;
    max_memory_mb: number;
    total_memory_mb: number;
    cpus: number;
    max_cpus: number;
    build_cpus: number;
    build_memory_mb: number;
    max_apps: number;
  };
  used_memory_mb: number;
  apps: number;
  mem_free_mb: number;
  github_token: boolean;
  boot_waiting: boolean;
  max_upload_bytes: number;
}

export interface Detection {
  env_keys?: string[];
  mode: Mode;
  runtime?: string;
  setup?: string;
  start?: string;
  port?: number;
  compose_file?: string;
  services?: { name: string; image?: string; build?: boolean; ports?: number[] }[];
  found: string[];
  notes?: string[];
  version?: { Commit: string; Message: string; Branch: string; Label: string };
}

export interface LocalFolder {
  name: string;
  path: string;
  git: boolean;
  markers?: string[];
}

const base = "/api/hosting";
const enc = encodeURIComponent;

export type FileScope = "project" | "data";
export interface ProjectFile { name: string; path: string; dir: boolean; bytes: number; modified: string; secret: boolean }
export interface FileDocument { kind: "text" | "env"; revision: string; content?: string; env?: EnvVar[] }
export interface ServiceSecrets { env: EnvVar[]; files: string[] | null; encrypted: boolean; revision: string }
const filesURL = (id: string, scope: FileScope, path: string, action = "") =>
  `${base}/apps/${enc(id)}/files${action}?scope=${scope}&path=${enc(path)}`;

export const hosting = {
  info: () => get<HostingInfo>(`${base}/info`),
  list: () => get<Service[]>(`${base}/apps`),
  get: (id: string) => get<Service>(`${base}/apps/${enc(id)}`),
  create: (cfg: Partial<AppConfig>, deploy: boolean) => api<Service>("POST", `${base}/apps`, { ...cfg, deploy }),
  update: (id: string, cfg: AppConfig) => api<Service>("PUT", `${base}/apps/${enc(id)}`, cfg),
  remove: (id: string) => api<{ ok: boolean }>("DELETE", `${base}/apps/${enc(id)}`),
  action: (id: string, action: "deploy" | "start" | "stop" | "restart") => api<Service>("POST", `${base}/apps/${enc(id)}/${action}`),
  check: (id: string) =>
    api<{ latest: string; commit: string; message: string; branch: string; deployed: string; update: boolean }>("POST", `${base}/apps/${enc(id)}/check`),
  logs: (id: string, tail = 300) => get<{ lines: { s: number; t: string; svc?: string }[] }>(`${base}/apps/${enc(id)}/logs?tail=${tail}`),
  deployLog: (id: string) => get<{ job: Job | null; lines: string[] }>(`${base}/apps/${enc(id)}/deploy-log`),
  inspect: (src: Pick<AppConfig, "source" | "repo" | "ref" | "path" | "dir">) => api<Detection>("POST", `${base}/inspect`, src),
  local: (path: string) => get<{ root: string; path: string; folders: LocalFolder[] }>(`${base}/local?path=${enc(path)}`),
  setToken: (token: string) => api<{ ok: boolean }>("PUT", `${base}/github-token`, { token }),
  dropToken: () => api<{ ok: boolean }>("DELETE", `${base}/github-token`),
  upload: (file: File, progress?: (percent: number) => void) => {
    if (!file.name.toLowerCase().endsWith(".zip")) return Promise.reject(new Error(t("Choose a .zip file.")));
    if (file.size > 300 * 1024 * 1024) return Promise.reject(new Error(t("The ZIP must be at most 300 MB.")));
    return apiUpload<{ path: string; bytes: number }>("POST", `${base}/uploads`, file, progress);
  },
  dropUpload: (path: string) => api<{ ok: boolean }>("DELETE", `${base}/uploads/${enc(path)}`),
  secrets: (id: string) => get<ServiceSecrets>(`${base}/apps/${enc(id)}/secrets`),
  saveSecrets: (id: string, env: EnvVar[], revision: string) => api<{ ok: boolean; restarting: boolean }>("PUT", `${base}/apps/${enc(id)}/secrets`, { env, revision }),
  files: (id: string, scope: FileScope, path: string) => get<{ entries: ProjectFile[] }>(filesURL(id, scope, path)),
  readFile: (id: string, scope: FileScope, path: string) => get<FileDocument>(filesURL(id, scope, path, "/text")),
  writeFile: (id: string, scope: FileScope, path: string, doc: FileDocument) => api<{ ok: boolean; secrets_imported?: boolean }>("PUT", filesURL(id, scope, path, "/text"), doc),
  mkdir: (id: string, scope: FileScope, path: string) => api<{ ok: boolean }>("POST", filesURL(id, scope, path, "/folder")),
  deleteFile: (id: string, scope: FileScope, path: string) => api<{ ok: boolean }>("DELETE", filesURL(id, scope, path)),
  uploadFile: (id: string, scope: FileScope, path: string, file: File, progress?: (percent: number) => void) => {
    if (file.size > 50 * 1024 * 1024) return Promise.reject(new Error(t("Each file must be at most 50 MB.")));
    return apiUpload<{ ok: boolean; secrets_imported?: boolean }>("PUT", filesURL(id, scope, path, "/upload"), file, progress);
  },
};

// ---------- pure helpers (tested) ----------

export const stateLabel: Record<State, string> = {
  never: t("Not deployed yet"),
  deploying: t("Deploying"),
  running: t("Running"),
  partial: t("Partly running"),
  stopped: t("Stopped"),
  exited: t("Stopped by itself"),
  crashloop: t("Crashing repeatedly"),
  waiting: t("Waiting for the server to start"),
  failed: t("Deploy failed"),
};

/** The buoy colour of a state: ok, busy, bad or idle. */
export function stateTone(s: State): "ok" | "busy" | "bad" | "idle" {
  switch (s) {
    case "running":
      return "ok";
    case "deploying":
    case "waiting":
      return "busy";
    case "exited":
    case "crashloop":
    case "partial":
    case "failed":
      return "bad";
  }
  return "idle";
}

export const phaseLabel: Record<Job["phase"], string> = {
  queued: t("Queued"),
  fetch: t("Fetching the code"),
  build: t("Preparing"),
  start: t("Starting"),
  done: t("Done"),
  failed: t("Failed"),
};

export const exposureInfo: Record<Exposure, { label: string; hint: string }> = {
  local: { label: t("Server only"), hint: t("No other device can reach it. Good for bots and jobs.") },
  lan: { label: t("Home and Tailscale"), hint: t("Devices on the home network and on your Tailscale.") },
  tailnet: { label: t("Tailscale only"), hint: t("Only devices connected to your Tailscale.") },
  public: { label: "Internet", hint: t("Anyone, over the public IP, with the port open on the router.") },
};

export const linkLabel: Record<Service["links"][number]["kind"], string> = {
  lan: t("Home network"),
  tailnet: "Tailscale",
  public: "Internet",
  local: t("On the server"),
  internal: t("Between services"),
};

/** Accepts "owner/repo", a github.com URL or an SSH remote. */
export function normalizeRepo(s: string): string {
  let r = s.trim();
  for (const p of ["https://github.com/", "http://github.com/", "github.com/", "git@github.com:"]) {
    if (r.startsWith(p)) {
      r = r.slice(p.length);
      break;
    }
  }
  return r.replace(/\/+$/, "").replace(/\.git$/, "");
}

export const validRepo = (s: string) => /^[A-Za-z0-9][A-Za-z0-9-]{0,38}\/[A-Za-z0-9._-]{1,100}$/.test(normalizeRepo(s));

/** Where the code comes from, in a few words. */
export function sourceText(a: Pick<AppConfig, "source" | "repo" | "ref" | "path" | "dir">): string {
  const where = a.source === "zip" ? t("Project uploaded as ZIP") : a.source === "local" ? t("folder {path}", { path: a.path }) : `${a.repo}${a.ref && a.ref !== "main" ? ` @ ${a.ref}` : ""}`;
  return a.dir ? `${where} / ${a.dir}` : where;
}

/** Short version text: a commit's 7 first characters, or the snapshot name. */
export function versionText(d?: Pick<Deployment, "commit" | "version">): string {
  if (!d) return "";
  return d.commit ? d.commit.slice(0, 7) : d.version.replace(/^local-/, t("folder "));
}

/** A log line "2026-…Z text" split into its local time and text. */
export function splitLogLine(line: string): { time: string; text: string } {
  const m = /^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})(\.\d+)?Z\s?(.*)$/.exec(line);
  if (!m) return { time: "", text: line };
  const d = new Date(`${m[1]}Z`);
  const time = Number.isNaN(d.getTime()) ? "" : d.toLocaleTimeString(getLang(), { hour: "2-digit", minute: "2-digit", second: "2-digit" });
  return { time, text: m[3] };
}

/** Starting commands for a runtime when nothing was detected. */
export const runtimeDefaults: Record<string, { setup: string; start: string; port: number }> = {
  node: { setup: "npm ci || npm install", start: "npm start", port: 3000 },
  "node-alpine": { setup: "npm ci || npm install", start: "npm start", port: 3000 },
  python: {
    setup: "python -m venv .venv && .venv/bin/pip install -r requirements.txt",
    start: ".venv/bin/python main.py",
    port: 8000,
  },
  go: { setup: "go build -o .bin/app .", start: "./.bin/app", port: 8080 },
  bun: { setup: "bun install", start: "bun run start", port: 3000 },
  deno: { setup: "", start: "deno run -A main.ts", port: 8000 },
  static: { setup: "", start: "", port: 8080 },
  shell: { setup: "", start: "sh ./start.sh", port: 8080 },
};

export const runtimeLabel: Record<string, string> = {
  node: "Node.js",
  "node-alpine": "Node.js Alpine",
  python: "Python",
  go: "Go",
  bun: "Bun",
  deno: "Deno",
  static: t("Static site"),
  shell: "Shell (Alpine)",
};

/** The service's settings, without the status fields (what PUT takes). */
export function configOf(s: Service): AppConfig {
  return {
    id: s.id,
    name: s.name,
    source: s.source,
    repo: s.repo,
    ref: s.ref,
    path: s.path,
    dir: s.dir,
    mode: s.mode,
    runtime: s.runtime,
    compose_file: s.compose_file,
    setup: s.setup,
    start: s.start,
    env: s.env.map((e) => ({ ...e })),
    ports: s.ports.map((p) => ({ ...p })),
    exposure: s.exposure,
    memory_mb: s.memory_mb,
    cpus: s.cpus,
    autostart: s.autostart,
    root: s.root,
  };
}

/** Problems that keep a form from being saved (pt-BR, for people). */
export function formProblems(c: AppConfig): string[] {
  const out: string[] = [];
  if (!c.name.trim()) out.push(t("Give the service a name."));
  if (c.source === "github" && !validRepo(c.repo ?? "")) out.push(t("Enter the repository as owner/name."));
  if (c.source === "local" && !c.path) out.push(t("Choose the project folder."));
  if (c.source === "zip" && !c.path) out.push(t("Upload the project ZIP."));
  if (c.mode === "script" && c.runtime !== "static" && !c.start?.trim()) out.push(t("Enter the command that starts the service."));
  const keys = new Set<string>();
  for (const e of c.env) {
    if (!/^[A-Za-z_][A-Za-z0-9_]{0,63}$/.test(e.key)) out.push(t("Invalid variable name: {v0}.", { v0: e.key || t("(empty)") }));
    else if (keys.has(e.key)) out.push(t("Repeated variable: {key}.", { key: e.key }));
    keys.add(e.key);
  }
  for (const p of c.ports) if (!(p.container >= 1 && p.container <= 65535)) out.push(t("Service ports range from 1 to 65535."));
  return out;
}
