import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, get, type AuditEntry, type Camera, type Role, type User } from "../lib/api";
import { useSession, useToast } from "../lib/session";
import { fmtBytes, fmtDate, relative } from "../lib/time";
import { useCameras } from "../pages/LivePage";
import { PasswordField } from "./PasswordField";
import { deviceName } from "./Devices";
import { Icon } from "./Icon";

export const roleLabel: Record<Role, string> = { admin: "Administrador", operator: "Operador", viewer: "Visualizador" };

const ROLES: { role: Role; icon: string; text: string }[] = [
  { role: "viewer", icon: "eye", text: "Assiste ao vivo e às gravações, vê eventos e lê a Nuvem." },
  { role: "operator", icon: "live", text: "Também move a câmera, liga a luz, fala e envia arquivos para a Nuvem." },
  { role: "admin", icon: "shield", text: "Tudo, inclusive usuários, câmeras e configurações do sistema." },
];

interface Usage {
  bytes: number;
  files: number;
  folders: number;
  partial?: boolean;
}

interface UserDetail {
  user: User;
  last_login: string;
  sessions: { id: string; created_at: string; expires_at: string; user_agent: string; mfa: boolean }[];
  audit: AuditEntry[];
  nuvem?: { folder: string; files: Usage; trash: Usage };
}

const actionLabel: Record<string, string> = {
  login: "Entrou",
  setup: "Configuração inicial",
  "user.create": "Criou usuário",
  "user.delete": "Removeu usuário",
  "user.role": "Mudou perfil",
  "user.password": "Trocou senha",
  "user.disabled": "Ativou/desativou conta",
  "user.cameras": "Mudou câmeras",
  "notify.mode": "Modo de avisos",
  "remote.app_key": "Pareou o app",
  "camera.white_light": "Luz branca",
  "camera.day_night": "Modo dia e noite",
  "camera.preset.set": "Salvou posição",
  "camera.preset.clear": "Apagou posição",
  "recording.export": "Exportou vídeo",
  "recording.timelapse": "Timelapse",
  "segment.protect": "Proteção de trecho",
  "nas.upload": "Enviou arquivo",
  "nas.mkdir": "Criou pasta",
  "nas.move": "Moveu/renomeou",
  "nas.delete": "Apagou arquivo",
};
export const auditLabel = (a: string) => actionLabel[a] ?? a;

function initials(name: string) {
  return name.slice(0, 1).toUpperCase();
}

/** Users page (admins): list, details, create, change, disable, delete. */
export function UsersAdmin() {
  const users = useQuery({ queryKey: ["users"], queryFn: () => get<User[]>("/api/users") });
  const [pick, setPick] = useState<number | null>(null);
  const [creating, setCreating] = useState(false);
  const [filter, setFilter] = useState("");
  const list = useMemo(
    () => (users.data ?? []).filter((u) => !filter || u.username.toLowerCase().includes(filter.toLowerCase())),
    [users.data, filter],
  );
  const current = pick ?? list[0]?.id ?? null;

  return (
    <div className="users-admin">
      <section className="card users-list">
        <div className="card-head">
          <h2>Usuários</h2>
          <span className="muted small">{users.data?.length ?? 0}</span>
        </div>
        <div className="users-tools">
          <label className="search">
            <Icon name="search" />
            <input className="input" placeholder="Procurar" aria-label="Procurar usuário" value={filter} onChange={(e) => setFilter(e.target.value)} />
          </label>
          <button className="btn primary" onClick={() => setCreating(true)}>
            <Icon name="plus" /> Novo usuário
          </button>
        </div>
        <ul className="user-rows">
          {list.map((u) => (
            <li key={u.id}>
              <button className={`user-row${current === u.id ? " on" : ""}${u.disabled ? " off" : ""}`} onClick={() => setPick(u.id)}>
                <span className={`avatar role-${u.role}`} aria-hidden="true">
                  {initials(u.username)}
                </span>
                <span className="user-row-text">
                  <strong>{u.username}</strong>
                  <span className="muted small">
                    {roleLabel[u.role]}
                    {u.disabled ? " · desativado" : ""}
                  </span>
                </span>
                {u.totp && (
                  <span className="pill ok small-pill" title="Verificação em 2 etapas ativa">
                    2 etapas
                  </span>
                )}
              </button>
            </li>
          ))}
        </ul>
      </section>
      {current !== null ? <UserPanel key={current} id={current} onGone={() => setPick(null)} /> : <section className="card" />}
      {creating && <CreateUserDialog onClose={() => setCreating(false)} onCreated={(id) => setPick(id)} />}
    </div>
  );
}

function UserPanel({ id, onGone }: { id: number; onGone: () => void }) {
  const { user: me } = useSession();
  const toast = useToast();
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["user", id], queryFn: () => get<UserDetail>(`/api/users/${id}`) });
  const cams = useCameras();
  const [dialog, setDialog] = useState<"password" | "cameras" | "delete" | null>(null);
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ["users"] });
    void qc.invalidateQueries({ queryKey: ["user", id] });
  };
  const update = useMutation({
    mutationFn: (body: object) => api("PATCH", `/api/users/${id}`, body),
    onSuccess: refresh,
    onError: (e: Error) => toast(translate(e.message), true),
  });
  const remove = useMutation({
    mutationFn: () => api("DELETE", `/api/users/${id}`),
    onSuccess: () => {
      toast("Usuário removido.");
      void qc.invalidateQueries({ queryKey: ["users"] });
      onGone();
    },
    onError: (e: Error) => toast(translate(e.message), true),
  });

  const d = q.data;
  if (!d) return <section className="card user-panel">{q.isError ? <p className="notice error">Não foi possível ler o usuário.</p> : <p className="muted">Carregando…</p>}</section>;
  const u = d.user;
  const self = u.id === me.id;
  const multiCam = (cams.data?.length ?? 0) > 1;

  return (
    <section className="card user-panel">
      <header className="user-head">
        <span className={`avatar big role-${u.role}`} aria-hidden="true">
          {initials(u.username)}
        </span>
        <div className="user-head-text">
          <h2>
            {u.username} {self && <span className="pill small-pill">você</span>}
          </h2>
          <span className="muted small">
            {roleLabel[u.role]} · conta criada em {fmtDate(u.created_at)} · {d.last_login ? `último acesso ${relative(d.last_login)}` : "nunca entrou"}
          </span>
        </div>
        <span className={`pill${u.disabled ? "" : " ok"}`}>
          <span className={`dot ${u.disabled ? "bad" : "ok"}`} /> {u.disabled ? "Desativado" : "Ativo"}
        </span>
      </header>

      <div className="user-grid">
        <div className="user-block">
          <h3>Perfil</h3>
          <div className="role-pick compact">
            {ROLES.map((r) => (
              <button
                key={r.role}
                className={`role-card${u.role === r.role ? " on" : ""}`}
                aria-pressed={u.role === r.role}
                disabled={update.isPending || (self && r.role !== "admin")}
                onClick={() => u.role !== r.role && update.mutate({ role: r.role }, { onSuccess: () => toast(`${u.username} agora é ${roleLabel[r.role].toLowerCase()}.`) })}
              >
                <Icon name={r.icon} />
                <strong>{roleLabel[r.role]}</strong>
              </button>
            ))}
          </div>
          <p className="muted small">{ROLES.find((r) => r.role === u.role)?.text}</p>
          {multiCam && u.role !== "admin" && (
            <p className="small">
              Câmeras: {u.cameras.length === 0 ? "todas" : u.cameras.map((c) => cams.data?.find((x) => x.id === c)?.name ?? c).join(", ")}{" "}
              <button className="linkish" onClick={() => setDialog("cameras")}>
                mudar
              </button>
            </p>
          )}
        </div>

        <div className="user-block">
          <h3>Nuvem</h3>
          {d.nuvem ? (
            <dl className="kv">
              <dt>Pasta</dt>
              <dd>{d.nuvem.folder}</dd>
              <dt>Arquivos</dt>
              <dd>
                {d.nuvem.files.files} em {d.nuvem.files.folders} {d.nuvem.files.folders === 1 ? "pasta" : "pastas"}
              </dd>
              <dt>Ocupa</dt>
              <dd>
                {fmtBytes(d.nuvem.files.bytes)}
                {d.nuvem.files.partial ? "+" : ""}
              </dd>
              <dt>Na lixeira</dt>
              <dd>{fmtBytes(d.nuvem.trash.bytes)}</dd>
            </dl>
          ) : (
            <p className="muted small">A Nuvem não está montada neste servidor.</p>
          )}
        </div>

        <div className="user-block">
          <h3>Segurança</h3>
          <dl className="kv">
            <dt>2 etapas</dt>
            <dd>{u.totp ? "ativa" : "desligada"}</dd>
            <dt>Aparelhos</dt>
            <dd>{d.sessions.length}</dd>
          </dl>
          <ul className="mini-list">
            {d.sessions.slice(0, 5).map((s) => (
              <li key={s.id}>
                <span>{deviceName(s.user_agent)}</span>
                <span className="muted small">
                  {s.mfa ? "de fora · " : ""}desde {fmtDate(s.created_at)}
                </span>
              </li>
            ))}
          </ul>
        </div>

        <div className="user-block span-2">
          <h3>Últimas ações</h3>
          {d.audit.length === 0 ? (
            <p className="muted small">Nada registrado ainda.</p>
          ) : (
            <ul className="mini-list audit-mini">
              {d.audit.map((a) => (
                <li key={a.id}>
                  <span>
                    <span className={`dot ${a.ok ? "ok" : "bad"}`} /> {auditLabel(a.action)}
                    <span className="muted small"> {[a.target, a.detail].filter(Boolean).join(" ")}</span>
                  </span>
                  <span className="muted small num">{relative(a.at)}</span>
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>

      <footer className="user-actions">
        <button className="btn" onClick={() => setDialog("password")}>
          <Icon name="lock" /> Redefinir senha
        </button>
        {u.totp && (
          <button
            className="btn"
            onClick={() =>
              window.confirm(`Desligar a verificação em 2 etapas de ${u.username}? Os acessos de fora dessa conta são encerrados.`) &&
              update.mutate({ totp_off: true }, { onSuccess: () => toast("Verificação em 2 etapas desligada.") })
            }
          >
            <Icon name="shield" /> Desligar 2 etapas
          </button>
        )}
        <span className="grow" />
        {!self && (
          <>
            <button
              className="btn"
              onClick={() =>
                (u.disabled || window.confirm(`Desativar ${u.username}? A conta sai de todos os aparelhos e não entra até ser reativada.`)) &&
                update.mutate({ disabled: !u.disabled }, { onSuccess: () => toast(u.disabled ? "Conta reativada." : "Conta desativada.") })
              }
            >
              <Icon name={u.disabled ? "check" : "x"} /> {u.disabled ? "Reativar conta" : "Desativar conta"}
            </button>
            <button className="btn danger" onClick={() => setDialog("delete")}>
              <Icon name="trash" /> Excluir
            </button>
          </>
        )}
      </footer>

      {dialog === "password" && <ResetPasswordDialog user={u} onClose={() => setDialog(null)} onSave={(pw) => update.mutateAsync({ password: pw })} />}
      {dialog === "cameras" && cams.data && (
        <CameraAccessDialog
          user={u}
          cameras={cams.data}
          onClose={() => setDialog(null)}
          onSave={(ids) => update.mutate({ cameras: ids }, { onSuccess: () => (toast(`Câmeras de ${u.username} atualizadas`), setDialog(null)) })}
        />
      )}
      {dialog === "delete" && <DeleteUserDialog user={u} onClose={() => setDialog(null)} onDelete={() => remove.mutate()} busy={remove.isPending} />}
    </section>
  );
}

function translate(msg: string): string {
  if (msg.includes("last admin")) return "É preciso manter pelo menos um administrador ativo.";
  if (msg.includes("yourself")) return "Você não pode fazer isso com a sua própria conta.";
  if (msg.includes("already exists")) return "Já existe um usuário com esse nome.";
  if (msg.includes("at least")) return "A senha precisa de 8 caracteres ou mais.";
  if (msg.includes("invalid username")) return "Nome inválido: use 3 a 32 letras, números, ponto, hífen ou sublinhado.";
  return msg;
}

/** A random, readable password: 4 groups of 4 (no look-alike characters). */
function generatePassword(): string {
  const chars = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKMNPQRSTUVWXYZ23456789";
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  const s = [...bytes].map((b) => chars[b % chars.length]).join("");
  return s.match(/.{4}/g)!.join("-");
}

function CreateUserDialog({ onClose, onCreated }: { onClose: () => void; onCreated: (id: number) => void }) {
  const toast = useToast();
  const qc = useQueryClient();
  const cams = useCameras();
  const [name, setName] = useState("");
  const [pw, setPw] = useState("");
  const [role, setRole] = useState<Role>("viewer");
  const [picked, setPicked] = useState<string[]>([]);
  const [shown, setShown] = useState(false);
  const valid = /^[A-Za-z0-9._-]{3,32}$/.test(name);
  const create = useMutation({
    mutationFn: () => api<User>("POST", "/api/users", { username: name, password: pw, role, cameras: role === "admin" ? [] : picked }),
    onSuccess: (u) => {
      toast(`Usuário ${u.username} criado.`);
      void qc.invalidateQueries({ queryKey: ["users"] });
      onCreated(u.id);
      onClose();
    },
    onError: (e: Error) => toast(translate(e.message), true),
  });
  return (
    <div className="dialog-backdrop" role="dialog" aria-modal="true" aria-labelledby="new-user" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <form
        className="dialog wide create-user"
        onSubmit={(e) => {
          e.preventDefault();
          if (valid && pw.length >= 8) create.mutate();
        }}
      >
        <div className="dialog-head">
          <div>
            <h2 id="new-user">Novo usuário</h2>
            <p className="muted small">A pessoa entra com esse nome e senha; ela pode trocar a senha depois em Minha conta.</p>
          </div>
          <button type="button" className="btn ghost icon small" aria-label="Fechar" onClick={onClose}>
            <Icon name="x" />
          </button>
        </div>
        <div className="form-grid">
          <div className="field">
            <label htmlFor="nu-name">Nome de usuário</label>
            <input
              id="nu-name"
              className="input"
              value={name}
              onChange={(e) => setName(e.target.value.trim())}
              autoFocus
              autoComplete="off"
              spellCheck={false}
              placeholder="ex.: maria"
            />
            <span className={`hint${name && !valid ? " bad" : ""}`}>3 a 32 letras, números, ponto, hífen ou sublinhado.</span>
          </div>
          <div className="field">
            <PasswordField label="Senha" value={pw} onChange={setPw} autoComplete="new-password" meter minLength={8} />
            <button
              type="button"
              className="linkish small gen-pw"
              onClick={() => {
                const g = generatePassword();
                setPw(g);
                setShown(true);
                void navigator.clipboard?.writeText(g).then(() => toast("Senha gerada e copiada."));
              }}
            >
              Gerar uma senha forte
            </button>
            {shown && pw && (
              <code className="copy-line selectable" aria-label="Senha gerada">
                {pw}
              </code>
            )}
          </div>
        </div>
        <fieldset className="role-pick">
          <legend>Perfil</legend>
          {ROLES.map((r) => (
            <label key={r.role} className={`role-card${role === r.role ? " on" : ""}`}>
              <input type="radio" name="nu-role" checked={role === r.role} onChange={() => setRole(r.role)} />
              <Icon name={r.icon} />
              <strong>{roleLabel[r.role]}</strong>
              <span className="muted small">{r.text}</span>
            </label>
          ))}
        </fieldset>
        {(cams.data?.length ?? 0) > 1 && role !== "admin" && (
          <fieldset className="choice-list">
            <legend>Câmeras (nenhuma marcada = todas)</legend>
            {cams.data!.map((c) => (
              <label key={c.id} className={`choice${picked.includes(c.id) ? " on" : ""}`}>
                <input type="checkbox" checked={picked.includes(c.id)} onChange={() => setPicked((p) => (p.includes(c.id) ? p.filter((x) => x !== c.id) : [...p, c.id]))} />
                <span>{c.name}</span>
              </label>
            ))}
          </fieldset>
        )}
        <div className="dialog-actions">
          <button type="button" className="btn ghost" onClick={onClose}>
            Cancelar
          </button>
          <button className="btn primary" disabled={!valid || pw.length < 8 || create.isPending}>
            <Icon name="plus" /> Criar usuário
          </button>
        </div>
      </form>
    </div>
  );
}

function ResetPasswordDialog({ user, onClose, onSave }: { user: User; onClose: () => void; onSave: (pw: string) => Promise<unknown> }) {
  const toast = useToast();
  const [pw, setPw] = useState("");
  const [busy, setBusy] = useState(false);
  return (
    <div className="dialog-backdrop" role="dialog" aria-modal="true" aria-labelledby="reset-title" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <form
        className="dialog"
        onSubmit={(e) => {
          e.preventDefault();
          setBusy(true);
          onSave(pw)
            .then(() => {
              toast(`Senha de ${user.username} redefinida.`);
              onClose();
            })
            .catch(() => {})
            .finally(() => setBusy(false));
        }}
      >
        <h2 id="reset-title">Nova senha para {user.username}</h2>
        <p className="muted">Os aparelhos conectados com essa conta saem e precisam entrar de novo.</p>
        <PasswordField label="Nova senha" value={pw} onChange={setPw} autoComplete="new-password" meter minLength={8} autoFocus />
        <button type="button" className="linkish small" style={{ justifySelf: "start" }} onClick={() => setPw(generatePassword())}>
          Gerar uma senha forte
        </button>
        <div className="dialog-actions">
          <button type="button" className="btn ghost" onClick={onClose}>
            Cancelar
          </button>
          <button className="btn primary" disabled={busy || pw.length < 8}>
            Redefinir senha
          </button>
        </div>
      </form>
    </div>
  );
}

function DeleteUserDialog({ user, onClose, onDelete, busy }: { user: User; onClose: () => void; onDelete: () => void; busy: boolean }) {
  const [typed, setTyped] = useState("");
  return (
    <div className="dialog-backdrop" role="dialog" aria-modal="true" aria-labelledby="del-title" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <form
        className="dialog"
        onSubmit={(e) => {
          e.preventDefault();
          if (typed === user.username) onDelete();
        }}
      >
        <h2 id="del-title">Excluir {user.username}?</h2>
        <p className="muted">
          A conta é apagada e sai de todos os aparelhos. Os arquivos dela na Nuvem continuam no disco, na pasta <strong>{user.username}</strong>. Para só
          bloquear o acesso, use Desativar conta.
        </p>
        <div className="field">
          <label htmlFor="del-confirm">Digite {user.username} para confirmar</label>
          <input id="del-confirm" className="input" value={typed} onChange={(e) => setTyped(e.target.value)} autoFocus autoComplete="off" />
        </div>
        <div className="dialog-actions">
          <button type="button" className="btn ghost" onClick={onClose}>
            Cancelar
          </button>
          <button className="btn danger-fill" disabled={typed !== user.username || busy}>
            <Icon name="trash" /> Excluir usuário
          </button>
        </div>
      </form>
    </div>
  );
}

// Which cameras a non-admin user sees. Nothing ticked = every camera.
function CameraAccessDialog({ user, cameras, onClose, onSave }: { user: User; cameras: Camera[]; onClose: () => void; onSave: (ids: string[]) => void }) {
  const [picked, setPicked] = useState<string[]>(user.cameras);
  const toggle = (id: string) => setPicked((p) => (p.includes(id) ? p.filter((x) => x !== id) : [...p, id]));
  return (
    <div className="dialog-backdrop" role="dialog" aria-modal="true" aria-labelledby="cams-title" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
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
