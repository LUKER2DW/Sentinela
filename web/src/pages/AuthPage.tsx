import { useState } from "react";
import { api, type User } from "../lib/api";
import { Mark } from "../components/Icon";

export function AuthPage({ mode, onDone }: { mode: "setup" | "login"; onDone: (u: User) => void }) {
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
      <div className="auth-card">
        <div className="wordmark">
          <Mark />
          Sentinela
        </div>
        {setup ? (
          <p className="muted">Primeiro acesso. Crie a conta de administrador para proteger as câmeras.</p>
        ) : (
          <p className="muted">Entre para ver as câmeras.</p>
        )}
        <form onSubmit={submit} className="panel">
          <div className="field">
            <label htmlFor="u">Usuário</label>
            <input id="u" className="input" autoComplete="username" value={username} onChange={(e) => setUsername(e.target.value)} required autoFocus={!setup} />
          </div>
          <div className="field">
            <label htmlFor="p">Senha</label>
            <input id="p" className="input" type="password" autoComplete={setup ? "new-password" : "current-password"} value={password} onChange={(e) => setPassword(e.target.value)} required minLength={setup ? 10 : undefined} autoFocus={setup} />
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
      </div>
    </div>
  );
}
