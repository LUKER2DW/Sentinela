import { useState } from "react";
import { api, type User } from "../lib/api";
import { Gull, Lighthouse, Waves } from "../components/Doodles";
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
            : msg.includes("at least 10")
              ? "A senha precisa ter pelo menos 10 caracteres."
              : msg,
      );
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="auth">
      <div className="auth-art" aria-hidden="true">
        <Gull className="g1" />
        <Gull className="g2" />
        <Gull className="g3" />
        <Lighthouse />
        <Waves rows={3} />
      </div>
      <div className="auth-panel">
        <ThemeToggle className="auth-top" />
        <div className="auth-card">
          <div className="stack-sm">
            <h1>Sentinela</h1>
            <p className="muted">
              {downMessage
                ? "O servidor não respondeu. Confira se ele está ligado e tente de novo."
                : setup
                  ? "Primeiro acesso. Crie a conta de administrador para proteger as câmeras."
                  : "Entre para ver as câmeras de casa."}
            </p>
          </div>
          {downMessage ? (
            <button className="btn primary" onClick={onRetry}>
              Tentar de novo
            </button>
          ) : (
            <form onSubmit={submit}>
              <div className="field">
                <label htmlFor="u">Usuário</label>
                <input id="u" className="input" autoComplete="username" value={username} onChange={(e) => setUsername(e.target.value)} required autoFocus={!setup} />
              </div>
              <div className="field">
                <label htmlFor="p">Senha</label>
                <input
                  id="p"
                  className="input"
                  type="password"
                  autoComplete={setup ? "new-password" : "current-password"}
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  required
                  minLength={setup ? 10 : undefined}
                  autoFocus={setup}
                />
              </div>
              {setup && (
                <div className="field">
                  <label htmlFor="c">Repita a senha</label>
                  <input id="c" className="input" type="password" autoComplete="new-password" value={confirm} onChange={(e) => setConfirm(e.target.value)} required />
                </div>
              )}
              {error && (
                <p className="notice error" role="alert">
                  {error}
                </p>
              )}
              <button className="btn primary" disabled={busy}>
                {setup ? "Criar administrador" : "Entrar"}
              </button>
            </form>
          )}
        </div>
      </div>
    </div>
  );
}
