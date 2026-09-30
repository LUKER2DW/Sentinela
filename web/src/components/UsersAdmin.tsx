import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, get, type AuditEntry, type Camera, type Role, type User } from "../lib/api";
import { useSession, useToast } from "../lib/session";
import { fmtBytes, fmtDate, relative } from "../lib/time";
import { useCameras } from "../pages/LivePage";
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
  "user.invite": "Novo convite",
  "user.email": "Mudou e-mail",
  "invite.accept": "Primeiro acesso",
  "user.disabled": "Ativou/desativou conta",
  "user.cameras": "Mudou câmeras",
  "notify.mode": "Modo de avisos",
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
    () =>
      (users.data ?? []).filter(
        (u) => !filter || u.username.toLowerCase().includes(filter.toLowerCase()) || (u.email ?? "").toLowerCase().includes(filter.toLowerCase()),
      ),
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
            <Icon name="plus" /> Autorizar pessoa
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
                    {u.pending ? " · aguardando 1º acesso" : ""}
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
  const [dialog, setDialog] = useState<"invite" | "cameras" | "delete" | "email" | null>(null);
  const [code, setCode] = useState<string | null>(null);
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ["users"] });
    void qc.invalidateQueries({ queryKey: ["user", id] });
  };
  const update = useMutation({
    mutationFn: (body: object) => api("PATCH", `/api/users/${id}`, body),
    onSuccess: refresh,
    onError: (e: Error) => toast(translate(e.message), true),
  });
  const reinvite = useMutation({
    mutationFn: () => api<InvitedUser>("POST", `/api/users/${id}/invite`),
    onSuccess: (r) => {
      refresh();
      setDialog(null);
      setCode(r.invite_code ?? null);
    },
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
          <span className="small">
            E-mail: {u.email || <span className="muted">nenhum</span>}{" "}
            <button className="linkish" onClick={() => setDialog("email")}>
              {u.email ? "mudar" : "adicionar"}
            </button>
          </span>
        </div>
        <span className={`pill${u.disabled || u.pending ? "" : " ok"}`}>
          <span className={`dot ${u.disabled ? "bad" : u.pending ? "warn" : "ok"}`} /> {u.disabled ? "Desativado" : u.pending ? "Aguardando 1º acesso" : "Ativo"}
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
        {!self && (
          <button className="btn" onClick={() => setDialog("invite")}>
            <Icon name="lock" /> {u.pending ? "Gerar novo convite" : "Esqueceu a senha: novo convite"}
          </button>
        )}
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

      {dialog === "invite" && (
        <div className="dialog-backdrop" role="dialog" aria-modal="true" aria-labelledby="reinv-title" onMouseDown={(e) => e.target === e.currentTarget && setDialog(null)}>
          <div className="dialog">
            <h2 id="reinv-title">Novo convite para {u.username}</h2>
            <p className="muted">
              {u.pending
                ? "O código anterior deixa de valer."
                : "A senha atual e a verificação em 2 etapas deixam de valer e a conta sai de todos os aparelhos. A pessoa cria uma senha nova com o código."}
            </p>
            <div className="dialog-actions">
              <button type="button" className="btn ghost" onClick={() => setDialog(null)}>
                Cancelar
              </button>
              <button className="btn primary" disabled={reinvite.isPending} onClick={() => reinvite.mutate()}>
                Gerar convite
              </button>
            </div>
          </div>
        </div>
      )}
      {dialog === "email" && (
        <EmailDialog
          user={u}
          onClose={() => setDialog(null)}
          onSave={(email) => update.mutate({ email }, { onSuccess: () => (toast("E-mail salvo."), setDialog(null)) })}
        />
      )}
      {code && <InviteCodeDialog user={u} code={code} onClose={() => setCode(null)} />}
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
  if (msg.includes("already exists")) return "Já existe uma conta com esse nome ou e-mail.";
  if (msg.includes("already used")) return "Esse e-mail já é de outra conta.";
  if (msg.includes("invalid e-mail")) return "E-mail inválido.";
  if (msg.includes("invalid username")) return "Nome inválido: use 3 a 32 letras, números, ponto, hífen ou sublinhado.";
  return msg;
}

interface InvitedUser extends User {
  invite_code?: string;
}

/** Shows an invite code once, with ways to pass it on. */
function InviteCodeDialog({ user, code, onClose }: { user: User; code: string; onClose: () => void }) {
  const toast = useToast();
  const login = user.email || user.username;
  const text =
    `Você foi autorizado no Sentinela. No primeiro acesso, toque em "Primeiro acesso? Tenho um código de convite" e use:\n` +
    `Usuário ou e-mail: ${login}\nCódigo: ${code}\nO código vale por 7 dias.`;
  const share = async () => {
    try {
      if (navigator.share) await navigator.share({ title: "Convite do Sentinela", text });
      else {
        await navigator.clipboard.writeText(text);
        toast("Convite copiado.");
      }
    } catch {
      // share sheet closed
    }
  };
  return (
    <div className="dialog-backdrop" role="dialog" aria-modal="true" aria-labelledby="code-title">
      <div className="dialog">
        <h2 id="code-title">Convite de {user.username}</h2>
        <p className="muted">Passe este código para a pessoa. Ela entra com o usuário ou e-mail abaixo, usa o código e cria a própria senha. Ele vale por 7 dias e só aparece agora.</p>
        <dl className="kv">
          <dt>Usuário</dt>
          <dd className="selectable">{user.username}</dd>
          {user.email && (
            <>
              <dt>E-mail</dt>
              <dd className="selectable">{user.email}</dd>
            </>
          )}
        </dl>
        <code className="invite-code selectable" aria-label="Código de convite">
          {code}
        </code>
        <div className="dialog-actions">
          <button type="button" className="btn" onClick={() => void navigator.clipboard?.writeText(code).then(() => toast("Código copiado."))}>
            Copiar código
          </button>
          <button type="button" className="btn" onClick={() => void share()}>
            Enviar convite
          </button>
          <button type="button" className="btn primary" onClick={onClose}>
            Pronto
          </button>
        </div>
      </div>
    </div>
  );
}

function EmailDialog({ user, onClose, onSave }: { user: User; onClose: () => void; onSave: (email: string) => void }) {
  const [email, setEmail] = useState(user.email ?? "");
  return (
    <div className="dialog-backdrop" role="dialog" aria-modal="true" aria-labelledby="email-title" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <form
        className="dialog"
        onSubmit={(e) => {
          e.preventDefault();
          onSave(email.trim());
        }}
      >
        <h2 id="email-title">E-mail de {user.username}</h2>
        <p className="muted">A pessoa pode entrar com o usuário ou com este e-mail. Deixe vazio para não usar e-mail.</p>
        <div className="field">
          <label htmlFor="em">E-mail</label>
          <input id="em" className="input" type="email" autoComplete="off" value={email} onChange={(e) => setEmail(e.target.value)} autoFocus />
        </div>
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

const EMAIL = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

function CreateUserDialog({ onClose, onCreated }: { onClose: () => void; onCreated: (id: number) => void }) {
  const toast = useToast();
  const qc = useQueryClient();
  const cams = useCameras();
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [role, setRole] = useState<Role>("viewer");
  const [picked, setPicked] = useState<string[]>([]);
  const [made, setMade] = useState<InvitedUser | null>(null);
  const nameOk = name === "" || /^[A-Za-z0-9._-]{3,32}$/.test(name);
  const emailOk = email === "" || EMAIL.test(email);
  const valid = nameOk && emailOk && (name !== "" || email !== "");
  const create = useMutation({
    mutationFn: () => api<InvitedUser>("POST", "/api/users", { username: name, email, role, cameras: role === "admin" ? [] : picked }),
    onSuccess: (u) => {
      void qc.invalidateQueries({ queryKey: ["users"] });
      onCreated(u.id);
      setMade(u);
    },
    onError: (e: Error) => toast(translate(e.message), true),
  });
  if (made?.invite_code) return <InviteCodeDialog user={made} code={made.invite_code} onClose={onClose} />;
  return (
    <div className="dialog-backdrop" role="dialog" aria-modal="true" aria-labelledby="new-user" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <form
        className="dialog wide create-user"
        onSubmit={(e) => {
          e.preventDefault();
          if (valid) create.mutate();
        }}
      >
        <div className="dialog-head">
          <div>
            <h2 id="new-user">Autorizar pessoa</h2>
            <p className="muted small">
              Você só autoriza o usuário ou o e-mail. O sistema gera um código de convite; no primeiro acesso (em casa ou de fora) a pessoa usa o código e
              cria a própria senha.
            </p>
          </div>
          <button type="button" className="btn ghost icon small" aria-label="Fechar" onClick={onClose}>
            <Icon name="x" />
          </button>
        </div>
        <div className="form-grid">
          <div className="field">
            <label htmlFor="nu-email">E-mail</label>
            <input
              id="nu-email"
              className="input"
              type="email"
              value={email}
              onChange={(e) => setEmail(e.target.value.trim())}
              autoFocus
              autoComplete="off"
              spellCheck={false}
              placeholder="ex.: maria@gmail.com"
            />
            <span className={`hint${emailOk ? "" : " bad"}`}>Opcional. A pessoa poderá entrar com ele.</span>
          </div>
          <div className="field">
            <label htmlFor="nu-name">Nome de usuário</label>
            <input
              id="nu-name"
              className="input"
              value={name}
              onChange={(e) => setName(e.target.value.trim())}
              autoComplete="off"
              spellCheck={false}
              placeholder={email ? `vazio = ${email.split("@")[0]}` : "ex.: maria"}
            />
            <span className={`hint${nameOk ? "" : " bad"}`}>3 a 32 letras, números, ponto, hífen ou sublinhado. Também é o nome da pasta na Nuvem.</span>
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
          <button className="btn primary" disabled={!valid || create.isPending}>
            <Icon name="plus" /> Autorizar e gerar convite
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
