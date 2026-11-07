import { type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { get, type AuditEntry, type SystemInfo, fetchSystem } from "../lib/api";
import { useSession } from "../lib/session";
import { fmtBytes, fmtDuration, fmtTime, relative, spanDays } from "../lib/time";
import { useCameras } from "./LivePage";
import { VolumeMeter } from "./NuvemPage";
import { AddCameraCard } from "../components/AddCamera";
import { ReopenSetupGuide } from "../components/SetupGuide";
import { ChangelogCard, UpdatesCard } from "../components/Changelog";
import { DetectionCard, OverlayCard } from "../components/Detection";
import { useCompact } from "../lib/viewport";
import { MoreMenu } from "../components/MoreMenu";
import { Icon } from "../components/Icon";
import { NoticesPanel, useSystemNotices } from "../components/Notices";
import { UsersAdmin, auditLabel } from "../components/UsersAdmin";
import { href, navigate } from "../lib/router";

const stateLabel: Record<string, string> = {
  recording: "Gravando",
  starting: "Conectando",
  backoff: "Tentando reconectar",
  stopped: "Parado",
};

/** How long recordings are kept: 0 days means until the disk is full, then the oldest are overwritten. */
export function retentionLabel(cams: { retention_days: number }[]): string {
  if (cams.length === 0 || cams.some((c) => c.retention_days <= 0)) return "até encher o disco (as mais antigas são substituídas)";
  return `até ${Math.max(...cams.map((c) => c.retention_days))} dias`;
}

/** Every camera's motion events are arriving (unknown counts as fine until the watcher reports). */
export function eventsOk(s: Pick<SystemInfo, "event_watch">): boolean {
  return Object.values(s.event_watch ?? {}).every((w) => w.ok);
}

/** Days of recording that fit, from the real rate; null while unknown. */
export function recordingDays(s: SystemInfo): number | null {
  const recBytes = s.storage.reduce((a, x) => a + x.bytes, 0);
  const daily = Object.values(s.daily_bytes).reduce((a, x) => a + x, 0);
  // Space recordings may use: the retention limit minus everything else on the disk.
  const budget = s.disk.total * (s.max_disk_pct / 100) - (s.disk.used - recBytes);
  return daily > 0 && s.disk.total > 0 ? Math.max(0, Math.floor(budget / daily)) : null;
}

type Section = "overview" | "recording" | "cameras" | "detection" | "server" | "updates" | "users" | "audit";
const SECTIONS: { id: Section; label: string; icon: string; admin?: boolean }[] = [
  { id: "overview", label: "Visão geral", icon: "gauge" },
  { id: "recording", label: "Gravação", icon: "recordings" },
  { id: "cameras", label: "Câmeras", icon: "video" },
  { id: "detection", label: "Detecção", icon: "scan" },
  { id: "server", label: "Servidor e discos", icon: "server" },
  { id: "updates", label: "Atualizações", icon: "refresh" },
  { id: "users", label: "Usuários", icon: "users", admin: true },
  { id: "audit", label: "Registro de ações", icon: "history", admin: true },
];

export function SystemPage({ params }: { params: URLSearchParams }) {
  const { can } = useSession();
  const compact = useCompact();
  const sys = useQuery({ queryKey: ["system"], queryFn: fetchSystem, refetchInterval: 10_000 });
  const s = sys.data;
  const { unread } = useSystemNotices();
  // Users, the audit log and cameras stay on the computer (owner's choice):
  // rare, wide, and risky to fat-finger on a phone.
  const sections = SECTIONS.filter((x) => (!x.admin || can("admin")) && !(compact && (x.admin || x.id === "cameras")));
  const asked = params.get("s") as Section | null;
  const section: Section = sections.some((x) => x.id === asked) ? asked! : "overview";
  const pick = (id: Section) => navigate("system", { s: id });

  // Phone: "Mais" is a menu of everything that is not on the tab bar; each
  // row opens its page with a way back.
  if (compact && !asked) return <MoreMenu version={s?.version} unread={unread.length} />;

  return (
    <>
      <header className="page-head">
        <div className="title">
          <h1>{compact ? SECTIONS.find((x) => x.id === section)?.label : "Sistema"}</h1>
          {!compact && <span className="sub">Gravação, câmeras, servidor{can("admin") ? " e usuários" : ""}.</span>}
        </div>
        {s && !compact && <span className="pill">Versão {s.version}</span>}
      </header>
      <div className="sys-layout">
        {!compact && (
          <nav className="sys-nav" role="tablist" aria-label="Categorias">
            {sections.map((x) => (
              <button key={x.id} role="tab" aria-selected={section === x.id} onClick={() => pick(x.id)}>
                <Icon name={x.icon} />
                <span>{x.label}</span>
                {x.id === "overview" && unread.length > 0 && <span className="count-badge">{unread.length}</span>}
              </button>
            ))}
            <a className="sys-nav-link" href={href("conta")}>
              <Icon name="user" />
              <span>Minha conta</span>
            </a>
          </nav>
        )}
        <div className="sys-body" key={section} role="tabpanel">
          {sys.isError && <p className="notice error">Não foi possível ler o estado do servidor.</p>}
          {s && <SectionBody section={section} s={s} admin={can("admin")} />}
        </div>
      </div>
    </>
  );
}

function SectionBody({ section, s, admin }: { section: Section; s: SystemInfo; admin: boolean }): ReactNode {
  switch (section) {
    case "overview":
      return (
        <div className="stack">
          {admin && (
            <div className="row-gap">
              <ReopenSetupGuide />
            </div>
          )}
          <NoticesPanel />
          <StatusTiles s={s} />
          <div className="grid-2">
            <Storage s={s} />
            <Recorders s={s} />
          </div>
        </div>
      );
    case "updates":
      return (
        <div className="stack">
          <UpdatesCard s={s} />
          <ChangelogCard />
        </div>
      );
    case "recording":
      return (
        <div className="grid-2">
          <Storage s={s} />
          <Recorders s={s} />
        </div>
      );
    case "cameras":
      return (
        <div className="stack">
          <div className="grid-2">
            <CamerasCard />
            <LiveService s={s} />
          </div>
          {admin && <AddCameraCard />}
        </div>
      );
    case "detection":
      return (
        <div className="stack">
          <DetectionCard s={s} />
          <OverlayCard />
        </div>
      );
    case "server":
      return (
        <div className="grid-2">
          <Server s={s} />
          <Disks s={s} />
        </div>
      );
    case "users":
      return <UsersAdmin />;
    case "audit":
      return <Audit />;
  }
}

/** One line per area, green or not, on the overview. */
function StatusTiles({ s }: { s: SystemInfo }) {
  const days = recordingDays(s);
  const recOk =
    s.recorders.length > 0 && s.recorders.every((r) => r.state === "recording" && new Date(s.now).getTime() - new Date(r.heartbeat).getTime() < 60_000);
  const disksOk = Object.entries(s.disk_health?.mounts ?? {}).every(([m, h]) => m === "/" || h.state === "ok");
  const tiles = [
    { icon: "recordings", label: "Gravação", ok: recOk, value: recOk ? "Gravando" : "Com problema", to: "recording" },
    { icon: "disk", label: "Espaço", ok: days === null || days >= 7, value: days === null ? "calculando" : `${days} ${days === 1 ? "dia cabe" : "dias cabem"}`, to: "recording" },
    {
      icon: "server",
      label: "Servidor",
      ok: !s.host.supported || s.host.cpu_pct < 85,
      value: s.host.supported ? `${s.host.cpu_pct.toFixed(0)}% CPU · ${fmtBytes(s.host.mem_available)} livres` : "—",
      to: "server",
    },
    { icon: "disk", label: "Discos", ok: disksOk, value: disksOk ? "Saudáveis" : "Atenção", to: "server" },
    {
      icon: "scan",
      label: "Detector",
      ok: !s.detector || s.detector.healthy,
      value: !s.detector ? "desligado" : s.detector.healthy ? "Analisando" : "Sem sinal",
      to: "detection",
    },
    {
      icon: "bell",
      label: "Eventos da câmera",
      ok: eventsOk(s),
      value: eventsOk(s) ? "Recebendo" : "Sem eventos de movimento",
      to: "detection",
    },
  ];
  return (
    <div className="status-tiles">
      {tiles.map((t) => (
        <a key={t.label} className={`status-tile${t.ok ? "" : " bad"}`} href={href("system", { s: t.to })}>
          <Icon name={t.icon} />
          <span className="muted small">{t.label}</span>
          <strong>{t.value}</strong>
        </a>
      ))}
    </div>
  );
}

function CamerasCard() {
  const cams = useCameras();
  return (
    <section className="card">
      <h2>Câmeras</h2>
      <div className="rows">
        {(cams.data ?? []).map((c) => (
          <div key={c.id} className="status-row">
            <div>
              <strong>{c.name}</strong>
              <p className="muted small">
                {c.id} · {c.retention_days > 0 ? `guarda ${c.retention_days} dias` : "guarda até encher o disco"} · áudio {c.record_audio ? "gravado" : "desligado"}
              </p>
            </div>
            <a className="btn small ghost" href={href("live", { view: "single", cam: c.id })}>
              Ao vivo
            </a>
          </div>
        ))}
      </div>
      <p className="muted small card-foot">Nome, retenção e áudio ficam em /etc/sentinela/config.yaml no servidor.</p>
    </section>
  );
}

function Audit() {
  const audit = useQuery({ queryKey: ["audit"], queryFn: () => get<AuditEntry[]>("/api/audit"), refetchInterval: 30_000 });
  return (
    <section className="card">
      <h2>Registro de ações</h2>
      <div className="table-wrap audit-wrap">
        <table className="table">
          <thead>
            <tr>
              <th>Quando</th>
              <th>Quem</th>
              <th>O quê</th>
              <th>Detalhe</th>
            </tr>
          </thead>
          <tbody>
            {(audit.data ?? []).map((a) => (
              <tr key={a.id}>
                <td className="num" title={new Date(a.at).toLocaleString("pt-BR")}>
                  {relative(a.at)}
                </td>
                <td>{a.username || "—"}</td>
                <td>
                  <span className={`dot ${a.ok ? "ok" : "bad"}`} style={{ display: "inline-block", marginRight: 8 }} />
                  {auditLabel(a.action)}
                </td>
                <td className="muted">{[a.target, a.detail].filter(Boolean).join(" ")}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

function Storage({ s }: { s: SystemInfo }) {
  const recBytes = s.storage.reduce((a, x) => a + x.bytes, 0);
  const daily = Object.values(s.daily_bytes).reduce((a, x) => a + x, 0);
  const days = recordingDays(s);
  const oldest = s.storage.map((x) => x.oldest).sort()[0];
  const onDisk = spanDays(s.storage);
  return (
    <section className="card">
      <h2>Armazenamento</h2>
      {days !== null ? (
        <p className="big">
          <b>{days}</b>
          <span className="muted">{days === 1 ? "dia de gravação cabe" : "dias de gravação cabem"} no disco</span>
        </p>
      ) : (
        <p className="big">
          <b>–</b>
          <span className="muted">{s.disk.total === 0 ? "disco medido só no servidor" : "calculando o ritmo…"}</span>
        </p>
      )}
      <dl className="kv card-foot">
        <dt>Gravações</dt>
        <dd>{fmtBytes(recBytes)}</dd>
        <dt>Por dia</dt>
        <dd>{daily > 0 ? fmtBytes(daily) : "calculando"}</dd>
        <dt>No disco agora</dt>
        <dd>{onDisk !== null ? `${onDisk.toLocaleString("pt-BR", { maximumFractionDigits: 1 })} ${onDisk === 1 ? "dia" : "dias"}` : "nenhuma"}</dd>
        <dt>Mais antiga</dt>
        <dd>{oldest ? relative(oldest) : "nenhuma"}</dd>
      </dl>
    </section>
  );
}

function Recorders({ s }: { s: SystemInfo }) {
  const cams = useCameras();
  return (
    <section className="card">
      <h2>Gravadores</h2>
      <div className="rows">
        {(cams.data ?? []).map((c) => {
          const r = s.recorders.find((x) => x.camera_id === c.id);
          const stale = !r || new Date(s.now).getTime() - new Date(r.heartbeat).getTime() > 60_000;
          const ok = r?.state === "recording" && !stale;
          return (
            <div key={c.id} className="status-row">
              <div>
                <strong>{c.name}</strong>
                <p className="muted small">
                  {r ? `Desde ${fmtTime(r.since)}, ${r.restarts} ${r.restarts === 1 ? "reinício" : "reinícios"}` : "Aguardando o gravador"}
                  {r && r.restarts_1h > 0 && ` (${r.restarts_1h} na última hora)`}
                </p>
                <p className="muted small">{gapText(s.gaps_24h?.[c.id])}</p>
                {r?.last_error && !ok && <p className="notice error small">{r.last_error}</p>}
              </div>
              <span className="pill">
                <span className={`dot ${ok ? "rec" : "bad"}`} />
                {stale ? "Sem sinal" : stateLabel[r!.state] ?? r!.state}
              </span>
            </div>
          );
        })}
      </div>
      <dl className="kv card-foot">
        <dt>Áudio</dt>
        <dd>{cams.data?.every((c) => c.record_audio) ? "gravado" : "desligado"}</dd>
        <dt>Guardar por</dt>
        <dd>{retentionLabel(cams.data ?? [])}</dd>
      </dl>
    </section>
  );
}

function LiveService({ s }: { s: SystemInfo }) {
  return (
    <section className="card">
      <h2>Vídeo ao vivo</h2>
      {s.go2rtc_error ? (
        <p className="notice error">Serviço de vídeo ao vivo (go2rtc) fora do ar.</p>
      ) : (
        <div className="rows">
          {Object.entries(s.go2rtc ?? {})
            .sort(([a], [b]) => a.localeCompare(b))
            .map(([name, st]) => (
              <div key={name} className="status-row">
                <strong>{name.endsWith("_sub") ? `${name.replace(/_sub$/, "")} (SD)` : `${name} (HD)`}</strong>
                <span className="pill">
                  <span className={`dot ${st.online ? "ok" : ""}`} />
                  {st.online ? `${st.consumers} assistindo` : "Em espera"}
                </span>
              </div>
            ))}
        </div>
      )}
      <p className="muted small card-foot">O vídeo só é puxado da câmera enquanto alguém assiste.</p>
    </section>
  );
}

function Disks({ s }: { s: SystemInfo }) {
  return (
    <section className="card">
      <h2>Discos</h2>
      <div className="stack-sm">
        {(s.volumes ?? []).length === 0 && <p className="muted">Nenhum disco configurado.</p>}
        {(s.volumes ?? []).map((v) => (
          <VolumeMeter key={v.id} v={v} health={s.disk_health?.mounts[volumeMount(v.id)]} />
        ))}
      </div>
      <Backup b={s.backup} now={s.now} />
      <p className="muted small card-foot">Gravações acima de {s.max_disk_pct}% do disco apagam as mais antigas.</p>
    </section>
  );
}

const resticLabel: Record<string, string> = {
  ok: "em dia",
  off: "sem disco externo configurado",
  disk_missing: "disco externo desconectado",
  failed: "falhou",
};

function Backup({ b, now }: { b: SystemInfo["backup"]; now: string }) {
  if (!b) return null;
  // Nightly job: more than 36 h without a good copy means it is not running.
  const stale = !b.db_at || new Date(now).getTime() - new Date(b.db_at).getTime() > 36 * 3_600_000;
  return (
    <dl className="kv card-foot">
      <dt>Cópia do banco</dt>
      <dd className={stale || !b.db_ok ? "bad" : undefined}>{b.db_at ? relative(b.db_at) : "nunca"}</dd>
      <dt>Disco externo</dt>
      <dd className={b.restic === "failed" || b.restic === "disk_missing" ? "bad" : undefined}>
        {b.restic === "ok" && b.restic_at ? relative(b.restic_at) : resticLabel[b.restic] ?? b.restic}
      </dd>
    </dl>
  );
}

// Volume id -> host mount point used by scripts/disk-health.sh.
function volumeMount(id: string) {
  return ({ recordings: "/data", nuvem: "/srv/nas", backup: "/srv/backup" } as Record<string, string>)[id] ?? "";
}

function gapText(g: SystemInfo["gaps_24h"][string] | undefined) {
  if (!g) return "";
  if (g.count === 0) return "Sem falhas nas últimas 24 h";
  return `${g.count} ${g.count === 1 ? "falha" : "falhas"} nas últimas 24 h (maior: ${fmtDuration(g.longest_secs * 1000)})`;
}

function Server({ s }: { s: SystemInfo }) {
  return (
    <section className="card">
      <h2>Servidor</h2>
      <dl className="kv">
        {s.host.supported && (
          <>
            <dt>Processador</dt>
            <dd>
              {s.host.cpu_pct.toFixed(0)}% de {s.host.num_cpu} núcleos
            </dd>
            <dt>Memória livre</dt>
            <dd>
              {fmtBytes(s.host.mem_available)} de {fmtBytes(s.host.mem_total)}
            </dd>
            <dt>Ligado há</dt>
            <dd>{fmtDuration(s.host.uptime_ns / 1e6)}</dd>
          </>
        )}
        <dt>Memória do Sentinela</dt>
        <dd>{fmtBytes(s.host.proc_rss)}</dd>
        <dt>Serviço iniciado</dt>
        <dd>{relative(s.started)}</dd>
      </dl>
    </section>
  );
}

