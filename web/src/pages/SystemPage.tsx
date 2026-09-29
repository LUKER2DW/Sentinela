import { useState, type ReactNode } from "react";
import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, get, type AuditEntry, type Camera, type ControlState, type Role, type SystemInfo, type User, fetchSystem } from "../lib/api";
import { useSession, useToast } from "../lib/session";
import { fmtBytes, fmtDuration, fmtTime, relative, spanDays } from "../lib/time";
import { useCameras } from "./LivePage";
import { VolumeMeter } from "./NuvemPage";
import { PasswordField } from "../components/PasswordField";
import { NotificationsCard } from "../components/Notifications";
import { DevicesCard } from "../components/Devices";
import { SecurityCard } from "../components/Security";
import { AddCameraCard } from "../components/AddCamera";
import { DetectionCard } from "../components/Detection";
import { useCompact } from "../lib/viewport";
import { OceanScene } from "../components/Doodles";

const roleLabel: Record<Role, string> = { admin: "Administrador", operator: "Operador", viewer: "Visualizador" };
const stateLabel: Record<string, string> = {
  recording: "Gravando",
  starting: "Conectando",
  backoff: "Tentando reconectar",
  stopped: "Parado",
};

/** Days of recording that fit, from the real rate; null while unknown. */
export function recordingDays(s: SystemInfo): number | null {
  const recBytes = s.storage.reduce((a, x) => a + x.bytes, 0);
  const daily = Object.values(s.daily_bytes).reduce((a, x) => a + x, 0);
  // Space recordings may use: the retention limit minus everything else on the disk.
  const budget = s.disk.total * (s.max_disk_pct / 100) - (s.disk.used - recBytes);
  return daily > 0 && s.disk.total > 0 ? Math.max(0, Math.floor(budget / daily)) : null;
}

export function SystemPage() {
  const { can } = useSession();
  const compact = useCompact();
  const sys = useQuery({ queryKey: ["system"], queryFn: fetchSystem, refetchInterval: 10_000 });
  const s = sys.data;

  return (
    <>
      <header className="page-head">
        <div className="title">
          <h1>Sistema</h1>
          <span className="sub">Gravação, discos, servidor e acessos.</span>
        </div>
        {s && <span className="pill">Versão {s.version}</span>}
      </header>
      {sys.isError && <p className="notice error">Não foi possível ler o estado do servidor.</p>}
      {s && (compact ? <PhoneSections s={s} admin={can("admin")} /> : (
        <div className="stack">
          <DiskWarning s={s} />
          <RecordingWarning s={s} />
          <CloudWarning />
          <div className="grid-3">
            <Storage s={s} />
            <Recorders s={s} />
            <LiveService s={s} />
            <Disks s={s} />
            <Server s={s} />
            <Account />
            <SecurityCard />
            <NotificationsCard />
            <DevicesCard />
            <DetectionCard s={s} />
          </div>
          {can("admin") && <Users />}
          {can("admin") && <AddCameraCard />}
          {can("admin") && <Audit />}
        </div>
      ))}
    </>
  );
}

type Section = "account" | "recording" | "server" | "detection";
const SECTIONS: { id: Section; label: string }[] = [
  { id: "account", label: "Conta" },
  { id: "recording", label: "Gravação" },
  { id: "server", label: "Servidor" },
  { id: "detection", label: "Detecção" },
];
const SECTION_KEY = "sentinela.systemSection";

function savedSection(): Section {
  try {
    const v = localStorage.getItem(SECTION_KEY);
    if (SECTIONS.some((x) => x.id === v)) return v as Section;
  } catch {
    /* private mode */
  }
  return "account";
}

/**
 * Phone layout of the System page: one section at a time behind sticky
 * chips. Users, cameras and the audit log are desktop-only (owner's
 * choice): they are rare, wide, and risky to fat-finger.
 */
function PhoneSections({ s, admin }: { s: SystemInfo; admin: boolean }) {
  const [section, setSection] = useState<Section>(savedSection);
  const pick = (id: Section) => {
    setSection(id);
    try {
      localStorage.setItem(SECTION_KEY, id);
    } catch {
      /* private mode */
    }
    window.scrollTo({ top: 0, behavior: "smooth" });
  };
  const cards: Record<Section, ReactNode> = {
    account: (
      <>
        <Account />
        <SecurityCard />
        <NotificationsCard />
        <DevicesCard />
      </>
    ),
    recording: (
      <>
        <Storage s={s} />
        <Recorders s={s} />
      </>
    ),
    server: (
      <>
        <LiveService s={s} />
        <Disks s={s} />
        <Server s={s} />
      </>
    ),
    detection: <DetectionCard s={s} />,
  };
  const { user, logout } = useSession();
  return (
    <div className="stack">
      <div className="phone-who">
        <OceanScene className="phone-ocean" />
        <div className="who">
          <span className="avatar" aria-hidden="true">
            {user.username.slice(0, 1).toUpperCase()}
          </span>
          <div className="who-text">
            <div className="who-name">{user.username}</div>
            <div className="who-role">{roleLabel[user.role]}</div>
          </div>
          <button className="btn small ghost" onClick={logout}>
            Sair
          </button>
        </div>
      </div>
      {/* No disk/recording/cloud warnings on phones (owner, 2026-09-29):
          they get a page of their own later. */}
      <div className="section-tabs" role="tablist" aria-label="Seções">
        {SECTIONS.map((x) => (
          <button key={x.id} role="tab" aria-selected={section === x.id} onClick={() => pick(x.id)}>
            {x.label}
          </button>
        ))}
      </div>
      <div className="stack section-body" key={section} role="tabpanel">
        {cards[section]}
        {admin && section === "account" && (
          <p className="muted small desk-note">Usuários, câmeras e o registro de ações ficam no computador.</p>
        )}
      </div>
    </div>
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
        <dd>até {Math.max(...(cams.data ?? [{ retention_days: 0 }]).map((c) => c.retention_days))} dias</dd>
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

/** Warns while a camera still talks to the vendor cloud (docs/CAMERA.md §4). */
function CloudWarning() {
  const cams = useCameras();
  const states = useQueries({
    queries: (cams.data ?? []).map((c) => ({
      queryKey: ["control", c.id],
      queryFn: () => get<ControlState>(`/api/cameras/${c.id}/control`),
      staleTime: 5 * 60_000,
      retry: 1,
    })),
  });
  const on = (cams.data ?? []).filter((_, i) => states[i]?.data?.cloud_p2p === true);
  if (!on.length) return null;
  return (
    <div className="notice error" role="alert">
      <strong>Nuvem do fabricante ligada:</strong> {on.map((c) => c.name).join(", ")}{" "}
      {on.length === 1 ? "ainda se conecta" : "ainda se conectam"} ao serviço P2P da XMEye pela internet. Desligue
      “Nuvem”/“P2P” nas configurações de rede da câmera e bloqueie a saída dela para a internet no roteador.
    </div>
  );
}

/** Reconnect storm: this many reconnects in one hour (matches the recorder log). */
const STORM = 5;

function gapText(g: SystemInfo["gaps_24h"][string] | undefined) {
  if (!g) return "";
  if (g.count === 0) return "Sem falhas nas últimas 24 h";
  return `${g.count} ${g.count === 1 ? "falha" : "falhas"} nas últimas 24 h (maior: ${fmtDuration(g.longest_secs * 1000)})`;
}

/** Gap alert: holes > 30 s in the last 24 h, or a reconnect storm right now. */
function RecordingWarning({ s }: { s: SystemInfo }) {
  const cams = useCameras();
  const name = (id: string) => cams.data?.find((c) => c.id === id)?.name ?? id;
  const gaps = Object.entries(s.gaps_24h ?? {}).filter(([, g]) => g.count > 0);
  const storms = s.recorders.filter((r) => r.restarts_1h >= STORM);
  if (!gaps.length && !storms.length) return null;
  return (
    <div className="notice error" role="alert">
      <strong>Gravação com falhas:</strong>{" "}
      {[
        ...gaps.map(([id, g]) => {
          const last = g.recent[g.recent.length - 1];
          return `${name(id)} ficou sem gravar ${g.count}× nas últimas 24 h (a última de ${fmtTime(last.from)} a ${fmtTime(last.to)})`;
        }),
        ...storms.map((r) => `${name(r.camera_id)} reconectou ${r.restarts_1h}× na última hora`),
      ].join("; ")}
      . Veja se a câmera está ligada e na rede; a gravação continua sozinha quando ela volta.
    </div>
  );
}

function DiskWarning({ s }: { s: SystemInfo }) {
  const bad = Object.entries(s.disk_health?.mounts ?? {}).filter(([m, h]) => m !== "/" && h.state !== "ok");
  if (!bad.length) return null;
  const names: Record<string, string> = { "/data": "gravações", "/srv/nas": "Nuvem", "/srv/backup": "backups" };
  return (
    <div className="notice error" role="alert">
      <strong>Disco com sinais de desgaste:</strong>{" "}
      {bad.map(([m, h]) => `${names[m] ?? m} (${h.model}, ${h.uncorrectable} leituras com erro, ${h.pending} setores pendentes)`).join("; ")}.
      {" "}Troque esse disco quando puder. As gravações continuam, mas podem perder trechos se ele piorar.
    </div>
  );
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

function Account() {
  const { user, logout } = useSession();
  const toast = useToast();
  const [cur, setCur] = useState("");
  const [next, setNext] = useState("");
  const change = useMutation({
    mutationFn: () => api("POST", "/api/me/password", { current: cur, new: next }),
    onSuccess: () => {
      toast("Senha alterada. Outros aparelhos foram desconectados.");
      setCur("");
      setNext("");
    },
    onError: (e: Error) => toast(e.message.includes("at least") ? "A nova senha precisa de 8 caracteres ou mais." : "Senha atual incorreta.", true),
  });
  return (
    <section className="card">
      <h2>Sua conta</h2>
      <p className="muted" style={{ marginBottom: 16 }}>
        {user.username}, {roleLabel[user.role].toLowerCase()}
      </p>
      <form
        className="form-stack"
        onSubmit={(e) => {
          e.preventDefault();
          change.mutate();
        }}
      >
        <PasswordField label="Senha atual" value={cur} onChange={setCur} autoComplete="current-password" />
        <PasswordField label="Nova senha" value={next} onChange={setNext} autoComplete="new-password" meter minLength={8} />
        <div className="card-foot row-gap">
          <button className="btn" disabled={change.isPending}>
            Alterar senha
          </button>
          <button type="button" className="btn ghost" onClick={logout}>
            Sair
          </button>
        </div>
      </form>
    </section>
  );
}

function Users() {
  const toast = useToast();
  const qc = useQueryClient();
  const { user: me } = useSession();
  const users = useQuery({ queryKey: ["users"], queryFn: () => get<User[]>("/api/users") });
  const [name, setName] = useState("");
  const [pw, setPw] = useState("");
  const [role, setRole] = useState<Role>("viewer");
  const refresh = () => void qc.invalidateQueries({ queryKey: ["users"] });
  const create = useMutation({
    mutationFn: () => api("POST", "/api/users", { username: name, password: pw, role }),
    onSuccess: () => {
      toast(`Usuário ${name} criado`);
      setName("");
      setPw("");
      refresh();
    },
    onError: (e: Error) => toast(e.message, true),
  });
  const update = useMutation({
    mutationFn: ({ id, body }: { id: number; body: object }) => api("PATCH", `/api/users/${id}`, body),
    onSuccess: refresh,
    onError: (e: Error) => toast(e.message, true),
  });
  const cams = useCameras();
  const multiCam = (cams.data?.length ?? 0) > 1;
  const [camsFor, setCamsFor] = useState<User | null>(null);
  const [resetFor, setResetFor] = useState<User | null>(null);
  const [resetPw, setResetPw] = useState("");
  const remove = useMutation({
    mutationFn: (id: number) => api("DELETE", `/api/users/${id}`),
    onSuccess: refresh,
    onError: (e: Error) => toast(e.message, true),
  });

  return (
    <section className="card span-all">
      <h2>Usuários</h2>
      <div className="table-wrap">
        <table className="table">
          <thead>
            <tr>
              <th>Usuário</th>
              <th>Perfil</th>
              {multiCam && <th>Câmeras</th>}
              <th />
            </tr>
          </thead>
          <tbody>
            {(users.data ?? []).map((u) => (
              <tr key={u.id}>
                <td>{u.username}</td>
                <td>
                  <select className="input" style={{ minHeight: 34 }} value={u.role} aria-label={`Perfil de ${u.username}`} onChange={(e) => update.mutate({ id: u.id, body: { role: e.target.value } })}>
                    {Object.entries(roleLabel).map(([v, l]) => (
                      <option key={v} value={v}>
                        {l}
                      </option>
                    ))}
                  </select>
                </td>
                {multiCam && (
                  <td>
                    {u.role === "admin" ? (
                      <span className="muted">Todas</span>
                    ) : (
                      <button className="btn small ghost" onClick={() => setCamsFor(u)}>
                        {u.cameras.length === 0 ? "Todas" : u.cameras.map((id) => cams.data?.find((c) => c.id === id)?.name ?? id).join(", ")}
                      </button>
                    )}
                  </td>
                )}
                <td style={{ textAlign: "right", whiteSpace: "nowrap" }}>
                  <button
                    className="btn small ghost"
                    onClick={() => {
                      setResetPw("");
                      setResetFor(u);
                    }}
                  >
                    Redefinir senha
                  </button>
                  {u.totp && (
                    <button
                      className="btn small ghost"
                      title="Celular perdido: desliga a verificação em 2 etapas desta conta"
                      onClick={() =>
                        window.confirm(`Desligar a verificação em 2 etapas de ${u.username}? Os acessos de fora dessa conta são encerrados.`) &&
                        update.mutate({ id: u.id, body: { totp_off: true } })
                      }
                    >
                      Desligar 2 etapas
                    </button>
                  )}
                  {u.id !== me.id && (
                    <button className="btn small ghost danger" onClick={() => window.confirm(`Remover ${u.username}?`) && remove.mutate(u.id)}>
                      Remover
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <form
        className="inline-form"
        onSubmit={(e) => {
          e.preventDefault();
          create.mutate();
        }}
      >
        <input className="input" placeholder="Novo usuário" aria-label="Novo usuário" value={name} onChange={(e) => setName(e.target.value)} required pattern="[A-Za-z0-9._\-]{3,32}" />
        <PasswordField label="Senha (8+ caracteres)" hideLabel value={pw} onChange={setPw} autoComplete="new-password" minLength={8} />
        <select className="input" value={role} onChange={(e) => setRole(e.target.value as Role)} aria-label="Perfil">
          {Object.entries(roleLabel).map(([v, l]) => (
            <option key={v} value={v}>
              {l}
            </option>
          ))}
        </select>
        <button className="btn primary" disabled={create.isPending}>
          Criar usuário
        </button>
      </form>
      <p className="faint" style={{ fontSize: "0.85rem", marginTop: 8 }}>
        Visualizador assiste. Operador também move a câmera, liga a luz e fala. Administrador também gerencia usuários.
      </p>
      {camsFor && cams.data && (
        <CameraAccessDialog
          user={camsFor}
          cameras={cams.data}
          onClose={() => setCamsFor(null)}
          onSave={(ids) =>
            update.mutate(
              { id: camsFor.id, body: { cameras: ids } },
              {
                onSuccess: () => {
                  toast(`Câmeras de ${camsFor.username} atualizadas`);
                  setCamsFor(null);
                },
              },
            )
          }
        />
      )}
      {resetFor && (
        <div className="dialog-backdrop" role="dialog" aria-modal="true" aria-labelledby="reset-title" onClick={(e) => e.target === e.currentTarget && setResetFor(null)}>
          <form
            className="dialog"
            onSubmit={(e) => {
              e.preventDefault();
              update.mutate(
                { id: resetFor.id, body: { password: resetPw } },
                {
                  onSuccess: () => {
                    toast(`Senha de ${resetFor.username} redefinida`);
                    setResetFor(null);
                  },
                },
              );
            }}
          >
            <h2 id="reset-title">Nova senha para {resetFor.username}</h2>
            <p className="muted">Os aparelhos conectados com essa conta saem e precisam entrar de novo.</p>
            <PasswordField label="Nova senha" value={resetPw} onChange={setResetPw} autoComplete="new-password" meter minLength={8} autoFocus />
            <div className="dialog-actions">
              <button type="button" className="btn ghost" onClick={() => setResetFor(null)}>
                Cancelar
              </button>
              <button className="btn primary" disabled={update.isPending}>
                Redefinir senha
              </button>
            </div>
          </form>
        </div>
      )}
    </section>
  );
}

const actionLabel: Record<string, string> = {
  login: "Entrou",
  setup: "Configuração inicial",
  "user.create": "Criou usuário",
  "user.delete": "Removeu usuário",
  "user.role": "Mudou perfil",
  "user.password": "Trocou senha",
  "camera.white_light": "Luz branca",
  "camera.day_night": "Modo dia e noite",
  "camera.preset.set": "Salvou posição",
  "camera.preset.clear": "Apagou posição",
  "recording.export": "Exportou vídeo",
  "segment.protect": "Proteção de trecho",
};

function Audit() {
  const audit = useQuery({ queryKey: ["audit"], queryFn: () => get<AuditEntry[]>("/api/audit"), refetchInterval: 30_000 });
  return (
    <section className="card span-all">
      <h2>Histórico de ações</h2>
      <div className="table-wrap" style={{ maxHeight: 360, overflowY: "auto" }}>
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
                <td className="num">{relative(a.at)}</td>
                <td>{a.username || "—"}</td>
                <td>
                  <span className={`dot ${a.ok ? "ok" : "bad"}`} style={{ display: "inline-block", marginRight: 8 }} />
                  {actionLabel[a.action] ?? a.action}
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

// Which cameras a non-admin user sees. Nothing ticked = every camera.
function CameraAccessDialog({ user, cameras, onClose, onSave }: { user: User; cameras: Camera[]; onClose: () => void; onSave: (ids: string[]) => void }) {
  const [picked, setPicked] = useState<string[]>(user.cameras);
  const toggle = (id: string) => setPicked((p) => (p.includes(id) ? p.filter((x) => x !== id) : [...p, id]));
  return (
    <div className="dialog-backdrop" role="dialog" aria-modal="true" aria-labelledby="cams-title" onClick={(e) => e.target === e.currentTarget && onClose()}>
      <form
        className="dialog"
        onSubmit={(e) => {
          e.preventDefault();
          onSave(picked.length === cameras.length ? [] : picked);
        }}
      >
        <h2 id="cams-title">Câmeras de {user.username}</h2>
        <p className="muted">Ao vivo, gravações, eventos e avisos só das câmeras marcadas. Nenhuma marcada = todas.</p>
        {cameras.map((c) => (
          <label key={c.id} className="check-row">
            <input type="checkbox" checked={picked.includes(c.id)} onChange={() => toggle(c.id)} /> {c.name}
          </label>
        ))}
        <div className="dialog-actions">
          <button type="button" className="btn ghost" onClick={onClose}>
            Cancelar
          </button>
          <button className="btn primary">Salvar</button>
        </div>
      </form>
    </div>
  );
}
