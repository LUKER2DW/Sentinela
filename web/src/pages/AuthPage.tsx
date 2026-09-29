import { useState } from "react";
import { ApiError, api, type User } from "../lib/api";
import { canChangeConnection, changeConnection, zoneLabel } from "../lib/access";
import { LighthouseMark } from "../components/Lighthouse";
import { Panorama } from "../components/Panorama";
import { Anchor, Compass } from "../components/Decor";
import { PasswordField } from "../components/PasswordField";
import { ThemeToggle } from "../components/ThemeToggle";

interface Props {
  mode: "setup" | "login";
  /** Public path (Funnel/domain): ask for the two-step code (ADR-0019). */
  mfa?: boolean;
  onDone: (u: User) => void;
  downMessage?: string;
  onRetry?: () => void;
}

export function AuthPage({ mode, mfa = false, onDone, downMessage, onRetry }: Props) {
  const [username, setUsername] = useState(mode === "setup" ? "admin" : "");
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [askCode, setAskCode] = useState(mfa);
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const setup = mode === "setup";
  // Opened through the tailnet name (away from home), or offline altogether.
  const remote = location.hostname.endsWith(".ts.net") || !navigator.onLine;
  const android = /Android/i.test(navigator.userAgent);
  const inApp = canChangeConnection();

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    if (setup && password !== confirm) {
      setError("As senhas não são iguais.");
      return;
    }
    setBusy(true);
    try {
      const body = setup ? { username, password } : askCode ? { username, password, code: code.replace(/\s/g, "") } : { username, password };
      const r = await api<{ user: User }>("POST", setup ? "/api/setup" : "/api/login", body);
      onDone(r.user);
    } catch (err) {
      const msg = (err as Error).message;
      if (err instanceof ApiError && err.mfa) {
        setAskCode(true);
        setError(askCode ? "Digite o código de 6 dígitos do app autenticador." : "");
        return;
      }
      setError(
        msg.includes("invalid username")
          ? "Usuário ou senha incorretos."
          : msg.includes("invalid code")
            ? "Código incorreto ou já usado. Espere o próximo código e tente de novo."
            : msg.includes("not set up")
              ? "Esta conta ainda não tem a verificação em 2 etapas. Ative-a em casa: Mais → Segurança."
              : msg.includes("for this account")
                ? "Muitas tentativas nesta conta. Espere uma hora."
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
                ? remote
                  ? "Sem conexão com a casa. Fora de casa, o Sentinela só abre com o Tailscale ligado neste aparelho."
                  : "O servidor não respondeu. Confira se ele está ligado e tente de novo."
                : setup
                  ? "Primeiro acesso. Crie a conta de administrador: ela protege as câmeras, as gravações e os arquivos."
                  : "O farol da casa: câmeras ao vivo, gravações e a sua nuvem, num só lugar."}
            </p>
          </div>
          {downMessage ? (
            <div className="stack-sm">
              {remote && android && (
                // Opens the Tailscale app (or its Play Store page if missing).
                <a className="btn primary block" href="intent://#Intent;package=com.tailscale.ipn;end">
                  Abrir o Tailscale
                </a>
              )}
              {remote && !android && <p className="muted small">Abra o app Tailscale, ligue a conexão e volte aqui.</p>}
              <button className={`btn block${remote && android ? "" : " primary"}`} onClick={onRetry}>
                Tentar de novo
              </button>
              {inApp && (
                <button className="btn block" onClick={changeConnection}>
                  Trocar forma de conexão
                </button>
              )}
            </div>
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
              {!setup && askCode && (
                <div className="field">
                  <label htmlFor="otp">Código de verificação</label>
                  <input
                    id="otp"
                    className="input otp"
                    inputMode="numeric"
                    autoComplete="one-time-code"
                    pattern="[0-9 ]{6,7}"
                    maxLength={7}
                    placeholder="000 000"
                    value={code}
                    onChange={(e) => setCode(e.target.value)}
                    required
                  />
                  <p className="muted small">Fora de casa: abra o app autenticador e digite o código do Sentinela.</p>
                </div>
              )}
              {error && (
                <p className="notice error" role="alert">
                  {error}
                </p>
              )}
              <button className="btn primary block" disabled={busy}>
                {busy ? <span className="spinner small" aria-hidden="true" /> : null}
                {setup ? "Criar administrador" : "Entrar"}
              </button>
              {!setup && (
                <p className="muted small auth-via">
                  Conectado por: {zoneLabel()}
                  {inApp && (
                    <>
                      {" · "}
                      <button type="button" className="linkish" onClick={changeConnection}>
                        trocar
                      </button>
                    </>
                  )}
                </p>
              )}
            </form>
          )}
        </div>
      </div>
    </div>
  );
}
