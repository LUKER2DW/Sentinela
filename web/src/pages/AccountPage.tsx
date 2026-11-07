import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { api, get, type User } from "../lib/api";
import { useSession, useToast } from "../lib/session";
import { fmtDate } from "../lib/time";
import { PasswordField } from "../components/PasswordField";
import { policyError } from "../lib/password";
import { SecurityCard } from "../components/Security";
import { DevicesCard } from "../components/Devices";
import { DeviceNotifyCard, HouseModeCard, NotifyTypesCard, usePushState } from "../components/Notifications";
import { AppCard } from "../components/AppUpdate";
import { AppPairingCard } from "../components/AppPairing";
import { AppearanceCard } from "../components/Appearance";
import { CardHead } from "../components/CardHead";
import { CoastBanner } from "../components/Coast";
import { Icon } from "../components/Icon";
import { roleLabel } from "../components/UsersAdmin";
import { href, navigate } from "../lib/router";
import { useCompact } from "../lib/viewport";
import { isApp } from "../lib/push";
import { Avatar, removeAvatar, uploadAvatar } from "../components/Avatar";

const roleText = {
  admin: "Você gerencia tudo: usuários, câmeras e o sistema.",
  operator: "Você assiste, move a câmera, liga a luz, fala e envia arquivos.",
  viewer: "Você assiste ao vivo e às gravações e vê os eventos.",
} as const;

type Section = "security" | "devices" | "notify" | "look" | "app";
export const ACCOUNT_SECTIONS: { id: Section; label: string; icon: string; hint: string }[] = [
  { id: "security", label: "Senha e acesso", icon: "key", hint: "Senha e verificação em 2 etapas" },
  { id: "devices", label: "Aparelhos", icon: "monitor", hint: "Onde a conta está aberta" },
  { id: "notify", label: "Notificações", icon: "bell", hint: "O que avisa e o modo da casa" },
  { id: "look", label: "Aparência", icon: "sun", hint: "Tema claro, escuro ou automático" },
  { id: "app", label: "Aplicativo", icon: "app", hint: "Conectar e atualizar o app" },
];

/** Minha conta: the signed-in user's own settings, by category. */
export function AccountPage({ params }: { params: URLSearchParams }) {
  const { user, logout, can } = useSession();
  const compact = useCompact();
  const asked = params.get("s") as Section | null;
  const section: Section = ACCOUNT_SECTIONS.some((x) => x.id === asked) ? asked! : "security";
  const pick = (id: Section) => navigate("conta", { s: id });
  // Phone, opened from a "Mais" row: just that page, like a settings app.
  const sub = compact && !!asked;

  return (
    <>
      <header className="page-head">
        <div className="title">
          <h1>{sub ? ACCOUNT_SECTIONS.find((x) => x.id === section)?.label : "Minha conta"}</h1>
          {!sub && <span className="sub">Acesso, aparelhos e avisos desta conta. Nada aqui muda a conta das outras pessoas.</span>}
        </div>
      </header>
      <div className="account-layout">
        {!sub && (
          <section className="card account-hero">
            <CoastBanner className="account-coast" />
            <div className="account-id">
              <AvatarPicker name={user.username} role={user.role} />
              <div className="account-who">
                <h2>{user.username}</h2>
                <p className="muted small">
                  {roleLabel[user.role]}, conta desde {fmtDate(user.created_at)}
                </p>
                <p className="small">{roleText[user.role]}</p>
              </div>
              <div className="account-hero-actions">
                {can("admin") && !compact && (
                  <a className="btn small" href={href("system", { s: "users" })}>
                    <Icon name="users" /> Usuários
                  </a>
                )}
                <button className="btn small ghost danger" onClick={logout} aria-label="Sair" title="Sair">
                  <Icon name="logout" /> <span className="sair-label">Sair</span>
                </button>
              </div>
            </div>
            <AccountFacts onPick={pick} />
          </section>
        )}

        <div className="sys-layout account-sections">
          {!sub && (
            <nav className={compact ? "section-tabs" : "sys-nav"} role="tablist" aria-label="Categorias da conta">
              {ACCOUNT_SECTIONS.map((x) => (
                <button key={x.id} role="tab" aria-selected={section === x.id} onClick={() => pick(x.id)}>
                  {!compact && <Icon name={x.icon} />}
                  <span>{x.label}</span>
                </button>
              ))}
            </nav>
          )}
          <div className="sys-body" key={section} role="tabpanel" aria-label={ACCOUNT_SECTIONS.find((x) => x.id === section)?.label}>
            {section === "security" && (
              <div className="grid-2 set-grid">
                <PasswordCard />
                <SecurityCard />
              </div>
            )}
            {section === "devices" && <DevicesCard />}
            {section === "notify" && (
              <div className="grid-2 set-grid">
                <div className="stack">
                  <DeviceNotifyCard />
                  <HouseModeCard />
                </div>
                <NotifyTypesCard />
              </div>
            )}
            {section === "look" && (
              <div className="set-grid single">
                <AppearanceCard />
              </div>
            )}
            {section === "app" && (
              <div className="set-grid single">
                <AppPairingCard />
                <AppCard />
              </div>
            )}
          </div>
        </div>
      </div>
    </>
  );
}

/** The profile picture; tap to choose a new one (cropped here to a square). */
function AvatarPicker({ name, role }: { name: string; role: string }) {
  const toast = useToast();
  const [busy, setBusy] = useState(false);
  const pick = (file?: File) => {
    if (!file) return;
    setBusy(true);
    uploadAvatar(file)
      .then(() => toast("Foto de perfil atualizada."))
      .catch((e: Error) => toast(e.message, true))
      .finally(() => setBusy(false));
  };
  return (
    <div className="avatar-picker">
      <label className={`avatar-edit${busy ? " busy" : ""}`} title="Trocar a foto de perfil">
        <Avatar name={name} className={`big role-${role}`} />
        <span className="avatar-cam" aria-hidden="true">
          <Icon name="camera" />
        </span>
        <input type="file" accept="image/*" className="sr-only" aria-label="Escolher foto de perfil" disabled={busy} onChange={(e) => (pick(e.target.files?.[0]), (e.target.value = ""))} />
      </label>
      <button
        className="linkish small"
        disabled={busy}
        onClick={() =>
          removeAvatar()
            .then(() => toast("Foto removida."))
            .catch((e: Error) => toast(e.message, true))
        }
      >
        Remover foto
      </button>
    </div>
  );
}

/** Three facts under the name; each opens its category. */
function AccountFacts({ onPick }: { onPick: (s: Section) => void }) {
  const me = useQuery({ queryKey: ["me"], queryFn: () => get<{ user: User }>("/api/me") });
  const devices = useQuery({ queryKey: ["my-sessions"], queryFn: () => get<unknown[]>("/api/me/sessions") });
  const [push] = usePushState();
  const totp = me.data?.user.totp;
  const facts: { s: Section; icon: string; label: string; value: string; ok: boolean | null }[] = [
    { s: "security", icon: "shield", label: "Verificação em 2 etapas", value: totp === undefined ? "…" : totp ? "Ativa" : "Desligada", ok: totp ?? null },
    { s: "devices", icon: "monitor", label: "Aparelhos conectados", value: devices.data ? String(devices.data.length) : "…", ok: null },
    {
      s: "notify",
      icon: "bell",
      label: isApp() ? "Avisos no app" : "Avisos neste navegador",
      value: push === null ? "…" : push === "on" ? "Ligados" : push === "off" ? "Desligados" : "Indisponíveis",
      ok: push === null ? null : push === "on",
    },
  ];
  return (
    <div className="account-facts">
      {facts.map((f) => (
        <button key={f.s} className="account-fact" onClick={() => onPick(f.s)}>
          <Icon name={f.icon} />
          <span className="muted small">{f.label}</span>
          <strong>
            {f.ok !== null && <span className={`dot ${f.ok ? "ok" : "warn"}`} />}
            {f.value}
          </strong>
        </button>
      ))}
    </div>
  );
}

function PasswordCard() {
  const toast = useToast();
  const [cur, setCur] = useState("");
  const [next, setNext] = useState("");
  const [again, setAgain] = useState("");
  const change = useMutation({
    mutationFn: () => api("POST", "/api/me/password", { current: cur, new: next }),
    onSuccess: () => {
      toast("Senha alterada. Outros aparelhos foram desconectados.");
      setCur("");
      setNext("");
      setAgain("");
    },
    onError: (e: Error) => toast(e.message.includes("current password") ? "Senha atual incorreta." : policyError(next) || e.message, true),
  });
  return (
    <section className="card set-card" aria-label="Senha">
      <CardHead icon="key" title="Senha" sub="Trocar a senha desconecta os outros aparelhos desta conta." />
      <form
        className="form-stack"
        onSubmit={(e) => {
          e.preventDefault();
          if (next === again) change.mutate();
        }}
      >
        <PasswordField label="Senha atual" value={cur} onChange={setCur} autoComplete="current-password" />
        <PasswordField label="Nova senha" value={next} onChange={setNext} autoComplete="new-password" meter />
        <PasswordField label="Repita a nova senha" value={again} onChange={setAgain} autoComplete="new-password" matches={next} />
        <div className="card-foot row-gap">
          <button className="btn primary" disabled={change.isPending || !cur || policyError(next) !== "" || next !== again}>
            Alterar senha
          </button>
        </div>
      </form>
    </section>
  );
}
