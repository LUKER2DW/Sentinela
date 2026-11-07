import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, get, type AuditEntry, type Camera, type Role, type User } from "../lib/api";
import { useSession, useToast } from "../lib/session";
import { fmtBytes, fmtDate, relative } from "../lib/time";
import { useCameras } from "../pages/LivePage";
import { deviceName } from "./Devices";
import { Icon } from "./Icon";

import { t, translated } from "../lib/i18n";
export const roleLabel: Record<Role, string> = translated({ admin: "Administrator", operator: "Operator", viewer: "Viewer" });

const ROLES: { role: Role; icon: string; text: string }[] = [
  { role: "viewer", icon: "eye", text: t("Watches live and recordings and sees events. No access to Cloud.") },
  { role: "operator", icon: "live", text: t("Also moves the camera, turns on the light, talks and uses Cloud: its own folder and Shared.") },
  { role: "admin", icon: "shield", text: t("Everything, including users, cameras and system settings.") },
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
  cloud?: { folder: string; files: Usage; trash: Usage };
}

const actionLabel: Record<string, string> = {
  login: t("Signed in"),
  setup: t("Initial setup"),
  "user.create": t("Created user"),
  "user.delete": t("Removed user"),
  "user.role": t("Changed role"),
  "user.password": t("Changed password"),
  "user.invite": t("New invite"),
  "user.email": t("Changed email"),
  "invite.accept": t("First sign-in"),
  "user.disabled": t("Turned account on/off"),
  "user.cameras": t("Changed cameras"),
  "notify.mode": t("Notice mode"),
  "camera.white_light": t("White light"),
  "camera.day_night": t("Day and night mode"),
  "camera.preset.set": t("Saved position"),
  "camera.preset.clear": t("Deleted position"),
  "recording.export": t("Exported video"),
  "recording.timelapse": "Timelapse",
  "segment.protect": t("Clip protection"),
  "nas.upload": t("Uploaded file"),
  "nas.mkdir": t("Created folder"),
  "nas.move": t("Moved/renamed"),
  "nas.delete": t("Deleted file"),
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
          <h2>{t("Users")}</h2>
          <span className="muted small">{users.data?.length ?? 0}</span>
        </div>
        <div className="users-tools">
          <label className="search">
            <Icon name="search" />
            <input className="input" placeholder={t("Search")} aria-label={t("Search user")} value={filter} onChange={(e) => setFilter(e.target.value)} />
          </label>
          <button className="btn primary" onClick={() => setCreating(true)}>
            <Icon name="plus" />{" "}{t("Authorize person")}
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
                    {u.pending ? t(" · waiting for first sign-in") : ""}
                    {u.disabled ? t(" · disabled") : ""}
                  </span>
                </span>
                {u.totp && (
                  <span className="pill ok small-pill" title={t("Two-step verification active")}>
                    {t("2 steps")}
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
  const [invitedNow, setInvitedNow] = useState<InvitedUser | null>(null);
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
      setInvitedNow(r.invite_code ? r : null);
    },
    onError: (e: Error) => toast(translate(e.message), true),
  });
  const remove = useMutation({
    mutationFn: () => api("DELETE", `/api/users/${id}`),
    onSuccess: () => {
      toast(t("User removed."));
      void qc.invalidateQueries({ queryKey: ["users"] });
      onGone();
    },
    onError: (e: Error) => toast(translate(e.message), true),
  });

  const d = q.data;
  if (!d) return <section className="card user-panel">{q.isError ? <p className="notice error">{t("Could not read the user.")}</p> : <p className="muted">{t("Loading…")}</p>}</section>;
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
            {u.username} {self && <span className="pill small-pill">{t("you")}</span>}
          </h2>
          <span className="muted small">
            {roleLabel[u.role]}{" "}{t("· account created on")}{" "}{fmtDate(u.created_at)} · {d.last_login ? t("last sign-in {v0}", { v0: relative(d.last_login) }) : t("never signed in")}
          </span>
          <span className="small">
            {t("Email:")}{" "}{u.email || <span className="muted">{t("no email")}</span>}{" "}
            <button className="linkish" onClick={() => setDialog("email")}>
              {u.email ? t("change") : t("add")}
            </button>
          </span>
        </div>
        <span className={`pill${u.disabled || u.pending ? "" : " ok"}`}>
          <span className={`dot ${u.disabled ? "bad" : u.pending ? "warn" : "ok"}`} /> {u.disabled ? t("Disabled") : u.pending ? t("Waiting for first sign-in") : t("Active")}
        </span>
      </header>

      <div className="user-grid">
        <div className="user-block">
          <h3>{t("Profile")}</h3>
          <div className="role-pick compact">
            {ROLES.map((r) => (
              <button
                key={r.role}
                className={`role-card${u.role === r.role ? " on" : ""}`}
                aria-pressed={u.role === r.role}
                disabled={update.isPending || (self && r.role !== "admin")}
                onClick={() => u.role !== r.role && update.mutate({ role: r.role }, { onSuccess: () => toast(t("{username} is now {v1}.", { username: u.username, v1: roleLabel[r.role].toLowerCase() })) })}
              >
                <Icon name={r.icon} />
                <strong>{roleLabel[r.role]}</strong>
              </button>
            ))}
          </div>
          <p className="muted small">{ROLES.find((r) => r.role === u.role)?.text}</p>
          {multiCam && u.role !== "admin" && (
            <p className="small">
              {t("Cameras:")}{" "}{u.cameras.length === 0 ? t("all") : u.cameras.map((c) => cams.data?.find((x) => x.id === c)?.name ?? c).join(", ")}{" "}
              <button className="linkish" onClick={() => setDialog("cameras")}>
                {t("change")}
              </button>
            </p>
          )}
        </div>

        <div className="user-block">
          <h3>{t("Cloud")}</h3>
          {d.cloud ? (
            <dl className="kv">
              <dt>{t("Folder")}</dt>
              <dd>{d.cloud.folder}</dd>
              <dt>{t("Files")}</dt>
              <dd>
                {t(d.cloud.files.folders === 1 ? "{files} in {folders} folder" : "{files} in {folders} folders", { files: d.cloud.files.files, folders: d.cloud.files.folders })}
              </dd>
              <dt>{t("Uses")}</dt>
              <dd>
                {fmtBytes(d.cloud.files.bytes)}
                {d.cloud.files.partial ? "+" : ""}
              </dd>
              <dt>{t("In trash")}</dt>
              <dd>{fmtBytes(d.cloud.trash.bytes)}</dd>
            </dl>
          ) : (
            <p className="muted small">{t("Cloud is not mounted on this server.")}</p>
          )}
        </div>

        <div className="user-block">
          <h3>{t("Security")}</h3>
          <dl className="kv">
            <dt>{t("2 steps")}</dt>
            <dd>{u.totp ? t("on") : t("turned off")}</dd>
            <dt>{t("Devices")}</dt>
            <dd>{d.sessions.length}</dd>
          </dl>
          <ul className="mini-list">
            {d.sessions.slice(0, 5).map((s) => (
              <li key={s.id}>
                <span>{deviceName(s.user_agent)}</span>
                <span className="muted small">
                  {s.mfa ? t("from outside · ") : ""}{t("since {date}", { date: fmtDate(s.created_at) })}
                </span>
              </li>
            ))}
          </ul>
        </div>

        <div className="user-block span-2">
          <h3>{t("Recent actions")}</h3>
          {d.audit.length === 0 ? (
            <p className="muted small">{t("Nothing recorded yet.")}</p>
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
            <Icon name="lock" /> {u.pending ? t("Create new invite") : t("Forgot the password: new invite")}
          </button>
        )}
        {u.totp && (
          <button
            className="btn"
            onClick={() =>
              window.confirm(t("Turn off two-step verification for {username}? Outside access for this account is ended.", { username: u.username })) &&
              update.mutate({ totp_off: true }, { onSuccess: () => toast(t("Two-step verification turned off.")) })
            }
          >
            <Icon name="shield" />{" "}{t("Turn off 2 steps")}
          </button>
        )}
        <span className="grow" />
        {!self && (
          <>
            <button
              className="btn"
              onClick={() =>
                (u.disabled || window.confirm(t("Disable {username}? The account is signed out of every device and cannot sign in until it is re-enabled.", { username: u.username }))) &&
                update.mutate({ disabled: !u.disabled }, { onSuccess: () => toast(u.disabled ? t("Account re-enabled.") : t("Account disabled.")) })
              }
            >
              <Icon name={u.disabled ? "check" : "x"} /> {u.disabled ? t("Re-enable account") : t("Disable account")}
            </button>
            <button className="btn danger" onClick={() => setDialog("delete")}>
              <Icon name="trash" />{" "}{t("Remove")}
            </button>
          </>
        )}
      </footer>

      {dialog === "invite" && (
        <div className="dialog-backdrop" role="dialog" aria-modal="true" aria-labelledby="reinv-title" onMouseDown={(e) => e.target === e.currentTarget && setDialog(null)}>
          <div className="dialog">
            <h2 id="reinv-title">{t("New invite for")}{" "}{u.username}</h2>
            <p className="muted">
              {u.pending
                ? t("The previous code stops working.")
                : t("The current password and two-step check stop working and the account is signed out of every device. The person creates a new password with the code.")}
            </p>
            <div className="dialog-actions">
              <button type="button" className="btn ghost" onClick={() => setDialog(null)}>
                {t("Cancel")}
              </button>
              <button className="btn primary" disabled={reinvite.isPending} onClick={() => reinvite.mutate()}>
                {t("Create invite")}
              </button>
            </div>
          </div>
        </div>
      )}
      {dialog === "email" && (
        <EmailDialog
          user={u}
          onClose={() => setDialog(null)}
          onSave={(email) => update.mutate({ email }, { onSuccess: () => (toast(t("Email saved.")), setDialog(null)) })}
        />
      )}
      {invitedNow && <InviteCodeDialog user={invitedNow} onClose={() => setInvitedNow(null)} />}
      {dialog === "cameras" && cams.data && (
        <CameraAccessDialog
          user={u}
          cameras={cams.data}
          onClose={() => setDialog(null)}
          onSave={(ids) => update.mutate({ cameras: ids }, { onSuccess: () => (toast(t("{username}'s cameras updated", { username: u.username })), setDialog(null)) })}
        />
      )}
      {dialog === "delete" && <DeleteUserDialog user={u} onClose={() => setDialog(null)} onDelete={() => remove.mutate()} busy={remove.isPending} />}
    </section>
  );
}

function translate(msg: string): string {
  if (msg.includes("last admin")) return t("At least one active administrator must remain.");
  if (msg.includes("yourself")) return t("You cannot do that with your own account.");
  if (msg.includes("already exists")) return t("An account with this name or email already exists.");
  if (msg.includes("already used")) return t("This email already belongs to another account.");
  if (msg.includes("invalid e-mail")) return t("Invalid email.");
  if (msg.includes("invalid username")) return t("Invalid name: use 3 to 32 letters, numbers, dot, hyphen or underscore.");
  return msg;
}

interface InvitedUser extends User {
  invite_code?: string;
  /** First-access link on the public address (server config remote.public.url). */
  invite_url?: string;
}

/** The invite link: the server's public address, else this page's own. */
function inviteLink(u: InvitedUser): string {
  if (u.invite_url) return u.invite_url;
  const q = new URLSearchParams({ c: u.invite_code ?? "", u: u.email || u.username });
  return `${window.location.origin}/#/invite?${q.toString()}`;
}

/** Shows an invite code once, with ways to pass it on. */
function InviteCodeDialog({ user, onClose }: { user: InvitedUser; onClose: () => void }) {
  const toast = useToast();
  const link = inviteLink(user);
  const text = t("You were invited to Sentinela. Open this link to create your password (valid for 7 days):\n{link}", { link });
  const share = async () => {
    try {
      if (navigator.share) await navigator.share({ title: t("Sentinela invite"), text });
      else {
        await navigator.clipboard.writeText(text);
        toast(t("Invite copied."));
      }
    } catch {
      // share sheet closed
    }
  };
  return (
    <div className="dialog-backdrop" role="dialog" aria-modal="true" aria-labelledby="code-title">
      <div className="dialog">
        <h2 id="code-title">{t("Invite from")}{" "}{user.username}</h2>
        <p className="muted">
          {t("Send this link to the person. When they open it, they create their own password and sign in. It is valid for 7 days, works once and is shown only now.")}
        </p>
        <dl className="kv">
          <dt>{t("User")}</dt>
          <dd className="selectable">{user.username}</dd>
          {user.email && (
            <>
              <dt>{t("Email")}</dt>
              <dd className="selectable">{user.email}</dd>
            </>
          )}
        </dl>
        <code className="invite-link selectable" aria-label={t("Invite link")}>
          {link}
        </code>
        {!user.invite_url && (
          <p className="muted small">
            {t("The link uses this page's address. To work from outside the home, set up")}{" "}<code>remote.public.url</code>{" "}{t("on the server.")}
          </p>
        )}
        <div className="dialog-actions">
          <button type="button" className="btn" onClick={() => void navigator.clipboard?.writeText(link).then(() => toast(t("Link copied.")))}>
            {t("Copy link")}
          </button>
          <button type="button" className="btn" onClick={() => void share()}>
            {t("Send invite")}
          </button>
          <button type="button" className="btn primary" onClick={onClose}>
            {t("Done")}
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
        <h2 id="email-title">{t("Email from")}{" "}{user.username}</h2>
        <p className="muted">{t("The person can sign in with the username or this email. Leave empty to not use an email.")}</p>
        <div className="field">
          <label htmlFor="em">{t("Email")}</label>
          <input id="em" className="input" type="email" autoComplete="off" value={email} onChange={(e) => setEmail(e.target.value)} autoFocus />
        </div>
        <div className="dialog-actions">
          <button type="button" className="btn ghost" onClick={onClose}>
            {t("Cancel")}
          </button>
          <button className="btn primary">{t("Save")}</button>
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
  if (made?.invite_code) return <InviteCodeDialog user={made} onClose={onClose} />;
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
            <h2 id="new-user">{t("Authorize person")}</h2>
            <p className="muted small">
              {t("You only authorize the username or the email. The system creates an invite link; when the person opens it (at home or outside), they create their own password.")}
            </p>
          </div>
          <button type="button" className="btn ghost icon small" aria-label={t("Close")} onClick={onClose}>
            <Icon name="x" />
          </button>
        </div>
        <div className="form-grid">
          <div className="field">
            <label htmlFor="nu-email">{t("Email")}</label>
            <input
              id="nu-email"
              className="input"
              type="email"
              value={email}
              onChange={(e) => setEmail(e.target.value.trim())}
              autoFocus
              autoComplete="off"
              spellCheck={false}
              placeholder={t("e.g. maria@gmail.com")}
            />
            <span className={`hint${emailOk ? "" : " bad"}`}>{t("Optional. The person can sign in with it.")}</span>
          </div>
          <div className="field">
            <label htmlFor="nu-name">{t("Username")}</label>
            <input
              id="nu-name"
              className="input"
              value={name}
              onChange={(e) => setName(e.target.value.trim())}
              autoComplete="off"
              spellCheck={false}
              placeholder={email ? t("empty = {v0}", { v0: email.split("@")[0] }) : t("e.g. maria")}
            />
            <span className={`hint${nameOk ? "" : " bad"}`}>{t("3 to 32 letters, numbers, dot, hyphen or underscore. It is also the folder name in Cloud.")}</span>
          </div>
        </div>
        <fieldset className="role-pick">
          <legend>{t("Profile")}</legend>
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
            <legend>{t("Cameras (none ticked = all)")}</legend>
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
            {t("Cancel")}
          </button>
          <button className="btn primary" disabled={!valid || create.isPending}>
            <Icon name="plus" />{" "}{t("Authorize and create invite")}
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
        <h2 id="del-title">{t("Remove")}{" "}{user.username}?</h2>
        <p className="muted">
          {t("The account is deleted and signed out of every device. Its files in Cloud stay on disk, in the folder")}{" "}<strong>{user.username}</strong>{t(". To just block access, use Disable account.")}
        </p>
        <div className="field">
          <label htmlFor="del-confirm">{t("Enter")}{" "}{user.username}{" "}{t("to confirm")}</label>
          <input id="del-confirm" className="input" value={typed} onChange={(e) => setTyped(e.target.value)} autoFocus autoComplete="off" />
        </div>
        <div className="dialog-actions">
          <button type="button" className="btn ghost" onClick={onClose}>
            {t("Cancel")}
          </button>
          <button className="btn danger-fill" disabled={typed !== user.username || busy}>
            <Icon name="trash" />{" "}{t("Delete user")}
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
        <h2 id="cams-title">{t("Cameras of")}{" "}{user.username}</h2>
        <p className="muted">{t("Live, recordings, events and notices only from the ticked cameras. None ticked = all.")}</p>
        {cameras.map((c) => (
          <label key={c.id} className="check-row">
            <input type="checkbox" checked={picked.includes(c.id)} onChange={() => toggle(c.id)} /> {c.name}
          </label>
        ))}
        <div className="dialog-actions">
          <button type="button" className="btn ghost" onClick={onClose}>
            {t("Cancel")}
          </button>
          <button className="btn primary">{t("Save")}</button>
        </div>
      </form>
    </div>
  );
}
