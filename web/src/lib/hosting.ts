// Client for the self-hosted services API (/api/hosting/*, ADR-0021). The
// API forwards to the apps agent; admins only, changes from home/tailnet.
import { useQuery } from "@tanstack/react-query";
import { api, get } from "./api";

/** Whether this user may open the Serviços page (admin, platform on). */
export function useHostingEnabled(): boolean {
  const q = useQuery({
    queryKey: ["me", "features"],
    queryFn: () => get<{ features?: { hosting?: boolean } }>("/api/me"),
    staleTime: 5 * 60_000,
  });
  return !!q.data?.features?.hosting;
}

export type Source = "github" | "local";
export type Mode = "script" | "dockerfile" | "compose";
export type Exposure = "local" | "lan" | "tailnet" | "public";
export type State = "never" | "deploying" | "running" | "partial" | "stopped" | "exited" | "crashloop" | "waiting";

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
}

export interface Detection {
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
};

// ---------- pure helpers (tested) ----------

export const stateLabel: Record<State, string> = {
  never: "Ainda não implantado",
  deploying: "Implantando",
  running: "Rodando",
  partial: "Rodando em parte",
  stopped: "Parado",
  exited: "Parou sozinho",
  crashloop: "Caindo repetidamente",
  waiting: "Aguardando o servidor ligar",
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
      return "bad";
  }
  return "idle";
}

export const phaseLabel: Record<Job["phase"], string> = {
  queued: "Na fila",
  fetch: "Buscando o código",
  build: "Preparando",
  start: "Iniciando",
  done: "Concluído",
  failed: "Falhou",
};

export const exposureInfo: Record<Exposure, { label: string; hint: string }> = {
  local: { label: "Só no servidor", hint: "Nenhum outro aparelho acessa. Bom para bots e tarefas." },
  lan: { label: "Casa e Tailscale", hint: "Aparelhos da rede de casa e do seu Tailscale." },
  tailnet: { label: "Só Tailscale", hint: "Somente aparelhos conectados ao seu Tailscale." },
  public: { label: "Internet", hint: "Qualquer pessoa, pelo IP público, com a porta liberada no roteador." },
};

export const linkLabel: Record<Service["links"][number]["kind"], string> = {
  lan: "Rede de casa",
  tailnet: "Tailscale",
  public: "Internet",
  local: "No servidor",
  internal: "Entre serviços",
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
  const where = a.source === "local" ? `pasta ${a.path}` : `${a.repo}${a.ref && a.ref !== "main" ? ` @ ${a.ref}` : ""}`;
  return a.dir ? `${where} / ${a.dir}` : where;
}

/** Short version text: a commit's 7 first characters, or the snapshot name. */
export function versionText(d?: Pick<Deployment, "commit" | "version">): string {
  if (!d) return "";
  return d.commit ? d.commit.slice(0, 7) : d.version.replace(/^local-/, "pasta de ");
}

/** A log line "2026-…Z text" split into its local time and text. */
export function splitLogLine(line: string): { time: string; text: string } {
  const m = /^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})(\.\d+)?Z\s?(.*)$/.exec(line);
  if (!m) return { time: "", text: line };
  const d = new Date(`${m[1]}Z`);
  const time = Number.isNaN(d.getTime()) ? "" : d.toLocaleTimeString("pt-BR", { hour: "2-digit", minute: "2-digit", second: "2-digit" });
  return { time, text: m[3] };
}

/** Starting commands for a runtime when nothing was detected. */
export const runtimeDefaults: Record<string, { setup: string; start: string; port: number }> = {
  node: { setup: "npm ci || npm install", start: "npm start", port: 3000 },
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
  python: "Python",
  go: "Go",
  bun: "Bun",
  deno: "Deno",
  static: "Site estático",
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
  if (!c.name.trim()) out.push("Dê um nome ao serviço.");
  if (c.source === "github" && !validRepo(c.repo ?? "")) out.push("Informe o repositório como dono/nome.");
  if (c.source === "local" && !c.path) out.push("Escolha a pasta do projeto.");
  if (c.mode === "script" && c.runtime !== "static" && !c.start?.trim()) out.push("Informe o comando que inicia o serviço.");
  const keys = new Set<string>();
  for (const e of c.env) {
    if (!/^[A-Za-z_][A-Za-z0-9_]{0,63}$/.test(e.key)) out.push(`Nome de variável inválido: ${e.key || "(vazio)"}.`);
    else if (keys.has(e.key)) out.push(`Variável repetida: ${e.key}.`);
    keys.add(e.key);
  }
  for (const p of c.ports) if (!(p.container >= 1 && p.container <= 65535)) out.push("Portas do serviço vão de 1 a 65535.");
  return out;
}
