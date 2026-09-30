import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { api } from "../lib/api";
import { useSession, useToast } from "../lib/session";
import { fmtDate } from "../lib/time";
import { PasswordField } from "../components/PasswordField";
import { policyError } from "../lib/password";
import { SecurityCard } from "../components/Security";
import { DevicesCard } from "../components/Devices";
import { NotificationsCard } from "../components/Notifications";
import { OceanScene } from "../components/Doodles";
import { Icon } from "../components/Icon";
import { roleLabel } from "../components/UsersAdmin";
import { href } from "../lib/router";

const roleText = {
  admin: "Você gerencia tudo: usuários, câmeras e o sistema.",
  operator: "Você assiste, move a câmera, liga a luz, fala e envia arquivos.",
  viewer: "Você assiste ao vivo e às gravações e lê a Nuvem.",
} as const;

/** Minha conta: the signed-in user's own security, apart from the System page. */
export function AccountPage() {
  const { user, logout, can } = useSession();
  return (
    <>
      <header className="page-head">
        <div className="title">
          <h1>Minha conta</h1>
          <span className="sub">Senha, verificação em 2 etapas, aparelhos e avisos desta conta.</span>
        </div>
      </header>
      <div className="account-layout">
        <section className="card account-hero">
          <OceanScene className="account-ocean" />
          <div className="account-id">
            <span className={`avatar big role-${user.role}`} aria-hidden="true">
              {user.username.slice(0, 1).toUpperCase()}
            </span>
            <div>
              <h2>{user.username}</h2>
              <p className="muted small">
                {roleLabel[user.role]} · conta desde {fmtDate(user.created_at)}
              </p>
              <p className="small">{roleText[user.role]}</p>
            </div>
            <div className="account-hero-actions">
              {can("admin") && (
                <a className="btn small" href={href("system", { s: "users" })}>
                  <Icon name="users" /> Usuários
                </a>
              )}
              <button className="btn small ghost danger" onClick={logout}>
                <Icon name="logout" /> Sair
              </button>
            </div>
          </div>
        </section>
        <div className="grid-2 account-grid">
          <PasswordCard />
          <SecurityCard />
          <DevicesCard />
          <NotificationsCard />
        </div>
      </div>
    </>
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
    <section className="card" aria-label="Senha">
      <h2>Senha</h2>
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
