import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, get, type AuditEntry, type Role, type SystemInfo, type User, fetchSystem } from "../lib/api";
import { useSession, useToast } from "../lib/session";
import { fmtBytes, fmtDuration, fmtTime, relative } from "../lib/time";
import { useCameras } from "./LivePage";
import { VolumeMeter } from "./NuvemPage";
import { PasswordField } from "../components/PasswordField";

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
      {s && (
        <div className="stack">
          <DiskWarning s={s} />
          <div className="grid-3">
            <Storage s={s} />
            <Recorders s={s} />
            <LiveService s={s} />
            <Disks s={s} />
            <Server s={s} />
            <Account />
          </div>
          {can("admin") && <Users />}
          {can("admin") && <Audit />}
        </div>
      )}
    </>
  );
}

function Storage({ s }: { s: SystemInfo }) {
  const recBytes = s.storage.reduce((a, x) => a + x.bytes, 0);
  const daily = Object.values(s.daily_bytes).reduce((a, x) => a + x, 0);
  const days = recordingDays(s);
  const oldest = s.storage.map((x) => x.oldest).sort()[0];
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
                </p>
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
      <p className="muted small card-foot">Gravações acima de {s.max_disk_pct}% do disco apagam as mais antigas.</p>
    </section>
  );
}

// Volume id -> host mount point used by scripts/disk-health.sh.
function volumeMount(id: string) {
  return ({ recordings: "/data", nuvem: "/srv/nas", extra: "/srv/nas/Extra" } as Record<string, string>)[id] ?? "";
}

function DiskWarning({ s }: { s: SystemInfo }) {
  const bad = Object.entries(s.disk_health?.mounts ?? {}).filter(([m, h]) => m !== "/" && h.state !== "ok");
  if (!bad.length) return null;
  const names: Record<string, string> = { "/data": "gravações", "/srv/nas": "Nuvem", "/srv/nas/Extra": "Nuvem Extra" };
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
