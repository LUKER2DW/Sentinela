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

  const open = (id: string) => navigate("servicos", { s: id }, { push: compact });

  if (compact && current) return <ServiceDetail key={current.id} svc={current} info={info.data} />;

  return (
    <>
      <header className="page-head">
        <div className="title">
          <h1>Serviços</h1>
          <span className="sub">Seus projetos rodando no servidor, com limites para não atrapalhar as câmeras.</span>
        </div>
        <button className="btn primary" onClick={() => setCreating(true)} disabled={!info.data || !!info.data.docker_error}>
          <Icon name="plus" /> Novo serviço
        </button>
      </header>
      {info.isError && <p className="notice error">O agente de serviços não respondeu. Veja se o contêiner “apps” está rodando no servidor.</p>}
      {info.data && <Capacity info={info.data} onGitHub={() => setGhOpen(true)} />}
      {list.data && services.length === 0 ? (
        <div className="empty svc-empty">
          <Buoy tone="idle" />
          <strong>Nenhum serviço ainda</strong>
          <p>Envie um ZIP, traga um projeto do GitHub ou selecione uma pasta do servidor. O Sentinela instala, inicia e acompanha o serviço.</p>
          <button className="btn primary" onClick={() => setCreating(true)} disabled={!info.data || !!info.data.docker_error}>
            <Icon name="plus" /> Novo serviço
          </button>
        </div>
      ) : (
        <div className="svc-layout">
          <nav className="svc-list" aria-label="Serviços">
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
                  <p>Escolha um serviço para ver a saída, o deploy e as configurações.</p>
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
          Memória reservada <strong className="num">{info.used_memory_mb}</strong> de <span className="num">{info.limits.total_memory_mb} MB</span>
        </span>
        <div className="meter" role="img" aria-label={`${Math.round(pct)}% da memória dos serviços`}>
          <span style={{ width: `${pct}%` }} />
        </div>
      </div>
      <span className="small muted num">{info.mem_free_mb ? `${(info.mem_free_mb / 1024).toFixed(1)} GB livres no servidor` : ""}</span>
      {info.docker_error ? <span className="small svc-bad">{info.docker_error}</span> : <span className="small muted">Docker {info.docker}</span>}
      {info.boot_waiting && <span className="small muted">Servidor recém-ligado: os serviços sobem depois das câmeras.</span>}
      <button className="btn ghost small" onClick={onGitHub}>
        <Icon name="key" /> {info.github_token ? "Acesso ao GitHub: ativo" : "Acesso ao GitHub"}
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
      toast({ deploy: "Deploy na fila.", start: "Início solicitado. Acompanhe o diagnóstico e a saída.", stop: "Serviço parado.", restart: "Reinício solicitado. Acompanhe o diagnóstico e a saída." }[a]);
    },
    onError: (e: Error) => toast(e.message, true),
  });
  const check = useMutation({
    mutationFn: () => hosting.check(svc.id),
    onSuccess: (r) =>
      toast(r.update ? `Há uma versão nova: ${r.commit ? r.commit.slice(0, 7) : r.latest}${r.message ? ` (${r.message})` : ""}.` : "Já está na versão mais recente."),
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
            {st.total ? ` (${st.running} de ${st.total} contêineres)` : ""}
            {st.since && (running || st.state === "exited") && <span className="muted"> {relative(st.since)}</span>}
          </p>
        </div>
        <div className="svc-actions">
          <button className="btn primary" disabled={busy} onClick={() => act.mutate("deploy")}>
            <Icon name="download" /> {svc.deployed ? "Atualizar" : "Implantar"}
          </button>
          {svc.deployed && running && (
            <>
              <button className="btn" disabled={busy} onClick={() => act.mutate("restart")}>
                <Icon name="refresh" /> Reiniciar
              </button>
              <button className="btn" disabled={busy} onClick={() => act.mutate("stop")}>
                <Icon name="stop" /> Parar
              </button>
            </>
          )}
          {svc.deployed && !running && (
            <button className="btn" disabled={busy} onClick={() => act.mutate("start")}>
              <Icon name="play" /> Iniciar
            </button>
          )}
          <span className="svc-more">
            <button className="btn icon" aria-label="Mais ações" aria-expanded={menu} onClick={() => setMenu((m) => !m)}>
              <Icon name="dots" />
            </button>
            {menu && (
              <span className="ctx-menu svc-menu" role="menu" onMouseLeave={() => setMenu(false)}>
                <button role="menuitem" onClick={() => (setMenu(false), check.mutate())} disabled={check.isPending}>
                  <Icon name="search" /> Procurar versão nova
                </button>
                <button role="menuitem" className="danger" onClick={() => (setMenu(false), setDeleting(true))}>
                  <Icon name="trash" /> Excluir serviço
                </button>
              </span>
            )}
          </span>
        </div>
      </header>

      {svc.needs_deploy && <p className="notice">O projeto ou a preparação mudou. Clique em Atualizar para aplicar.</p>}
      {st.error && st.state !== "deploying" && st.error !== st.job?.error && (running || st.error !== st.last_exit?.reason) && <p className="notice error">{st.error}</p>}
      {st.oom_killed && <p className="notice error">O serviço passou do limite de memória ({svc.memory_mb} MB). Aumente o limite em Configurações.</p>}
      {st.job?.phase === "failed" && <p className="notice error" role="alert">Último deploy falhou: {st.job.error} <button className="btn ghost small" onClick={() => setTab("deploy")}>Ver log do deploy</button></p>}
      {st.last_exit && !running && st.last_exit.reason !== st.job?.error && <p className="notice error" role="alert">{st.last_exit.reason}</p>}
      {st.retry_at && <p className="notice small">Reinícios pausados para conter as quedas. Próxima tentativa: {new Date(st.retry_at).toLocaleTimeString("pt-BR")}.</p>}
      <ServiceDiagnostics svc={svc} />

      <Links svc={svc} info={info} />
      <Facts svc={svc} />

      <div className="svc-tabs" role="tablist" aria-label="Detalhes">
        {(
          [
            ["out", "Saída"],
            ["deploy", "Deploy"],
            ["files", "Arquivos"],
            ["secrets", "Secrets"],
            ["config", "Configurações"],
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
    <summary>Diagnóstico <span className="muted small">{st.restarts ?? 0} tentativa(s) automática(s){st.last_exit ? ` · última saída: ${st.last_exit.code}` : ""}</span></summary>
    {st.last_exit && <p className="small">Última parada em {new Date(st.last_exit.at).toLocaleString("pt-BR")}: {st.last_exit.reason}</p>}
    <ol className="svc-event-list">{events.slice().reverse().map((e,i) => <li key={i}><time className="small muted">{new Date(e.at).toLocaleString("pt-BR")}</time><span>{e.message}</span></li>)}</ol>
  </details>;
}

function copy(text: string, toast: (t: string, e?: boolean) => void) {
  void navigator.clipboard?.writeText(text).then(
    () => toast("Endereço copiado."),
    () => toast("Não foi possível copiar.", true),
  );
}

function Links({ svc, info }: { svc: Service; info?: HostingInfo }) {
  const toast = useToast();
  if (svc.links.length === 0) {
    return svc.ports.length === 0 ? <p className="muted small svc-nolinks">Sem portas: o serviço não recebe conexões (bom para bots e tarefas).</p> : null;
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
          <button className="btn ghost icon small" aria-label={`Copiar ${l.url}`} onClick={() => copy(l.url, toast)}>
            <Icon name="copy" />
          </button>
        </li>
      ))}
      {svc.exposure === "public" && info?.hosts.public && (
        <li className="svc-link-note small muted">
          No roteador, encaminhe a porta {svc.ports.map((p) => `${p.host}/${p.proto}`).join(", ")} da internet para este servidor.
        </li>
      )}
      {svc.exposure !== "local" && (
        <li className="svc-link-note small muted">
          Pelo Tailscale, a regra de acesso (ACL) precisa liberar as portas {info ? `${info.port_first}-${info.port_last}` : "dos serviços"} do servidor.
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
        <dt>Versão</dt>
        <dd>
          {svc.deployed ? (
            <>
              <span className="num">{versionText(svc.deployed)}</span>
              {svc.deployed.message && <span className="svc-msg"> {svc.deployed.message}</span>}
              <span className="muted small"> {relative(svc.deployed.at)}</span>
            </>
          ) : (
            <span className="muted">Nenhuma</span>
          )}
        </dd>
      </div>
      <div>
        <dt>Processador</dt>
        <dd className="num">{running ? `${st.cpu_pct.toFixed(1)}%` : "—"} <span className="muted small">de {svc.cpus} núcleo</span></dd>
      </div>
      <div>
        <dt>Memória</dt>
        <dd>
          <span className="num">{running ? fmtBytes(st.mem_bytes) : "—"}</span> <span className="muted small">de {svc.memory_mb} MB</span>
          {running && (
            <span className="meter svc-mem" aria-hidden="true">
              <span style={{ width: `${memPct}%` }} />
            </span>
          )}
        </dd>
      </div>
      <div>
        <dt>Rede</dt>
        <dd className="num">{running ? `${fmtBytes(st.net_rx)} recebidos, ${fmtBytes(st.net_tx)} enviados` : "—"}</dd>
      </div>
      <div>
        <dt>Disco</dt>
        <dd className="num">{st.disk_bytes ? fmtBytes(st.disk_bytes) : "—"}</dd>
      </div>
      <div>
        <dt>Ao religar o servidor</dt>
        <dd>{svc.autostart ? "Inicia sozinho" : "Fica parado"}</dd>
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
      {empty ? <span className="svc-log-empty">Nada por aqui ainda.</span> : children}
    </pre>
  );
}

function LogView({ svc }: { svc: Service }) {
  const q = useQuery({ queryKey: ["hosting", "logs", svc.id], queryFn: () => hosting.logs(svc.id, 400), refetchInterval: 3_000 });
  const [errorsOnly, setErrorsOnly] = useState(false);
  const lines = q.data?.lines ?? [];
  return (
    <div className="stack-sm">
      {q.isError && <p className="notice error" role="alert">Não foi possível ler os logs: {q.error.message}</p>}
      <div className="svc-log-toolbar"><span className="muted small">Saída atualizada a cada 3 segundos · {lines.length} linha(s)</span><span className="grow" /><button className="btn ghost small" aria-pressed={errorsOnly} onClick={() => setErrorsOnly((x) => !x)}>{errorsOnly ? "Mostrar tudo" : "Erros e avisos"}</button><button className="btn ghost small" disabled={!lines.length} onClick={() => downloadLog(svc.id, "saida", lines.map((l) => l.t))}><Icon name="download" /> Baixar log</button></div>
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
    {errorsOnly && lines.length > 0 && !lines.some((l) => l.s === 2 || /\b(error|erro|falha|failed|exception|fatal)\b/i.test(l.t)) && <p className="hint">Nenhum erro ou aviso nestas linhas. Confira também o Diagnóstico: falhas nativas podem encerrar o processo sem escrever no log.</p>}
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
      {q.isError && <p className="notice error" role="alert">Não foi possível ler o log do deploy: {q.error.message}</p>}
      {job?.error && job.error !== svc.status.job?.error && <p className="notice error" role="alert">{job.error}</p>}
      {job && (
        <p className={`small svc-job ${job.phase}`}>
          {job.kind === "recreate" ? "Aplicando configurações" : "Deploy"}: {phaseLabel[job.phase]}
          {job.version && <span className="num"> ({job.version.slice(0, 7)})</span>}
          <span className="muted"> {relative(job.started)}</span>
        </p>
      )}
      <div className="svc-log-toolbar"><span className="small muted">Preparação, início e validação do serviço</span><span className="grow" /><button className="btn ghost small" disabled={!lines.length} onClick={() => downloadLog(svc.id, "deploy", lines)}><Icon name="download" /> Baixar log</button></div>
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
      toast(s.needs_deploy ? "Salvo. Clique em Atualizar para aplicar." : "Salvo.");
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
          Desfazer
        </button>
        <button className="btn primary" disabled={problems.length > 0 || save.isPending}>
          <Icon name="check" /> Salvar
        </button>
      </div>
      <p className="hint">Variáveis, portas, acesso e limites valem na hora (o serviço reinicia). Fonte e preparação valem no próximo deploy.</p>
    </form>
  );
}

// ---------- form pieces (new service and configurations) ----------

type Upd = (f: (c: AppConfig) => AppConfig) => void;
type SetCfg = (c: AppConfig | ((c: AppConfig) => AppConfig)) => void;

function SourceFields({ cfg, set }: { cfg: AppConfig; set: SetCfg }) {
  return (
    <fieldset className="svc-fieldset">
      <legend>Fonte</legend>
      {cfg.source === "github" ? (
        <div className="svc-grid">
          <div className="field">
            <label htmlFor="sf-repo">Repositório</label>
            <input id="sf-repo" className="input" value={cfg.repo ?? ""} onChange={(e) => set((c) => ({ ...c, repo: e.target.value }))} spellCheck={false} />
          </div>
          <div className="field">
            <label htmlFor="sf-ref">Branch</label>
            <input id="sf-ref" className="input" value={cfg.ref ?? ""} onChange={(e) => set((c) => ({ ...c, ref: e.target.value }))} spellCheck={false} />
          </div>
        </div>
      ) : (
        <p className="small">
          {cfg.source === "zip" ? "Projeto enviado por ZIP" : <>Pasta de origem <strong>{cfg.path}</strong></>}. Edite o projeto protegido na aba Arquivos.
        </p>
      )}
      <div className="field">
        <label htmlFor="sf-dir">Subpasta (opcional)</label>
        <input id="sf-dir" className="input" value={cfg.dir ?? ""} placeholder="ex.: apps/api" onChange={(e) => set((c) => ({ ...c, dir: e.target.value }))} spellCheck={false} />
      </div>
    </fieldset>
  );
}

const modeInfo: Record<Mode, { label: string; icon: string; text: string }> = {
  script: { label: "Comandos", icon: "keyboard", text: "Um ambiente pronto (Node, Python, Go…) roda os seus comandos." },
  dockerfile: { label: "Dockerfile", icon: "archive", text: "Constrói a imagem pelo Dockerfile do projeto." },
  compose: { label: "Compose", icon: "grid", text: "Sobe todos os serviços do compose.yaml, com os limites daqui." },
};

function RunFields({ cfg, set, info, detection }: { cfg: AppConfig; set: SetCfg; info: HostingInfo; detection?: Detection }) {
  const runtimes = Object.keys(info.runtimes);
  return (
    <fieldset className="svc-fieldset">
      <legend>Como rodar</legend>
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
            <label htmlFor="rf-rt">Ambiente</label>
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
            <label htmlFor="rf-setup">Preparação (roda a cada deploy)</label>
            <textarea id="rf-setup" className="input svc-code" rows={2} value={cfg.setup ?? ""} placeholder="ex.: npm ci && npm run build" onChange={(e) => set((c) => ({ ...c, setup: e.target.value }))} spellCheck={false} />
          </div>
          <div className="field">
            <label htmlFor="rf-start">Comando de início</label>
            <textarea
              id="rf-start"
              className="input svc-code"
              rows={2}
              value={cfg.start ?? ""}
              placeholder={cfg.runtime === "static" ? "vazio: serve os arquivos da pasta" : "ex.: npm start"}
              onChange={(e) => set((c) => ({ ...c, start: e.target.value }))}
              spellCheck={false}
            />
            <span className="hint">O código fica em /app, os dados que devem sobreviver em /data. A porta chega na variável PORT.</span>
          </div>
        </>
      )}
      {cfg.mode === "dockerfile" && (
        <div className="field">
          <label htmlFor="rf-cmd">Comando (opcional)</label>
          <input id="rf-cmd" className="input svc-code" value={cfg.start ?? ""} placeholder="vazio: o CMD do Dockerfile" onChange={(e) => set((c) => ({ ...c, start: e.target.value }))} spellCheck={false} />
        </div>
      )}
      {cfg.mode === "compose" && (
        <div className="stack-sm">
          <div className="field">
            <label htmlFor="rf-cf">Arquivo Compose</label>
            <input id="rf-cf" className="input svc-code" value={cfg.compose_file ?? ""} placeholder="vazio: compose.yaml ou docker-compose.yml" onChange={(e) => set((c) => ({ ...c, compose_file: e.target.value }))} spellCheck={false} />
          </div>
          {detection?.services && (
            <ul className="svc-compose">
              {detection.services.map((s) => (
                <li key={s.name}>
                  <strong>{s.name}</strong> <span className="muted small">{s.build ? "construído do projeto" : s.image}</span>
                  {s.ports && s.ports.length > 0 && <span className="small"> porta {s.ports.join(", ")}</span>}
                </li>
              ))}
            </ul>
          )}
          <p className="hint">
            As portas publicadas passam para a faixa dos serviços; cada contêiner recebe os limites abaixo. Use volumes nomeados para dados: as pastas do projeto são trocadas a cada deploy.
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
      <span className="svc-label">Portas que o serviço escuta</span>
      {cfg.ports.map((p, i) => (
        <div key={i} className="svc-row">
          <input
            className="input num"
            type="number"
            min={1}
            max={65535}
            aria-label="Porta do serviço"
            value={p.container || ""}
            onChange={(e) => upd((c) => ({ ...c, ports: c.ports.map((x, j) => (j === i ? { ...x, container: Number(e.target.value) } : x)) }))}
          />
          <select className="input" aria-label="Protocolo" value={p.proto} onChange={(e) => upd((c) => ({ ...c, ports: c.ports.map((x, j) => (j === i ? { ...x, proto: e.target.value as "tcp" | "udp" } : x)) }))}>
            <option value="tcp">TCP</option>
            <option value="udp">UDP</option>
          </select>
          <span className="muted small svc-host">{p.host ? `servidor :${p.host}` : "porta do servidor ao salvar"}</span>
          <button type="button" className="btn ghost icon small" aria-label="Remover porta" onClick={() => upd((c) => ({ ...c, ports: c.ports.filter((_, j) => j !== i) }))}>
            <Icon name="x" />
          </button>
        </div>
      ))}
      {cfg.ports.length < 5 && (
        <button type="button" className="btn ghost small svc-add" onClick={() => upd((c) => ({ ...c, ports: [...c.ports, { container: 8080, host: 0, proto: "tcp" }] }))}>
          <Icon name="plus" /> Porta
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
      <span className="svc-label">Variáveis de ambiente</span>
      {cfg.env.map((e, i) => (
        <div key={i} className="svc-row env">
          <input
            className="input svc-code"
            aria-label="Nome"
            placeholder="NOME"
            value={e.key}
            onChange={(ev) => upd((c) => ({ ...c, env: c.env.map((x, j) => (j === i ? { ...x, key: ev.target.value.trim() } : x)) }))}
            spellCheck={false}
          />
          <input
            className="input svc-code"
            aria-label={`Valor de ${e.key}`}
            type={e.secret && !shown[i] ? "password" : "text"}
            placeholder={e.secret ? "guardado (deixe vazio para manter)" : "valor"}
            value={e.value}
            autoComplete="off"
            onChange={(ev) => upd((c) => ({ ...c, env: c.env.map((x, j) => (j === i ? { ...x, value: ev.target.value } : x)) }))}
            spellCheck={false}
          />
          <button
            type="button"
            className={`btn ghost icon small${e.secret ? " on" : ""}`}
            aria-pressed={!!e.secret}
            title={e.secret ? "Secreto: o valor não volta para a tela" : "Marcar como secreto"}
            aria-label="Secreto"
            onClick={() => {
              if (e.secret) setShown((s) => ({ ...s, [i]: !s[i] }));
              else upd((c) => ({ ...c, env: c.env.map((x, j) => (j === i ? { ...x, secret: true } : x)) }));
            }}
          >
            <Icon name={e.secret ? (shown[i] ? "eyeOff" : "lock") : "eye"} />
          </button>
          <button type="button" className="btn ghost icon small" aria-label={`Remover ${e.key}`} onClick={() => upd((c) => ({ ...c, env: c.env.filter((_, j) => j !== i) }))}>
            <Icon name="x" />
          </button>
        </div>
      ))}
      <button type="button" className="btn ghost small svc-add" onClick={() => upd((c) => ({ ...c, env: [...c.env, { key: "", value: "" }] }))}>
        <Icon name="plus" /> Variável
      </button>
    </div>
  );
}

function AccessFields({ cfg, set, info, hosts }: { cfg: AppConfig; set: SetCfg; info: HostingInfo; hosts: HostingInfo["hosts"] }) {
  const options: Exposure[] = ["lan", "tailnet", "local", "public"];
  return (
    <fieldset className="svc-fieldset">
      <legend>Acesso e limites</legend>
      <div className="role-pick svc-expose">
        {options.map((x) => {
          const off = (x === "public" && !hosts.public) || (x === "tailnet" && !hosts.tailnet);
          return (
            <label key={x} className={`role-card${cfg.exposure === x ? " on" : ""}${off ? " off" : ""}`}>
              <input type="radio" name="svc-expose" disabled={off} checked={cfg.exposure === x} onChange={() => set((c) => ({ ...c, exposure: x }))} />
              <strong>{exposureInfo[x].label}</strong>
              <span className="muted small">{off ? (x === "public" ? "Defina apps.public_host no servidor." : "Tailscale desligado.") : exposureInfo[x].hint}</span>
            </label>
          );
        })}
      </div>
      {cfg.exposure === "public" && (
        <p className="notice small">
          Na internet o serviço fica sem a senha do Sentinela: proteja-o no próprio projeto. Ele não alcança as câmeras nem a rede de casa.
        </p>
      )}
      <EnvEditor cfg={cfg} set={set} />
      <div className="svc-grid">
        <div className="field">
          <label htmlFor="af-mem">Memória (MB{cfg.mode === "compose" ? " por contêiner" : ""})</label>
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
          <span className="hint">Até {info.limits.max_memory_mb} MB.</span>
        </div>
        <div className="field">
          <label htmlFor="af-cpu">Processador (núcleos)</label>
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
          <span className="hint">Até {info.limits.max_cpus}. As câmeras têm prioridade.</span>
        </div>
      </div>
      <label className="toggle-row">
        <span>
          <Icon name="refresh" /> Iniciar sozinho quando o servidor religar
        </span>
        <button type="button" role="switch" className="switch" aria-checked={cfg.autostart} onClick={() => set((c) => ({ ...c, autostart: !c.autostart }))} />
      </label>
      {cfg.mode === "script" && (
        <label className="toggle-row">
          <span>
            <Icon name="shield" /> Rodar como root dentro do contêiner
          </span>
          <button type="button" role="switch" className="switch" aria-checked={cfg.root} onClick={() => set((c) => ({ ...c, root: !c.root }))} />
        </label>
      )}
    </fieldset>
  );
}

// ---------- new service ----------

const STEPS = ["Fonte", "Como rodar", "Acesso e limites"];

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
      toast("Serviço criado. O primeiro deploy começou.");
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
            <h2 id="ns-title">Novo serviço</h2>
            <p className="muted small">O código é copiado para o servidor e roda isolado, com memória e processador limitados.</p>
          </div>
          <button type="button" className="btn ghost icon small" aria-label="Fechar" disabled={pending} onClick={onClose}>
            <Icon name="x" />
          </button>
        </div>
        <ol className="stepper" aria-label="Etapas">
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
            <div className="segmented svc-source" role="group" aria-label="De onde vem o código">
              <button type="button" disabled={pending} aria-pressed={cfg.source === "github"} onClick={() => changeSource("github")}>
                <Icon name="network" /> GitHub
              </button>
              <button type="button" aria-pressed={cfg.source === "local"} disabled={!info.local_root || pending} onClick={() => changeSource("local")}>
                <Icon name="folder" /> Pasta no servidor
              </button>
              <button type="button" aria-pressed={cfg.source === "zip"} disabled={pending} onClick={() => changeSource("zip")}><Icon name="upload" /> Enviar ZIP</button>
            </div>
            {cfg.source === "github" ? (
              <div className="svc-grid">
                <div className="field">
                  <label htmlFor="ns-repo">Repositório</label>
                  <input
                    id="ns-repo"
                    className="input"
                    autoFocus
                    value={cfg.repo}
                    placeholder="dono/projeto ou https://github.com/dono/projeto"
                    onChange={(e) => setCfg((c) => ({ ...c, repo: e.target.value }))}
                    spellCheck={false}
                  />
                  {!info.github_token && <span className="hint">Repositório privado? Configure o acesso ao GitHub antes.</span>}
                </div>
                <div className="field">
                  <label htmlFor="ns-ref">Branch</label>
                  <input id="ns-ref" className="input" value={cfg.ref} onChange={(e) => setCfg((c) => ({ ...c, ref: e.target.value }))} spellCheck={false} />
                </div>
              </div>
            ) : cfg.source === "zip" ? (
              <div className="field svc-zip-upload">
                <label htmlFor="ns-zip">Projeto em .zip</label>
                <input id="ns-zip" className="input" type="file" accept=".zip,application/zip" disabled={inspect.isPending} onChange={(e) => {
                  if (uploaded.current) { void hosting.dropUpload(uploaded.current).catch(() => {}); uploaded.current = ""; }
                  setZipFile(e.target.files?.[0] ?? null); setCfg((c) => ({ ...c, path: "" })); inspect.reset();
                }} />
                <span className="hint">Até 300 MB compactado e 1 GB extraído. Inclua a .env se necessária; seus valores ficam protegidos no servidor. Dependências serão instaladas na preparação.</span>
                {zipFile && <p className="small">{zipFile.name} · {fmtBytes(zipFile.size)}</p>}
                {inspect.isPending && <p className="notice small" role="status">{uploadProgress < 100 ? `Enviando ZIP… ${uploadProgress}%` : "ZIP enviado. Extraindo e analisando o projeto…"}</p>}
              </div>
            ) : (
              <FolderPicker root={info.local_root} value={cfg.path ?? ""} onPick={(p) => setCfg((c) => ({ ...c, path: p }))} />
            )}
            <div className="field">
              <label htmlFor="ns-dir">Subpasta (opcional)</label>
              <input id="ns-dir" className="input" value={cfg.dir} placeholder="ex.: apps/api" onChange={(e) => setCfg((c) => ({ ...c, dir: e.target.value }))} spellCheck={false} />
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
              <label htmlFor="ns-name">Nome</label>
              <input id="ns-name" className="input" value={cfg.name} onChange={(e) => setCfg((c) => ({ ...c, name: e.target.value }))} />
            </div>
            <AccessFields cfg={cfg} set={setCfg} info={info} hosts={info.hosts} />
            {problems.length > 0 && <p className="hint bad">{problems[0]}</p>}
          </div>
        )}

        <div className="dialog-actions">
          {step > 0 && (
            <button type="button" className="btn ghost" onClick={() => setStep(step - 1)}>
              Voltar
            </button>
          )}
          <span className="grow" />
          {step === 0 && (
            <button className="btn primary" disabled={!sourceOk || inspect.isPending}>
              <Icon name="search" /> {inspect.isPending ? "Analisando…" : "Analisar o projeto"}
            </button>
          )}
          {step === 1 && (
            <button className="btn primary">
              Continuar <Icon name="chevronRight" />
            </button>
          )}
          {step === 2 && (
            <button className="btn primary" disabled={problems.length > 0 || create.isPending}>
              <Icon name="download" /> {cfg.source === "local" ? "Mover e implantar" : "Criar e implantar"}
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
      ? `Encontrei ${d.compose_file} com ${d.services?.length ?? 0} serviço(s).`
      : d.mode === "dockerfile"
        ? `Encontrei um Dockerfile${d.port ? ` que expõe a porta ${d.port}` : ""}.`
        : d.found.length > 0
          ? `Encontrei ${d.found.join(", ")}: ${runtimeLabel[d.runtime ?? ""] ?? d.runtime}.`
          : "Não reconheci o tipo do projeto.";
  return (
    <div className="svc-detect">
      <p>
        <Icon name="scan" /> {what} Os campos abaixo já vêm preenchidos; ajuste se precisar.
      </p>
      {v && (
        <p className="small muted">
          {v.Commit ? `Commit ${v.Commit.slice(0, 7)}${v.Branch ? ` em ${v.Branch}` : ""}${v.Message ? `: ${v.Message}` : ""}` : `Pasta sem Git (${v.Label})`}
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
      <nav className="svc-crumbs small" aria-label="Pasta atual">
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
        {q.data?.folders.length === 0 && <li className="muted small">Nenhuma subpasta aqui.</li>}
        {q.data?.folders.map((f) => (
          <li key={f.path} className={value === f.path ? "on" : ""}>
            <button type="button" className="svc-folder" onClick={() => onPick(f.path)} onDoubleClick={() => setAt(f.path)} aria-pressed={value === f.path}>
              <Icon name="folder" />
              <span className="svc-folder-name">{f.name}</span>
              {f.git && <span className="pill small-pill">Git</span>}
              {f.markers?.map((m) => (
                <span key={m} className="pill small-pill">
                  {markerLabel[m] ?? m}
                </span>
              ))}
            </button>
            <button type="button" className="btn ghost icon small" aria-label={`Abrir ${f.name}`} onClick={() => setAt(f.path)}>
              <Icon name="chevronRight" />
            </button>
          </li>
        ))}
      </ul>
      <p className="notice small">Ao implantar, esta pasta será transferida da Nuvem para a área protegida de Serviços. Depois, edite seus arquivos pela aba Arquivos. As dependências são instaladas novamente a cada deploy.</p>
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
      toast(tok ? "Acesso ao GitHub salvo." : "Acesso ao GitHub removido.");
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
        <h2 id="gh-title">Acesso ao GitHub</h2>
        <p className="muted small">
          Só para repositórios privados. No GitHub, crie um token “fine-grained” com permissão de leitura de conteúdo (Contents: Read) apenas nos repositórios
          que vai usar. Ele fica guardado no servidor e nunca volta para a tela.
        </p>
        <div className="field">
          <label htmlFor="gh-tok">Token</label>
          <input id="gh-tok" className="input" type="password" autoComplete="off" value={tok} placeholder={has ? "guardado; cole outro para trocar" : "github_pat_…"} onChange={(e) => setTok(e.target.value.trim())} />
        </div>
        <div className="dialog-actions">
          {has && (
            <button type="button" className="btn danger" onClick={() => (setTok(""), save.mutate())} disabled={save.isPending}>
              Remover
            </button>
          )}
          <span className="grow" />
          <button type="button" className="btn ghost" onClick={onClose}>
            Cancelar
          </button>
          <button className="btn primary" disabled={!tok || save.isPending}>
            Salvar
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
      toast("Serviço excluído.");
      navigate("servicos", undefined);
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
        <h2 id="del-svc">Excluir {svc.name}?</h2>
        <p className="muted small">
          O serviço para e é apagado do servidor com os dados dele (/data{svc.mode === "compose" ? " e os volumes do Compose" : ""}). O repositório ou a pasta de
          origem não são tocados.
        </p>
        <div className="field">
          <label htmlFor="del-typed">
            Digite <strong>{svc.id}</strong> para confirmar
          </label>
          <input id="del-typed" className="input" value={typed} onChange={(e) => setTyped(e.target.value.trim())} autoComplete="off" spellCheck={false} autoFocus />
        </div>
        <div className="dialog-actions">
          <button type="button" className="btn ghost" onClick={onClose}>
            Cancelar
          </button>
          <button className="btn primary danger-fill" disabled={typed !== svc.id || del.isPending}>
            <Icon name="trash" /> Excluir
          </button>
        </div>
      </form>
    </div>
  );
}
