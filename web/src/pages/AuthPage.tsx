import { useState } from "react";
import { api, type User } from "../lib/api";
import { LighthouseMark } from "../components/Lighthouse";
import { Panorama } from "../components/Panorama";
import { Anchor, Compass } from "../components/Decor";
import { PasswordField } from "../components/PasswordField";
import { ThemeToggle } from "../components/ThemeToggle";

interface Props {
  mode: "setup" | "login";
  onDone: (u: User) => void;
  downMessage?: string;
  onRetry?: () => void;
}

export function AuthPage({ mode, onDone, downMessage, onRetry }: Props) {
  const [username, setUsername] = useState(mode === "setup" ? "admin" : "");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const setup = mode === "setup";

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    if (setup && password !== confirm) {
      setError("As senhas não são iguais.");
      return;
    }
    setBusy(true);
    try {
      const r = await api<{ user: User }>("POST", setup ? "/api/setup" : "/api/login", { username, password });
      onDone(r.user);
    } catch (err) {
      const msg = (err as Error).message;
      setError(
        msg.includes("invalid username")
          ? "Usuário ou senha incorretos."
          : msg.includes("too many")
            ? "Muitas tentativas. Espere um minuto e tente de novo."
            : msg.includes("at least 8")
              ? "A senha precisa ter pelo menos 8 caracteres."
              : msg,
      );
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="auth">
      <div className="auth-art" aria-hidden="true">
        <Panorama />
      </div>
      <div className="auth-panel">
        <ThemeToggle className="auth-top" />
        <Compass className="auth-compass" />
        <Anchor className="auth-anchor" />
        <div className="auth-card">
          <div className="auth-head">
            <div className="auth-brand">
              <LighthouseMark />
              <h1>Sentinela</h1>
            </div>
            <p className="auth-lede">
              {downMessage
                ? "O servidor não respondeu. Confira se ele está ligado e tente de novo."
                : setup
                  ? "Primeiro acesso. Crie a conta de administrador: ela protege as câmeras, as gravações e os arquivos."
                  : "O farol da casa: câmeras ao vivo, gravações e a sua nuvem, num só lugar."}
            </p>
          </div>
          {downMessage ? (
            <button className="btn primary" onClick={onRetry}>
              Tentar de novo
            </button>
          ) : (
            <form onSubmit={submit} className="auth-form">
              <div className="field">
                <label htmlFor="u">Usuário</label>
                <input id="u" className="input" autoComplete="username" autoCapitalize="none" spellCheck={false} value={username} onChange={(e) => setUsername(e.target.value)} required autoFocus={!setup} />
              </div>
              <PasswordField
                label="Senha"
                value={password}
                onChange={setPassword}
                autoComplete={setup ? "new-password" : "current-password"}
                meter={setup}
                minLength={setup ? 8 : undefined}
                autoFocus={setup}
              />
              {setup && <PasswordField label="Repita a senha" value={confirm} onChange={setConfirm} autoComplete="new-password" matches={password} />}
              {error && (
                <p className="notice error" role="alert">
                  {error}
                </p>
              )}
              <button className="btn primary block" disabled={busy}>
                {busy ? <span className="spinner small" aria-hidden="true" /> : null}
                {setup ? "Criar administrador" : "Entrar"}
              </button>
            </form>
          )}
        </div>
      </div>
    </div>
  );
}
