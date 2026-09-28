import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, get, type AuditEntry, type Role, type SystemInfo, type User } from "../lib/api";
import { useSession, useToast } from "../lib/session";
import { fmtBytes, fmtDuration, fmtTime, relative } from "../lib/time";
import { useCameras } from "./LivePage";

const roleLabel: Record<Role, string> = { admin: "Administrador", operator: "Operador", viewer: "Visualizador" };
const stateLabel: Record<string, string> = {
  recording: "Gravando",
  starting: "Conectando",
  backoff: "Tentando reconectar",
  stopped: "Parado",
};

export function SystemPage() {
  const { can } = useSession();
  const sys = useQuery({ queryKey: ["system"], queryFn: () => get<SystemInfo>("/api/system"), refetchInterval: 10_000 });
  const cams = useCameras();
  const s = sys.data;

  return (
    <>
      <header className="page-head">
        <div className="title">
          <h1>Sistema</h1>
          <span className="sub">Gravação, disco, servidor e acessos.</span>
        </div>
        {s && <span className="pill">Versão {s.version}</span>}
      </header>
      {sys.isError && <p className="notice error">Não foi possível ler o estado do servidor.</p>}
      {s && (
        <div className="sys-grid">
          <Storage s={s} />
          <section className="card">
            <h2>Gravadores</h2>
            {(cams.data ?? []).map((c) => {
              const r = s.recorders.find((x) => x.camera_id === c.id);
              const stale = !r || new Date(s.now).getTime() - new Date(r.heartbeat).getTime() > 60_000;
              const ok = r?.state === "recording" && !stale;
              return (
                <div key={c.id} style={{ marginBottom: 16 }}>
                  <div className="toggle-row" style={{ minHeight: 32 }}>
                    <strong>{c.name}</strong>
                    <span className="pill">
                      <span className={`dot ${ok ? "rec" : "bad"}`} />
                      {stale ? "Sem sinal do gravador" : stateLabel[r!.state] ?? r!.state}
                    </span>
                  </div>
                  {r && (
                    <p className="muted" style={{ fontSize: "0.9rem" }}>
                      Desde {fmtTime(r.since)}, {r.restarts} {r.restarts === 1 ? "reinício" : "reinícios"}
                      {c.record_audio ? ", com áudio" : ", sem áudio"}, guarda {c.retention_days} dias
                    </p>
                  )}
                  {r?.last_error && !ok && (
                    <p className="notice error" style={{ marginTop: 6, fontSize: "0.88rem" }}>
                      {r.last_error}
                    </p>
                  )}
                </div>
              );
            })}
          </section>
          <section className="card">
            <h2>Vídeo ao vivo</h2>
            {s.go2rtc_error ? (
              <p className="notice error">Serviço de vídeo ao vivo (go2rtc) fora do ar.</p>
            ) : (
              <dl className="kv">
                {Object.entries(s.go2rtc ?? {}).map(([name, st]) => (
                  <div key={name} style={{ display: "contents" }}>
                    <dt>{name.endsWith("_sub") ? `${name.replace(/_sub$/, "")} (SD)` : `${name} (HD)`}</dt>
                    <dd>{st.online ? `${st.consumers} assistindo` : "Em espera"}</dd>
                  </div>
                ))}
              </dl>
            )}
          </section>
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
          <Account />
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
  // Space recordings may use: the retention limit minus everything else on the disk.
  const budget = s.disk.total * (s.max_disk_pct / 100) - (s.disk.used - recBytes);
  const days = daily > 0 && s.disk.total > 0 ? Math.max(0, Math.floor(budget / daily)) : null;
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
        <p className="muted" style={{ marginBottom: 16 }}>
          {s.disk.total === 0
            ? "O uso do disco só é medido no servidor Linux."
            : "A previsão de dias aparece depois de 10 minutos gravando."}
        </p>
      )}
      {s.disk.total > 0 && (
        <>
          <div className="meter" role="meter" aria-valuenow={s.disk.pct} aria-valuemin={0} aria-valuemax={100} aria-label="Uso do disco">
            <span style={{ width: `${s.disk.pct}%` }} />
            <i style={{ left: `${s.max_disk_pct}%` }} title="Limite: acima disso as gravações mais antigas são apagadas" />
          </div>
          <p className="muted" style={{ fontSize: "0.9rem", marginTop: 6 }}>
            {fmtBytes(s.disk.used)} usados de {fmtBytes(s.disk.total)}. Acima de {s.max_disk_pct}% as gravações mais antigas são apagadas.
          </p>
        </>
      )}
      <dl className="kv" style={{ marginTop: 16 }}>
        <dt>Gravações</dt>
        <dd>{fmtBytes(recBytes)}</dd>
        <dt>Por dia (ritmo atual)</dt>
        <dd>{daily > 0 ? fmtBytes(daily) : "calculando"}</dd>
        <dt>Mais antiga</dt>
        <dd>{oldest ? relative(oldest) : "nenhuma"}</dd>
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
    onError: (e: Error) => toast(e.message.includes("at least") ? "A nova senha precisa de 10 caracteres ou mais." : "Senha atual incorreta.", true),
  });
  return (
    <section className="card">
      <h2>Sua conta</h2>
      <p className="muted" style={{ marginBottom: 16 }}>
        {user.username}, {roleLabel[user.role].toLowerCase()}
      </p>
      <form
        style={{ display: "grid", gap: 10 }}
        onSubmit={(e) => {
          e.preventDefault();
          change.mutate();
        }}
      >
        <input className="input" type="password" autoComplete="current-password" placeholder="Senha atual" aria-label="Senha atual" value={cur} onChange={(e) => setCur(e.target.value)} required />
        <input className="input" type="password" autoComplete="new-password" placeholder="Nova senha" aria-label="Nova senha" minLength={10} value={next} onChange={(e) => setNext(e.target.value)} required />
        <div style={{ display: "flex", gap: 8 }}>
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
                      const p = window.prompt(`Nova senha para ${u.username} (mínimo 10 caracteres):`);
                      if (p) update.mutate({ id: u.id, body: { password: p } });
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
        <input className="input" type="password" autoComplete="new-password" placeholder="Senha (10+ caracteres)" aria-label="Senha" minLength={10} value={pw} onChange={(e) => setPw(e.target.value)} required />
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
