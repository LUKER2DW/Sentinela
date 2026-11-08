import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Icon } from "../components/Icon";
import { Buoy } from "../components/Buoy";
import { ServiceFiles } from "../components/ServiceFiles";
import { ServiceSecrets } from "../components/ServiceSecrets";
import { navigate } from "../lib/router";
import { useToast } from "../lib/session";
import { useCompact } from "../lib/viewport";
import { fmtBytes, relative } from "../lib/time";
import {
  configOf,
  exposureInfo,
  formProblems,
  hosting,
  linkLabel,
  normalizeRepo,
  phaseLabel,
  runtimeDefaults,
  runtimeLabel,
  sourceText,
  splitLogLine,
  stateLabel,
  stateTone,
  versionText,
  type AppConfig,
  type Detection,
  type Exposure,
  type HostingInfo,
  type Mode,
  type Service,
} from "../lib/hosting";

import { getLang, t } from "../lib/i18n";
import { copyText } from "../lib/clipboard";
// Self-hosted services (ADR-0021): projects from GitHub or a folder on the
// server, run next to the NVR with their own limits. Admins only.

const listKey = ["hosting", "list"];
const infoKey = ["hosting", "info"];

function useServices() {
  return useQuery({
    queryKey: listKey,
    queryFn: hosting.list,
    // Faster while something is being deployed.
    refetchInterval: (q) => (q.state.data?.some((s) => s.status.state === "deploying") ? 2_000 : 6_000),
  });
}

export function ServicesPage({ params }: { params: URLSearchParams }) {
  const compact = useCompact();
  const info = useQuery({ queryKey: infoKey, queryFn: hosting.info, refetchInterval: 30_000 });
  const list = useServices();
  const [creating, setCreating] = useState(false);
  const [ghOpen, setGhOpen] = useState(false);
  const sel = params.get("s");
  const services = list.data ?? [];
  const current = services.find((s) => s.id === sel);

  const open = (id: string) => navigate("services", { s: id }, { push: compact });

  if (compact && current) return <ServiceDetail key={current.id} svc={current} info={info.data} />;

  return (
    <>
      <header className="page-head">
        <div className="title">
          <h1>{t("Services")}</h1>
          <span className="sub">{t("Your projects running on the server, with limits so they do not get in the way of the cameras.")}</span>
        </div>
        <button className="btn primary" onClick={() => setCreating(true)} disabled={!info.data || !!info.data.docker_error}>
          <Icon name="plus" />{" "}{t("New service")}
        </button>
      </header>
      {info.isError && <p className="notice error">{t("The services agent did not respond. Check whether the \"apps\" container is running on the server.")}</p>}
      {info.data && <Capacity info={info.data} onGitHub={() => setGhOpen(true)} />}
      {list.data && services.length === 0 ? (
        <div className="empty svc-empty">
          <Buoy tone="idle" />
          <strong>{t("No services yet")}</strong>
          <p>{t("Upload a ZIP, bring a project from GitHub or pick a folder on the server. Sentinela installs, starts and monitors the service.")}</p>
          <button className="btn primary" onClick={() => setCreating(true)} disabled={!info.data || !!info.data.docker_error}>
            <Icon name="plus" />{" "}{t("New service")}
          </button>
        </div>
      ) : (
        <div className="svc-layout">
          <nav className="svc-list" aria-label={t("Services")}>
            {services.map((s) => (
              <button key={s.id} className="svc-item" aria-current={s.id === sel ? "true" : undefined} onClick={() => open(s.id)}>
                <Buoy tone={stateTone(s.status.state)} label={stateLabel[s.status.state]} />
                <span className="svc-item-text">
                  <span className="svc-name">{s.name}</span>
                  <span className="svc-src">{sourceText(s)}</span>
                </span>
                <span className={`svc-state ${stateTone(s.status.state)}`}>
                  {s.status.state === "deploying" && s.status.job ? phaseLabel[s.status.job.phase] : stateLabel[s.status.state]}
                </span>
              </button>
            ))}
          </nav>
          {!compact && (
            <section className="svc-detail" aria-live="polite">
              {current ? (
                <ServiceDetail key={current.id} svc={current} info={info.data} />
              ) : (
                <div className="empty">
                  <Buoy tone="idle" />
                  <p>{t("Choose a service to see its output, deploy and settings.")}</p>
                </div>
              )}
            </section>
          )}
        </div>
      )}
      {creating && info.data && (
        <NewServiceDialog
          info={info.data}
          onClose={() => setCreating(false)}
          onCreated={(id) => {
            setCreating(false);
            open(id);
          }}
        />
      )}
      {ghOpen && info.data && <GitHubDialog has={info.data.github_token} onClose={() => setGhOpen(false)} />}
    </>
  );
}

/** One quiet line: memory the services may use, free RAM, Docker, GitHub. */
function Capacity({ info, onGitHub }: { info: HostingInfo; onGitHub: () => void }) {
  const pct = Math.min(100, (info.used_memory_mb / info.limits.total_memory_mb) * 100);
  return (
    <div className="svc-capacity">
      <div className="svc-cap-meter">
        <span className="small">
          {t("Reserved memory")}{" "}<strong className="num">{info.used_memory_mb}</strong>{" "}{t("of")}{" "}<span className="num">{info.limits.total_memory_mb}{" "}{t("MB")}</span>
        </span>
        <div className="meter" role="img" aria-label={t("{v0}% of the services' memory", { v0: Math.round(pct) })}>
          <span style={{ width: `${pct}%` }} />
        </div>
      </div>
      <span className="small muted num">{info.mem_free_mb ? t("{v0} GB free on the server", { v0: (info.mem_free_mb / 1024).toFixed(1) }) : ""}</span>
      {info.docker_error ? <span className="small svc-bad">{info.docker_error}</span> : <span className="small muted">{t("Docker")}{" "}{info.docker}</span>}
      {info.boot_waiting && <span className="small muted">{t("Server just started: services come up after the cameras.")}</span>}
      <button className="btn ghost small" onClick={onGitHub}>
        <Icon name="key" /> {info.github_token ? t("GitHub access: active") : t("GitHub access")}
      </button>
    </div>
  );
}

// ---------- detail ----------

type Tab = "out" | "deploy" | "files" | "secrets" | "config";

function ServiceDetail({ svc, info }: { svc: Service; info?: HostingInfo }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [tab, setTab] = useState<Tab>(svc.status.state === "deploying" || !svc.deployed ? "deploy" : "out");
  const [deleting, setDeleting] = useState(false);
  const [menu, setMenu] = useState(false);
  const st = svc.status;
  const refresh = () => void qc.invalidateQueries({ queryKey: ["hosting"] });
  const act = useMutation({
    mutationFn: (a: "deploy" | "start" | "stop" | "restart") => hosting.action(svc.id, a),
    onSuccess: (_, a) => {
      refresh();
      if (a === "deploy") setTab("deploy");
      toast({ deploy: t("Deploy queued."), start: t("Start requested. Follow the diagnostics and the output."), stop: t("Service stopped."), restart: t("Restart requested. Follow the diagnostics and the output.") }[a]);
    },
    onError: (e: Error) => toast(e.message, true),
  });
  const check = useMutation({
    mutationFn: () => hosting.check(svc.id),
    onSuccess: (r) =>
      toast(r.update ? t("There is a new version: {v0}{v1}.", { v0: r.commit ? r.commit.slice(0, 7) : r.latest, v1: r.message ? ` (${r.message})` : "" }) : t("Already on the latest version.")),
    onError: (e: Error) => toast(e.message, true),
  });
  const busy = st.state === "deploying" || act.isPending;
  const running = st.state === "running" || st.state === "partial";
  return (
    <div className="svc-pane">
      <header className="svc-head">
        <Buoy tone={stateTone(st.state)} />
        <div className="svc-title">
          <h2>{svc.name}</h2>
          <p className={`svc-state ${stateTone(st.state)}`}>
            {st.state === "deploying" && st.job ? `${phaseLabel[st.job.phase]}…` : stateLabel[st.state]}
            {st.total ? t(" ({running} of {total} containers)", { running: st.running, total: st.total }) : ""}
            {st.since && (running || st.state === "exited") && <span className="muted"> {relative(st.since)}</span>}
          </p>
        </div>
        <div className="svc-actions">
          <button className="btn primary" disabled={busy} onClick={() => act.mutate("deploy")}>
            <Icon name="download" /> {svc.deployed ? t("Update") : t("Deploy")}
          </button>
          {svc.deployed && running && (
            <>
              <button className="btn" disabled={busy} onClick={() => act.mutate("restart")}>
                <Icon name="refresh" />{" "}{t("Restart")}
              </button>
              <button className="btn" disabled={busy} onClick={() => act.mutate("stop")}>
                <Icon name="stop" />{" "}{t("Stop")}
              </button>
            </>
          )}
          {svc.deployed && !running && (
            <button className="btn" disabled={busy} onClick={() => act.mutate("start")}>
              <Icon name="play" />{" "}{t("Start")}
            </button>
          )}
          <span className="svc-more">
            <button className="btn icon" aria-label={t("More actions")} aria-expanded={menu} onClick={() => setMenu((m) => !m)}>
              <Icon name="dots" />
            </button>
            {menu && (
              <span className="ctx-menu svc-menu" role="menu" onMouseLeave={() => setMenu(false)}>
                <button role="menuitem" onClick={() => (setMenu(false), check.mutate())} disabled={check.isPending}>
                  <Icon name="search" />{" "}{t("Check for a new version")}
                </button>
                <button role="menuitem" className="danger" onClick={() => (setMenu(false), setDeleting(true))}>
                  <Icon name="trash" />{" "}{t("Delete service")}
                </button>
              </span>
            )}
          </span>
        </div>
      </header>

      {svc.needs_deploy && <p className="notice">{t("The project or the preparation changed. Click Update to apply.")}</p>}
      {st.error && st.state !== "deploying" && st.error !== st.job?.error && (running || st.error !== st.last_exit?.reason) && <p className="notice error">{st.error}</p>}
      {st.oom_killed && <p className="notice error">{t("The service went over the memory limit (")}{svc.memory_mb}{" "}{t("MB). Raise the limit in Settings.")}</p>}
      {st.job?.phase === "failed" && <p className="notice error" role="alert">{t("Last deploy failed:")}{" "}{st.job.error} <button className="btn ghost small" onClick={() => setTab("deploy")}>{t("View deploy log")}</button></p>}
      {st.last_exit && !running && st.last_exit.reason !== st.job?.error && <p className="notice error" role="alert">{st.last_exit.reason}</p>}
      {st.retry_at && <p className="notice small">{t("Restarts paused to contain the failures. Next attempt:")}{" "}{new Date(st.retry_at).toLocaleTimeString(getLang())}.</p>}
      <ServiceDiagnostics svc={svc} />

      <Links svc={svc} info={info} />
      <Facts svc={svc} />

      <div className="svc-tabs" role="tablist" aria-label={t("Details")}>
        {(
          [
            ["out", t("Output")],
            ["deploy", "Deploy"],
            ["files", t("Files")],
            ["secrets", "Secrets"],
            ["config", t("Settings")],
          ] as const
        ).map(([id, label]) => (
          <button key={id} role="tab" aria-selected={tab === id} onClick={() => setTab(id)}>
            {label}
          </button>
        ))}
      </div>
      <div role="tabpanel">
        {tab === "out" && <LogView svc={svc} />}
        {tab === "deploy" && <DeployLog svc={svc} />}
        {tab === "files" && <ServiceFiles svc={svc} />}
        {tab === "secrets" && <ServiceSecrets svc={svc} />}
        {tab === "config" && info && <ConfigTab svc={svc} info={info} />}
      </div>
      {deleting && <DeleteDialog svc={svc} onClose={() => setDeleting(false)} />}
    </div>
  );
}

function ServiceDiagnostics({ svc }: { svc: Service }) {
  const st = svc.status;
  const events = st.events ?? [];
  if (!st.last_exit && events.length === 0) return null;
  return <details className="svc-diagnostics" open={st.state === "crashloop" || st.state === "exited" || st.state === "failed"}>
    <summary>{t("Diagnostics")}{" "}<span className="muted small">{st.restarts ?? 0}{" "}{t("automatic attempt(s)")}{st.last_exit ? t(" · last exit: {code}", { code: st.last_exit.code }) : ""}</span></summary>
    {st.last_exit && <p className="small">{t("Last stop at")}{" "}{new Date(st.last_exit.at).toLocaleString(getLang())}: {st.last_exit.reason}</p>}
    <ol className="svc-event-list">{events.slice().reverse().map((e,i) => <li key={i}><time className="small muted">{new Date(e.at).toLocaleString(getLang())}</time><span>{e.message}</span></li>)}</ol>
  </details>;
}

function copy(text: string, toast: (t: string, e?: boolean) => void) {
  void copyText(text).then((ok) => (ok ? toast(t("Address copied.")) : toast(t("Could not copy."), true)));
}

function Links({ svc, info }: { svc: Service; info?: HostingInfo }) {
  const toast = useToast();
  if (svc.links.length === 0) {
    return svc.ports.length === 0 ? <p className="muted small svc-nolinks">{t("No ports: the service receives no connections (good for bots and jobs).")}</p> : null;
  }
  return (
    <ul className="svc-links">
      {svc.links.map((l) => (
        <li key={l.url + l.kind}>
          <span className="svc-link-kind">
            {linkLabel[l.kind]}
            {l.service && <span className="muted"> ({l.service})</span>}
          </span>
          {l.kind === "internal" ? (
            <code className="svc-url">{l.url}</code>
          ) : (
            <a className="svc-url" href={l.url} target="_blank" rel="noopener noreferrer">
              {l.url.replace(/^http:\/\//, "")}
            </a>
          )}
          <button className="btn ghost icon small" aria-label={t("Copy {url}", { url: l.url })} onClick={() => copy(l.url, toast)}>
            <Icon name="copy" />
          </button>
        </li>
      ))}
      {svc.exposure === "public" && info?.hosts.public && (
        <li className="svc-link-note small muted">
          {t("On the router, forward the port")}{" "}{svc.ports.map((p) => `${p.host}/${p.proto}`).join(", ")}{" "}{t("from the internet to this server.")}
        </li>
      )}
      {svc.exposure !== "local" && (
        <li className="svc-link-note small muted">
          {t("Over Tailscale, the access rule (ACL) must allow the ports")}{" "}{info ? `${info.port_first}-${info.port_last}` : t("of the services")}{" "}{t("from the server.")}
        </li>
      )}
    </ul>
  );
}

function Facts({ svc }: { svc: Service }) {
  const st = svc.status;
  const memPct = st.mem_limit ? Math.min(100, (st.mem_bytes / st.mem_limit) * 100) : 0;
  const running = st.state === "running" || st.state === "partial";
  return (
    <dl className="svc-facts">
      <div>
        <dt>{t("Version")}</dt>
        <dd>
          {svc.deployed ? (
            <>
              <span className="num">{versionText(svc.deployed)}</span>
              {svc.deployed.message && <span className="svc-msg"> {svc.deployed.message}</span>}
              <span className="muted small"> {relative(svc.deployed.at)}</span>
            </>
          ) : (
            <span className="muted">{t("None")}</span>
          )}
        </dd>
      </div>
      <div>
        <dt>{t("Processor")}</dt>
        <dd className="num">{running ? `${st.cpu_pct.toFixed(1)}%` : "—"} <span className="muted small">{t("of {n} core", { n: svc.cpus })}</span></dd>
      </div>
      <div>
        <dt>{t("Memory")}</dt>
        <dd>
          <span className="num">{running ? fmtBytes(st.mem_bytes) : "—"}</span> <span className="muted small">{t("of {n} MB", { n: svc.memory_mb })}</span>
          {running && (
            <span className="meter svc-mem" aria-hidden="true">
              <span style={{ width: `${memPct}%` }} />
            </span>
          )}
        </dd>
      </div>
      <div>
        <dt>{t("Network")}</dt>
        <dd className="num">{running ? t("{v0} received, {v1} sent", { v0: fmtBytes(st.net_rx), v1: fmtBytes(st.net_tx) }) : "—"}</dd>
      </div>
      <div>
        <dt>{t("Disk")}</dt>
        <dd className="num">{st.disk_bytes ? fmtBytes(st.disk_bytes) : "—"}</dd>
      </div>
      <div>
        <dt>{t("When the server restarts")}</dt>
        <dd>{svc.autostart ? t("Starts by itself") : t("Stays still")}</dd>
      </div>
    </dl>
  );
}

/** A dark pane that follows the end of a growing log. */
function LogPane({ children, empty }: { children: ReactNode; empty: boolean }) {
  const ref = useRef<HTMLPreElement>(null);
  const atEnd = useRef(true);
  useEffect(() => {
    const el = ref.current;
    if (el && atEnd.current) el.scrollTop = el.scrollHeight;
  });
  return (
    <pre
      ref={ref}
      className="svc-log"
      tabIndex={0}
      onScroll={(e) => {
        const el = e.currentTarget;
        atEnd.current = el.scrollHeight - el.scrollTop - el.clientHeight < 24;
      }}
    >
      {empty ? <span className="svc-log-empty">{t("Nothing here yet.")}</span> : children}
    </pre>
  );
}

function LogView({ svc }: { svc: Service }) {
  const q = useQuery({ queryKey: ["hosting", "logs", svc.id], queryFn: () => hosting.logs(svc.id, 400), refetchInterval: 3_000 });
  const [errorsOnly, setErrorsOnly] = useState(false);
  const lines = q.data?.lines ?? [];
  return (
    <div className="stack-sm">
      {q.isError && <p className="notice error" role="alert">{t("Could not read the logs:")}{" "}{q.error.message}</p>}
      <div className="svc-log-toolbar"><span className="muted small">{t("Output updated every 3 seconds ·")}{" "}{lines.length}{" "}{t("line(s)")}</span><span className="grow" /><button className="btn ghost small" aria-pressed={errorsOnly} onClick={() => setErrorsOnly((x) => !x)}>{errorsOnly ? t("Show all") : t("Errors and warnings")}</button><button className="btn ghost small" disabled={!lines.length} onClick={() => downloadLog(svc.id, "saida", lines.map((l) => l.t))}><Icon name="download" />{" "}{t("Download log")}</button></div>
    <LogPane empty={lines.length === 0}>
      {lines.filter((l) => !errorsOnly || l.s === 2 || /\b(error|erro|falha|failed|exception|fatal)\b/i.test(l.t)).map((l, i) => {
        const { time, text } = splitLogLine(l.t);
        return (
          <span key={i} className={`svc-line${l.s === 2 ? " stderr" : ""}`}>
            <span className="svc-time">{time}</span>
            {l.svc && <span className="svc-who">{l.svc}</span>}
            {text}
            {"\n"}
          </span>
        );
      })}
    </LogPane>
    {errorsOnly && lines.length > 0 && !lines.some((l) => l.s === 2 || /\b(error|erro|falha|failed|exception|fatal)\b/i.test(l.t)) && <p className="hint">{t("No errors or warnings in these lines. Also check Diagnostics: native failures can end the process without writing to the log.")}</p>}
    </div>
  );
}

function downloadLog(id: string, kind: string, lines: string[]) {
  const url = URL.createObjectURL(new Blob([lines.join("\n") + "\n"], { type: "text/plain;charset=utf-8" }));
  const link = document.createElement("a");
  link.href = url; link.download = `${id}-${kind}.log`; link.click();
  URL.revokeObjectURL(url);
}

function DeployLog({ svc }: { svc: Service }) {
  const deploying = svc.status.state === "deploying";
  const q = useQuery({ queryKey: ["hosting", "deploylog", svc.id], queryFn: () => hosting.deployLog(svc.id), refetchInterval: deploying ? 1_500 : 15_000 });
  const job = q.data?.job ?? svc.status.job;
  const lines = q.data?.lines ?? [];
  return (
    <div className="stack-sm">
      {q.isError && <p className="notice error" role="alert">{t("Could not read the deploy log:")}{" "}{q.error.message}</p>}
      {job?.error && job.error !== svc.status.job?.error && <p className="notice error" role="alert">{job.error}</p>}
      {job && (
        <p className={`small svc-job ${job.phase}`}>
          {job.kind === "recreate" ? t("Applying settings") : t("Deploy")}: {phaseLabel[job.phase]}
          {job.version && <span className="num"> ({job.version.slice(0, 7)})</span>}
          <span className="muted"> {relative(job.started)}</span>
        </p>
      )}
      <div className="svc-log-toolbar"><span className="small muted">{t("Preparation, start and validation of the service")}</span><span className="grow" /><button className="btn ghost small" disabled={!lines.length} onClick={() => downloadLog(svc.id, "deploy", lines)}><Icon name="download" />{" "}{t("Download log")}</button></div>
      <LogPane empty={lines.length === 0}>
        {lines.map((l, i) => (
          <span key={i} className={`svc-line${l.includes("✗") ? " err" : l.includes("✓") ? " ok" : ""}`}>
            {l}
            {"\n"}
          </span>
        ))}
      </LogPane>
    </div>
  );
}

function ConfigTab({ svc, info }: { svc: Service; info: HostingInfo }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [cfg, setCfg] = useState<AppConfig>(() => configOf(svc));
  const problems = formProblems(cfg);
  const save = useMutation({
    mutationFn: () => hosting.update(svc.id, cfg),
    onSuccess: (s) => {
      void qc.invalidateQueries({ queryKey: ["hosting"] });
      setCfg(configOf(s));
      toast(s.needs_deploy ? t("Saved. Click Update to apply.") : t("Saved."));
    },
    onError: (e: Error) => toast(e.message, true),
  });
  return (
    <form
      className="stack-sm svc-config"
      onSubmit={(e) => {
        e.preventDefault();
        if (problems.length === 0) save.mutate();
      }}
    >
      <SourceFields cfg={cfg} set={setCfg} />
      <RunFields cfg={cfg} set={setCfg} info={info} />
      <AccessFields cfg={cfg} set={setCfg} info={info} hosts={info.hosts} />
      {problems.length > 0 && <p className="hint bad">{problems[0]}</p>}
      <div className="dialog-actions">
        <button type="button" className="btn ghost" onClick={() => setCfg(configOf(svc))}>
          {t("Undo")}
        </button>
        <button className="btn primary" disabled={problems.length > 0 || save.isPending}>
          <Icon name="check" />{" "}{t("Save")}
        </button>
      </div>
      <p className="hint">{t("Variables, ports, access and limits take effect right away (the service restarts). Source and preparation take effect on the next deploy.")}</p>
    </form>
  );
}

// ---------- form pieces (new service and configurations) ----------

type Upd = (f: (c: AppConfig) => AppConfig) => void;
type SetCfg = (c: AppConfig | ((c: AppConfig) => AppConfig)) => void;

function SourceFields({ cfg, set }: { cfg: AppConfig; set: SetCfg }) {
  return (
    <fieldset className="svc-fieldset">
      <legend>{t("Source")}</legend>
      {cfg.source === "github" ? (
        <div className="svc-grid">
          <div className="field">
            <label htmlFor="sf-repo">{t("Repository")}</label>
            <input id="sf-repo" className="input" value={cfg.repo ?? ""} onChange={(e) => set((c) => ({ ...c, repo: e.target.value }))} spellCheck={false} />
          </div>
          <div className="field">
            <label htmlFor="sf-ref">{t("Branch")}</label>
            <input id="sf-ref" className="input" value={cfg.ref ?? ""} onChange={(e) => set((c) => ({ ...c, ref: e.target.value }))} spellCheck={false} />
          </div>
        </div>
      ) : (
        <p className="small">
          {cfg.source === "zip" ? t("Project uploaded as ZIP") : <>{t("Source folder")}{" "}<strong>{cfg.path}</strong></>}{t(". Edit the protected project in the Files tab.")}
        </p>
      )}
      <div className="field">
        <label htmlFor="sf-dir">{t("Subfolder (optional)")}</label>
        <input id="sf-dir" className="input" value={cfg.dir ?? ""} placeholder={t("e.g. apps/api")} onChange={(e) => set((c) => ({ ...c, dir: e.target.value }))} spellCheck={false} />
      </div>
    </fieldset>
  );
}

const modeInfo: Record<Mode, { label: string; icon: string; text: string }> = {
  script: { label: t("Commands"), icon: "keyboard", text: t("A ready environment (Node, Python, Go…) runs your commands.") },
  dockerfile: { label: "Dockerfile", icon: "archive", text: t("Builds the image from the project's Dockerfile.") },
  compose: { label: "Compose", icon: "grid", text: t("Starts all the services in compose.yaml, with the limits from here.") },
};

function RunFields({ cfg, set, info, detection }: { cfg: AppConfig; set: SetCfg; info: HostingInfo; detection?: Detection }) {
  const runtimes = Object.keys(info.runtimes);
  return (
    <fieldset className="svc-fieldset">
      <legend>{t("How to run")}</legend>
      <div className="role-pick svc-modes">
        {(Object.keys(modeInfo) as Mode[]).map((m) => (
          <label key={m} className={`role-card${cfg.mode === m ? " on" : ""}`}>
            <input type="radio" name="svc-mode" checked={cfg.mode === m} onChange={() => set((c) => ({ ...c, mode: m }))} />
            <Icon name={modeInfo[m].icon} />
            <strong>{modeInfo[m].label}</strong>
            <span className="muted small">{modeInfo[m].text}</span>
          </label>
        ))}
      </div>
      {cfg.mode === "script" && (
        <>
          <div className="field">
            <label htmlFor="rf-rt">{t("Environment")}</label>
            <select
              id="rf-rt"
              className="input"
              value={cfg.runtime}
              onChange={(e) => {
                const rt = e.target.value;
                const d = runtimeDefaults[rt];
                set((c) => ({
                  ...c,
                  runtime: rt,
                  setup: c.setup || d?.setup || "",
                  start: c.start || d?.start || "",
                  ports: c.ports.length || !d ? c.ports : [{ container: d.port, host: 0, proto: "tcp" }],
                }));
              }}
            >
              {runtimes.map((r) => (
                <option key={r} value={r}>
                  {runtimeLabel[r] ?? r} ({info.runtimes[r]})
                </option>
              ))}
            </select>
          </div>
          <div className="field">
            <label htmlFor="rf-setup">{t("Preparation (runs on every deploy)")}</label>
            <textarea id="rf-setup" className="input svc-code" rows={2} value={cfg.setup ?? ""} placeholder={t("e.g. npm ci && npm run build")} onChange={(e) => set((c) => ({ ...c, setup: e.target.value }))} spellCheck={false} />
          </div>
          <div className="field">
            <label htmlFor="rf-start">{t("Start command")}</label>
            <textarea
              id="rf-start"
              className="input svc-code"
              rows={2}
              value={cfg.start ?? ""}
              placeholder={cfg.runtime === "static" ? t("empty: serves the folder's files") : t("e.g. npm start")}
              onChange={(e) => set((c) => ({ ...c, start: e.target.value }))}
              spellCheck={false}
            />
            <span className="hint">{t("The code lives in /app, the data that must survive in /data. The port arrives in the PORT variable.")}</span>
          </div>
        </>
      )}
      {cfg.mode === "dockerfile" && (
        <div className="field">
          <label htmlFor="rf-cmd">{t("Command (optional)")}</label>
          <input id="rf-cmd" className="input svc-code" value={cfg.start ?? ""} placeholder={t("empty: the Dockerfile CMD")} onChange={(e) => set((c) => ({ ...c, start: e.target.value }))} spellCheck={false} />
        </div>
      )}
      {cfg.mode === "compose" && (
        <div className="stack-sm">
          <div className="field">
            <label htmlFor="rf-cf">{t("Compose file")}</label>
            <input id="rf-cf" className="input svc-code" value={cfg.compose_file ?? ""} placeholder={t("empty: compose.yaml or docker-compose.yml")} onChange={(e) => set((c) => ({ ...c, compose_file: e.target.value }))} spellCheck={false} />
          </div>
          {detection?.services && (
            <ul className="svc-compose">
              {detection.services.map((s) => (
                <li key={s.name}>
                  <strong>{s.name}</strong> <span className="muted small">{s.build ? t("built from the project") : s.image}</span>
                  {s.ports && s.ports.length > 0 && <span className="small"> {t("port {ports}", { ports: s.ports.join(", ") })}</span>}
                </li>
              ))}
            </ul>
          )}
          <p className="hint">
            {t("Published ports move to the services range; each container gets the limits below. Use named volumes for data: the project folders are replaced on every deploy.")}
          </p>
        </div>
      )}
      {cfg.mode !== "compose" && <PortsEditor cfg={cfg} set={set} />}
    </fieldset>
  );
}

function PortsEditor({ cfg, set }: { cfg: AppConfig; set: SetCfg }) {
  const upd: Upd = set;
  return (
    <div className="field">
      <span className="svc-label">{t("Ports the service listens on")}</span>
      {cfg.ports.map((p, i) => (
        <div key={i} className="svc-row">
          <input
            className="input num"
            type="number"
            min={1}
            max={65535}
            aria-label={t("Service port")}
            value={p.container || ""}
            onChange={(e) => upd((c) => ({ ...c, ports: c.ports.map((x, j) => (j === i ? { ...x, container: Number(e.target.value) } : x)) }))}
          />
          <select className="input" aria-label={t("Protocol")} value={p.proto} onChange={(e) => upd((c) => ({ ...c, ports: c.ports.map((x, j) => (j === i ? { ...x, proto: e.target.value as "tcp" | "udp" } : x)) }))}>
            <option value="tcp">{t("TCP")}</option>
            <option value="udp">{t("UDP")}</option>
          </select>
          <span className="muted small svc-host">{p.host ? t("server :{host}", { host: p.host }) : t("server port when saving")}</span>
          <button type="button" className="btn ghost icon small" aria-label={t("Remove port")} onClick={() => upd((c) => ({ ...c, ports: c.ports.filter((_, j) => j !== i) }))}>
            <Icon name="x" />
          </button>
        </div>
      ))}
      {cfg.ports.length < 5 && (
        <button type="button" className="btn ghost small svc-add" onClick={() => upd((c) => ({ ...c, ports: [...c.ports, { container: 8080, host: 0, proto: "tcp" }] }))}>
          <Icon name="plus" />{" "}{t("Port")}
        </button>
      )}
    </div>
  );
}

function EnvEditor({ cfg, set }: { cfg: AppConfig; set: SetCfg }) {
  const upd: Upd = set;
  const [shown, setShown] = useState<Record<number, boolean>>({});
  return (
    <div className="field">
      <span className="svc-label">{t("Environment variables")}</span>
      {cfg.env.map((e, i) => (
        <div key={i} className="svc-row env">
          <input
            className="input svc-code"
            aria-label={t("Name")}
            placeholder={t("NAME")}
            value={e.key}
            onChange={(ev) => upd((c) => ({ ...c, env: c.env.map((x, j) => (j === i ? { ...x, key: ev.target.value.trim() } : x)) }))}
            spellCheck={false}
          />
          <input
            className="input svc-code"
            aria-label={t("Value of {key}", { key: e.key })}
            type={e.secret && !shown[i] ? "password" : "text"}
            placeholder={e.secret ? t("stored (leave empty to keep)") : t("value")}
            value={e.value}
            autoComplete="off"
            onChange={(ev) => upd((c) => ({ ...c, env: c.env.map((x, j) => (j === i ? { ...x, value: ev.target.value } : x)) }))}
            spellCheck={false}
          />
          <button
            type="button"
            className={`btn ghost icon small${e.secret ? " on" : ""}`}
            aria-pressed={!!e.secret}
            title={e.secret ? t("Secret: the value does not come back to the screen") : t("Mark as secret")}
            aria-label={t("Secret")}
            onClick={() => {
              if (e.secret) setShown((s) => ({ ...s, [i]: !s[i] }));
              else upd((c) => ({ ...c, env: c.env.map((x, j) => (j === i ? { ...x, secret: true } : x)) }));
            }}
          >
            <Icon name={e.secret ? (shown[i] ? "eyeOff" : "lock") : "eye"} />
          </button>
          <button type="button" className="btn ghost icon small" aria-label={t("Remove {key}", { key: e.key })} onClick={() => upd((c) => ({ ...c, env: c.env.filter((_, j) => j !== i) }))}>
            <Icon name="x" />
          </button>
        </div>
      ))}
      <button type="button" className="btn ghost small svc-add" onClick={() => upd((c) => ({ ...c, env: [...c.env, { key: "", value: "" }] }))}>
        <Icon name="plus" />{" "}{t("Variable")}
      </button>
    </div>
  );
}

function AccessFields({ cfg, set, info, hosts }: { cfg: AppConfig; set: SetCfg; info: HostingInfo; hosts: HostingInfo["hosts"] }) {
  const options: Exposure[] = ["lan", "tailnet", "local", "public"];
  return (
    <fieldset className="svc-fieldset">
      <legend>{t("Access and limits")}</legend>
      <div className="role-pick svc-expose">
        {options.map((x) => {
          const off = (x === "public" && !hosts.public) || (x === "tailnet" && !hosts.tailnet);
          return (
            <label key={x} className={`role-card${cfg.exposure === x ? " on" : ""}${off ? " off" : ""}`}>
              <input type="radio" name="svc-expose" disabled={off} checked={cfg.exposure === x} onChange={() => set((c) => ({ ...c, exposure: x }))} />
              <strong>{exposureInfo[x].label}</strong>
              <span className="muted small">{off ? (x === "public" ? t("Set apps.public_host on the server.") : t("Tailscale off.")) : exposureInfo[x].hint}</span>
            </label>
          );
        })}
      </div>
      {cfg.exposure === "public" && (
        <p className="notice small">
          {t("On the internet the service does not get the Sentinela password: protect it in the project itself. It cannot reach the cameras or the home network.")}
        </p>
      )}
      <EnvEditor cfg={cfg} set={set} />
      <div className="svc-grid">
        <div className="field">
          <label htmlFor="af-mem">{t("Memory (MB")}{cfg.mode === "compose" ? t(" per container") : ""})</label>
          <input
            id="af-mem"
            className="input num"
            type="number"
            min={32}
            max={info.limits.max_memory_mb}
            step={32}
            value={cfg.memory_mb}
            onChange={(e) => set((c) => ({ ...c, memory_mb: Number(e.target.value) }))}
          />
          <span className="hint">{t("Until")}{" "}{info.limits.max_memory_mb}{" "}{t("MB.")}</span>
        </div>
        <div className="field">
          <label htmlFor="af-cpu">{t("Processor (cores)")}</label>
          <input
            id="af-cpu"
            className="input num"
            type="number"
            min={0.05}
            max={info.limits.max_cpus}
            step={0.05}
            value={cfg.cpus}
            onChange={(e) => set((c) => ({ ...c, cpus: Number(e.target.value) }))}
          />
          <span className="hint">{t("Until")}{" "}{info.limits.max_cpus}{t(". Cameras have priority.")}</span>
        </div>
      </div>
      <label className="toggle-row">
        <span>
          <Icon name="refresh" />{" "}{t("Start by itself when the server restarts")}
        </span>
        <button type="button" role="switch" className="switch" aria-checked={cfg.autostart} onClick={() => set((c) => ({ ...c, autostart: !c.autostart }))} />
      </label>
      {cfg.mode === "script" && (
        <label className="toggle-row">
          <span>
            <Icon name="shield" />{" "}{t("Run as root inside the container")}
          </span>
          <button type="button" role="switch" className="switch" aria-checked={cfg.root} onClick={() => set((c) => ({ ...c, root: !c.root }))} />
        </label>
      )}
    </fieldset>
  );
}

// ---------- new service ----------

const STEPS = [t("Source"), t("How to run"), t("Access and limits")];

function emptyConfig(info: HostingInfo): AppConfig {
  return {
    id: "",
    name: "",
    source: "github",
    repo: "",
    ref: "main",
    path: "",
    dir: "",
    mode: "script",
    runtime: "node",
    setup: "",
    start: "",
    env: [],
    ports: [],
    exposure: "lan",
    memory_mb: info.limits.memory_mb,
    cpus: info.limits.cpus,
    autostart: true,
    root: false,
  };
}

/** Applies what the source looks like to a fresh form. */
function fromDetection(c: AppConfig, d: Detection): AppConfig {
  const name = c.name || (c.source === "local" ? (c.path ?? "").split("/").pop() : normalizeRepo(c.repo ?? "").split("/").pop()) || "";
  const port = d.port ?? (d.runtime ? runtimeDefaults[d.runtime]?.port : undefined);
  return {
    ...c,
    name,
    mode: d.mode,
    runtime: d.runtime ?? c.runtime,
    setup: d.setup ?? "",
    start: d.start ?? "",
    compose_file: d.compose_file ?? "",
    ports: d.mode === "compose" || !port ? [] : [{ container: port, host: 0, proto: "tcp" }],
  };
}

function NewServiceDialog({ info, onClose, onCreated }: { info: HostingInfo; onClose: () => void; onCreated: (id: string) => void }) {
  const toast = useToast();
  const qc = useQueryClient();
  const [step, setStep] = useState(0);
  const [cfg, setCfg] = useState<AppConfig>(() => emptyConfig(info));
  const [det, setDet] = useState<Detection | null>(null);
  const [zipFile, setZipFile] = useState<File | null>(null);
  const [uploadProgress, setUploadProgress] = useState(0);
  const uploaded = useRef("");
  useEffect(() => () => { if (uploaded.current) void hosting.dropUpload(uploaded.current).catch(() => {}); }, []);
  const inspect = useMutation({
    mutationFn: async () => {
      let sourcePath = cfg.path;
      if (cfg.source === "zip" && zipFile && !sourcePath) {
        setUploadProgress(0);
        const u = await hosting.upload(zipFile, setUploadProgress);
        sourcePath = u.path;
        uploaded.current = u.path;
        setCfg((c) => ({ ...c, path: u.path, name: c.name || zipFile.name.replace(/\.zip$/i, "") }));
      }
      return hosting.inspect({ source: cfg.source, repo: normalizeRepo(cfg.repo ?? ""), ref: cfg.ref, path: sourcePath, dir: cfg.dir });
    },
    onSuccess: (d) => {
      setDet(d);
      setCfg((c) => fromDetection(c, d));
      setStep(1);
    },
  });
  const create = useMutation({
    mutationFn: () => hosting.create({ ...cfg, repo: normalizeRepo(cfg.repo ?? "") }, true),
    onSuccess: (s) => {
      uploaded.current = "";
      void qc.invalidateQueries({ queryKey: ["hosting"] });
      toast(t("Service created. The first deploy has started."));
      onCreated(s.id);
    },
    onError: (e: Error) => toast(e.message, true),
  });
  const sourceOk = cfg.source === "github" ? /^[^/\s]+\/[^/\s]+$/.test(normalizeRepo(cfg.repo ?? "")) : cfg.source === "zip" ? !!zipFile || !!cfg.path : !!cfg.path;
  const problems = formProblems(cfg);
  const pending = inspect.isPending || create.isPending;
  const changeSource = (source: AppConfig["source"]) => {
    if (cfg.source === source) return;
    if (uploaded.current) { void hosting.dropUpload(uploaded.current).catch(() => {}); uploaded.current = ""; }
    setZipFile(null); setDet(null); inspect.reset();
    setCfg((c) => ({ ...c, source, path: "" }));
  };
  return (
    <div className="dialog-backdrop" role="dialog" aria-modal="true" aria-labelledby="ns-title" onMouseDown={(e) => e.target === e.currentTarget && !pending && onClose()}>
      <form
        className="dialog wide svc-new"
        onSubmit={(e) => {
          e.preventDefault();
          if (step === 0 && sourceOk) inspect.mutate();
          else if (step === 1) setStep(2);
          else if (step === 2 && problems.length === 0) create.mutate();
        }}
      >
        <div className="dialog-head">
          <div>
            <h2 id="ns-title">{t("New service")}</h2>
            <p className="muted small">{t("The code is copied to the server and runs isolated, with limited memory and processor.")}</p>
          </div>
          <button type="button" className="btn ghost icon small" aria-label={t("Close")} disabled={pending} onClick={onClose}>
            <Icon name="x" />
          </button>
        </div>
        <ol className="stepper" aria-label={t("Steps")}>
          {STEPS.map((s, i) => (
            <li key={s} className={i === step ? "on" : i < step ? "done" : ""}>
              <button type="button" disabled={i > step || pending} onClick={() => setStep(i)}>
                <span className="step-n">{i < step ? <Icon name="check" /> : i + 1}</span>
                {s}
              </button>
            </li>
          ))}
        </ol>

        {step === 0 && (
          <div className="step-body">
            <div className="segmented svc-source" role="group" aria-label={t("Where the code comes from")}>
              <button type="button" disabled={pending} aria-pressed={cfg.source === "github"} onClick={() => changeSource("github")}>
                <Icon name="network" />{" "}{t("GitHub")}
              </button>
              <button type="button" aria-pressed={cfg.source === "local"} disabled={!info.local_root || pending} onClick={() => changeSource("local")}>
                <Icon name="folder" />{" "}{t("Folder on the server")}
              </button>
              <button type="button" aria-pressed={cfg.source === "zip"} disabled={pending} onClick={() => changeSource("zip")}><Icon name="upload" />{" "}{t("Upload ZIP")}</button>
            </div>
            {cfg.source === "github" ? (
              <div className="svc-grid">
                <div className="field">
                  <label htmlFor="ns-repo">{t("Repository")}</label>
                  <input
                    id="ns-repo"
                    className="input"
                    autoFocus
                    value={cfg.repo}
                    placeholder={t("owner/project or https://github.com/owner/project")}
                    onChange={(e) => setCfg((c) => ({ ...c, repo: e.target.value }))}
                    spellCheck={false}
                  />
                  {!info.github_token && <span className="hint">{t("Private repository? Set up GitHub access first.")}</span>}
                </div>
                <div className="field">
                  <label htmlFor="ns-ref">{t("Branch")}</label>
                  <input id="ns-ref" className="input" value={cfg.ref} onChange={(e) => setCfg((c) => ({ ...c, ref: e.target.value }))} spellCheck={false} />
                </div>
              </div>
            ) : cfg.source === "zip" ? (
              <div className="field svc-zip-upload">
                <label htmlFor="ns-zip">{t("Project as .zip")}</label>
                <input id="ns-zip" className="input" type="file" accept=".zip,application/zip" disabled={inspect.isPending} onChange={(e) => {
                  if (uploaded.current) { void hosting.dropUpload(uploaded.current).catch(() => {}); uploaded.current = ""; }
                  setZipFile(e.target.files?.[0] ?? null); setCfg((c) => ({ ...c, path: "" })); inspect.reset();
                }} />
                <span className="hint">{t("Up to 300 MB compressed and 1 GB extracted. Include the .env if needed; its values stay protected on the server. Dependencies are installed during preparation.")}</span>
                {zipFile && <p className="small">{zipFile.name} · {fmtBytes(zipFile.size)}</p>}
                {inspect.isPending && <p className="notice small" role="status">{uploadProgress < 100 ? t("Uploading ZIP… {uploadProgress}%", { uploadProgress }) : t("ZIP uploaded. Extracting and analyzing the project…")}</p>}
              </div>
            ) : (
              <FolderPicker root={info.local_root} value={cfg.path ?? ""} onPick={(p) => setCfg((c) => ({ ...c, path: p }))} />
            )}
            <div className="field">
              <label htmlFor="ns-dir">{t("Subfolder (optional)")}</label>
              <input id="ns-dir" className="input" value={cfg.dir} placeholder={t("e.g. apps/api")} onChange={(e) => setCfg((c) => ({ ...c, dir: e.target.value }))} spellCheck={false} />
            </div>
            {inspect.isError && <p className="notice error">{(inspect.error as Error).message}</p>}
          </div>
        )}

        {step === 1 && (
          <div className="step-body">
            {det && <DetectionNote d={det} />}
            <RunFields cfg={cfg} set={setCfg} info={info} detection={det ?? undefined} />
          </div>
        )}

        {step === 2 && (
          <div className="step-body">
            <div className="field">
              <label htmlFor="ns-name">{t("Name")}</label>
              <input id="ns-name" className="input" value={cfg.name} onChange={(e) => setCfg((c) => ({ ...c, name: e.target.value }))} />
            </div>
            <AccessFields cfg={cfg} set={setCfg} info={info} hosts={info.hosts} />
            {problems.length > 0 && <p className="hint bad">{problems[0]}</p>}
          </div>
        )}

        <div className="dialog-actions">
          {step > 0 && (
            <button type="button" className="btn ghost" onClick={() => setStep(step - 1)}>
              {t("Back")}
            </button>
          )}
          <span className="grow" />
          {step === 0 && (
            <button className="btn primary" disabled={!sourceOk || inspect.isPending}>
              <Icon name="search" /> {inspect.isPending ? t("Analyzing…") : t("Analyze the project")}
            </button>
          )}
          {step === 1 && (
            <button className="btn primary">
              {t("Continue")}{" "}<Icon name="chevronRight" />
            </button>
          )}
          {step === 2 && (
            <button className="btn primary" disabled={problems.length > 0 || create.isPending}>
              <Icon name="download" /> {cfg.source === "local" ? t("Move and deploy") : t("Create and deploy")}
            </button>
          )}
        </div>
      </form>
    </div>
  );
}

function DetectionNote({ d }: { d: Detection }) {
  const v = d.version;
  const what =
    d.mode === "compose"
      ? t("Found {compose_file} with {v1} service(s).", { compose_file: d.compose_file, v1: d.services?.length ?? 0 })
      : d.mode === "dockerfile"
        ? d.port ? t("Found a Dockerfile that exposes port {port}.", { port: d.port }) : t("Found a Dockerfile.")
        : d.found.length > 0
          ? t("Found {files}: {runtime}.", { files: d.found.join(", "), runtime: runtimeLabel[d.runtime ?? ""] ?? d.runtime })
          : t("I did not recognize the project type.");
  return (
    <div className="svc-detect">
      <p>
        <Icon name="scan" /> {what}{" "}{t("The fields below are already filled in; adjust if needed.")}
      </p>
      {v && (
        <p className="small muted">
          {v.Commit ? t("Commit {v0}{v1}{v2}", { v0: v.Commit.slice(0, 7), v1: v.Branch ? ` em ${v.Branch}` : "", v2: v.Message ? `: ${v.Message}` : "" }) : t("Folder without Git ({Label})", { Label: v.Label })}
        </p>
      )}
      {d.notes?.map((n) => (
        <p key={n} className="small svc-bad">
          {n}
        </p>
      ))}
    </div>
  );
}

const markerLabel: Record<string, string> = {
  compose: "Compose",
  dockerfile: "Dockerfile",
  node: "Node",
  bun: "Bun",
  deno: "Deno",
  python: "Python",
  go: "Go",
  static: "Site",
};

function FolderPicker({ root, value, onPick }: { root: string; value: string; onPick: (p: string) => void }) {
  const [at, setAt] = useState(() => value.split("/").slice(0, -1).join("/"));
  const q = useQuery({ queryKey: ["hosting", "local", at], queryFn: () => hosting.local(at) });
  const crumbs = useMemo(() => (at ? at.split("/") : []), [at]);
  return (
    <div className="svc-picker">
      <nav className="svc-crumbs small" aria-label={t("Current folder")}>
        <button type="button" className="btn ghost small" onClick={() => setAt("")}>
          {root}
        </button>
        {crumbs.map((c, i) => (
          <button key={i} type="button" className="btn ghost small" onClick={() => setAt(crumbs.slice(0, i + 1).join("/"))}>
            / {c}
          </button>
        ))}
      </nav>
      {q.isError && <p className="notice error">{(q.error as Error).message}</p>}
      <ul className="svc-folders">
        {q.data?.folders.length === 0 && <li className="muted small">{t("No subfolders here.")}</li>}
        {q.data?.folders.map((f) => (
          <li key={f.path} className={value === f.path ? "on" : ""}>
            <button type="button" className="svc-folder" onClick={() => onPick(f.path)} onDoubleClick={() => setAt(f.path)} aria-pressed={value === f.path}>
              <Icon name="folder" />
              <span className="svc-folder-name">{f.name}</span>
              {f.git && <span className="pill small-pill">{t("Git")}</span>}
              {f.markers?.map((m) => (
                <span key={m} className="pill small-pill">
                  {markerLabel[m] ?? m}
                </span>
              ))}
            </button>
            <button type="button" className="btn ghost icon small" aria-label={t("Open {name}", { name: f.name })} onClick={() => setAt(f.path)}>
              <Icon name="chevronRight" />
            </button>
          </li>
        ))}
      </ul>
      <p className="notice small">{t("On deploy, this folder is moved from Cloud to the protected Services area. Afterwards, edit your files in the Files tab. Dependencies are installed again on every deploy.")}</p>
    </div>
  );
}

// ---------- dialogs ----------

function GitHubDialog({ has, onClose }: { has: boolean; onClose: () => void }) {
  const toast = useToast();
  const qc = useQueryClient();
  const [tok, setTok] = useState("");
  const save = useMutation({
    mutationFn: () => (tok ? hosting.setToken(tok) : hosting.dropToken()),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: infoKey });
      toast(tok ? t("GitHub access saved.") : t("GitHub access removed."));
      onClose();
    },
    onError: (e: Error) => toast(e.message, true),
  });
  return (
    <div className="dialog-backdrop" role="dialog" aria-modal="true" aria-labelledby="gh-title" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <form
        className="dialog"
        onSubmit={(e) => {
          e.preventDefault();
          save.mutate();
        }}
      >
        <h2 id="gh-title">{t("GitHub access")}</h2>
        <p className="muted small">
          {t("Only for private repositories. On GitHub, create a \"fine-grained\" token with content read permission (Contents: Read) only for the repositories you will use. It is kept on the server and never comes back to the screen.")}
        </p>
        <div className="field">
          <label htmlFor="gh-tok">{t("Token")}</label>
          <input id="gh-tok" className="input" type="password" autoComplete="off" value={tok} placeholder={has ? t("stored; paste another to replace") : "github_pat_…"} onChange={(e) => setTok(e.target.value.trim())} />
        </div>
        <div className="dialog-actions">
          {has && (
            <button type="button" className="btn danger" onClick={() => (setTok(""), save.mutate())} disabled={save.isPending}>
              {t("Remove")}
            </button>
          )}
          <span className="grow" />
          <button type="button" className="btn ghost" onClick={onClose}>
            {t("Cancel")}
          </button>
          <button className="btn primary" disabled={!tok || save.isPending}>
            {t("Save")}
          </button>
        </div>
      </form>
    </div>
  );
}

function DeleteDialog({ svc, onClose }: { svc: Service; onClose: () => void }) {
  const toast = useToast();
  const qc = useQueryClient();
  const [typed, setTyped] = useState("");
  const del = useMutation({
    mutationFn: () => hosting.remove(svc.id),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["hosting"] });
      toast(t("Service deleted."));
      navigate("services", undefined);
    },
    onError: (e: Error) => toast(e.message, true),
  });
  return (
    <div className="dialog-backdrop" role="dialog" aria-modal="true" aria-labelledby="del-svc" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <form
        className="dialog"
        onSubmit={(e) => {
          e.preventDefault();
          if (typed === svc.id) del.mutate();
        }}
      >
        <h2 id="del-svc">{t("Remove")}{" "}{svc.name}?</h2>
        <p className="muted small">
          {t("The service stops and is deleted from the server with its data (/data")}{svc.mode === "compose" ? t(" and the Compose volumes") : ""}{t("). The repository or source folder is not touched.")}
        </p>
        <div className="field">
          <label htmlFor="del-typed">
            {t("Enter")}{" "}<strong>{svc.id}</strong>{" "}{t("to confirm")}
          </label>
          <input id="del-typed" className="input" value={typed} onChange={(e) => setTyped(e.target.value.trim())} autoComplete="off" spellCheck={false} autoFocus />
        </div>
        <div className="dialog-actions">
          <button type="button" className="btn ghost" onClick={onClose}>
            {t("Cancel")}
          </button>
          <button className="btn primary danger-fill" disabled={typed !== svc.id || del.isPending}>
            <Icon name="trash" />{" "}{t("Remove")}
          </button>
        </div>
      </form>
    </div>
  );
}
