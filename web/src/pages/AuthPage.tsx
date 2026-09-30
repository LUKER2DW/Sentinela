import { useState } from "react";
import { ApiError, api, type User } from "../lib/api";
import { canChangeConnection, changeConnection, isPublicZone, zoneLabel } from "../lib/access";
import { policyError } from "../lib/password";
import { LighthouseMark } from "../components/Lighthouse";
import { Panorama } from "../components/Panorama";
import { Anchor, Compass } from "../components/Decor";
import { ChartBackdrop } from "../components/Chart";
import { PasswordField } from "../components/PasswordField";
import { ThemeToggle } from "../components/ThemeToggle";

interface Props {
  mode: "setup" | "login";
  onDone: (u: User) => void;
  downMessage?: string;
  onRetry?: () => void;
}

type View = "login" | "invite";

const LAST_LOGIN = "sentinela.login";

function savedLogin(): string {
  try {
    return localStorage.getItem(LAST_LOGIN) ?? "";
  } catch {
    return "";
  }
}

function saveLogin(name: string, remember: boolean) {
  try {
    if (remember) localStorage.setItem(LAST_LOGIN, name);
    else localStorage.removeItem(LAST_LOGIN);
  } catch {
    // private window: nothing to remember
  }
}

/** Portuguese text for the server's sign-in errors. */
function loginError(msg: string): string {
  if (msg.includes("invalid username")) return "Usuário, e-mail ou senha incorretos.";
  if (msg.includes("invalid code")) return "Código incorreto ou já usado. Espere o próximo código e tente de novo.";
  if (msg.includes("not set up")) return "Fora de casa, esta conta precisa da verificação em 2 etapas. Ative-a em casa: Minha conta → Verificação em 2 etapas.";
  if (msg.includes("from this address")) return "Muitas tentativas erradas deste endereço. Espere uma hora.";
  if (msg.includes("for this account")) return "Muitas tentativas nesta conta. Espere uma hora.";
  if (msg.includes("too many")) return "Muitas tentativas. Espere um minuto e tente de novo.";
  if (msg.includes("invalid or expired invite")) return "Convite inválido ou vencido. Peça um novo ao administrador.";
  if (msg.includes("disabled")) return "Esta conta está desativada.";
  if (msg.includes("at least") || msg.includes("too weak") || msg.includes("too common")) return "Senha fraca: use 10+ caracteres misturando letras com números ou símbolos.";
  return msg;
}

export function AuthPage({ mode, onDone, downMessage, onRetry }: Props) {
  const setup = mode === "setup";
  const [view, setView] = useState<View>("login");
  const inApp = canChangeConnection();

  return (
    <div className="auth">
      <div className="auth-art" aria-hidden="true">
        <Panorama />
      </div>
      <div className="auth-panel">
        <ChartBackdrop className="auth-chart" />
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
                ? "O servidor não respondeu. Confira a internet deste aparelho e tente de novo."
                : setup
                  ? "Primeiro acesso. Crie a conta de administrador: ela protege as câmeras, as gravações e os arquivos."
                  : view === "invite"
                    ? "Primeiro acesso: use o código de convite que o administrador passou e crie a sua senha."
                    : "O farol da casa: câmeras ao vivo, gravações e a sua nuvem, num só lugar."}
            </p>
          </div>
          {downMessage ? (
            <div className="stack-sm">
              <button className="btn block primary" onClick={onRetry}>
                Tentar de novo
              </button>
              {inApp && (
                <button className="btn block" onClick={changeConnection}>
                  Trocar endereço do servidor
                </button>
              )}
            </div>
          ) : setup ? (
            <SetupForm onDone={onDone} />
          ) : view === "invite" ? (
            <InviteForm onDone={onDone} onBack={() => setView("login")} />
          ) : (
            <LoginForm onDone={onDone} onInvite={() => setView("invite")} />
          )}
          {!downMessage && !setup && (
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
        </div>
      </div>
    </div>
  );
}

function ErrorNote({ text }: { text: string }) {
  return text ? (
    <p className="notice error" role="alert">
      {text}
    </p>
  ) : null;
}

function Submit({ busy, children }: { busy: boolean; children: React.ReactNode }) {
  return (
    <button className="btn primary block" disabled={busy}>
      {busy ? <span className="spinner small" aria-hidden="true" /> : null}
      {children}
    </button>
  );
}

function RememberBox({ value, onChange }: { value: boolean; onChange: (v: boolean) => void }) {
  return (
    <label className="check-row auth-remember">
      <input type="checkbox" checked={value} onChange={(e) => onChange(e.target.checked)} />
      <span>
        Manter conectado neste aparelho
        <span className="muted small"> por 30 dias</span>
      </span>
    </label>
  );
}

function OtpField({ value, onChange, hint }: { value: string; onChange: (v: string) => void; hint: string }) {
  return (
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
        value={value}
        onChange={(e) => onChange(e.target.value)}
        required
        autoFocus
      />
      <p className="muted small">{hint}</p>
    </div>
  );
}

function LoginForm({ onDone, onInvite }: { onDone: (u: User) => void; onInvite: () => void }) {
  const initial = savedLogin();
  const [login, setLogin] = useState(initial);
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [askCode, setAskCode] = useState(false);
  const [remember, setRemember] = useState(true);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    setBusy(true);
    const username = login.trim();
    try {
      const body = { username, password, remember, ...(askCode ? { code: code.replace(/\s/g, "") } : {}) };
      const r = await api<{ user: User }>("POST", "/api/login", body);
      saveLogin(username, remember);
      onDone(r.user);
    } catch (err) {
      if (err instanceof ApiError && err.mfa) {
        // Only accounts with two-step login get here.
        if (askCode) setError("Digite o código de 6 dígitos do app autenticador.");
        setAskCode(true);
        return;
      }
      setError(loginError((err as Error).message));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} className="auth-form">
      <div className="field">
        <label htmlFor="u">Usuário ou e-mail</label>
        <input
          id="u"
          name="username"
          className="input"
          autoComplete="username"
          autoCapitalize="none"
          spellCheck={false}
          value={login}
          onChange={(e) => {
            setLogin(e.target.value);
            setAskCode(false);
          }}
          required
          autoFocus={!initial}
        />
      </div>
      <PasswordField label="Senha" value={password} onChange={setPassword} autoComplete="current-password" autoFocus={!!initial && !askCode} />
      {askCode && <OtpField value={code} onChange={setCode} hint="Esta conta usa verificação em 2 etapas: abra o app autenticador e digite o código do Sentinela." />}
      <RememberBox value={remember} onChange={setRemember} />
      <ErrorNote text={error} />
      <Submit busy={busy}>Entrar</Submit>
      <button type="button" className="btn ghost block" onClick={onInvite}>
        Primeiro acesso? Tenho um código de convite
      </button>
    </form>
  );
}

function InviteForm({ onDone, onBack }: { onDone: (u: User) => void; onBack: () => void }) {
  const [login, setLogin] = useState("");
  const [invite, setInvite] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [remember, setRemember] = useState(true);
  const [totp, setTotp] = useState<{ qr_svg: string; secret: string } | null>(null);
  const [code, setCode] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    const bad = policyError(password);
    if (bad) return setError(bad);
    if (password !== confirm) return setError("As senhas não são iguais.");
    setBusy(true);
    try {
      const body = { login: login.trim(), code: invite, password, remember, ...(totp ? { totp_code: code.replace(/\s/g, "") } : {}) };
      const r = await api<{ user?: User; totp_required?: boolean; qr_svg?: string; secret?: string }>("POST", "/api/invite/accept", body);
      if (r.totp_required && r.qr_svg && r.secret) {
        setTotp({ qr_svg: r.qr_svg, secret: r.secret });
        return;
      }
      if (r.user) {
        saveLogin(login.trim(), remember);
        onDone(r.user);
      }
    } catch (err) {
      setError(loginError((err as Error).message));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} className="auth-form">
      {!totp ? (
        <>
          <div className="field">
            <label htmlFor="il">Usuário ou e-mail autorizado</label>
            <input id="il" className="input" autoComplete="username" autoCapitalize="none" spellCheck={false} value={login} onChange={(e) => setLogin(e.target.value)} required autoFocus />
          </div>
          <div className="field">
            <label htmlFor="ic">Código de convite</label>
            <input
              id="ic"
              className="input otp"
              autoComplete="off"
              autoCapitalize="characters"
              spellCheck={false}
              placeholder="XXXXX-XXXXX"
              maxLength={14}
              value={invite}
              onChange={(e) => setInvite(e.target.value.toUpperCase())}
              required
            />
          </div>
          <PasswordField label="Crie a sua senha" value={password} onChange={setPassword} autoComplete="new-password" meter />
          <PasswordField label="Repita a senha" value={confirm} onChange={setConfirm} autoComplete="new-password" matches={password} />
          <RememberBox value={remember} onChange={setRemember} />
        </>
      ) : (
        <div className="stack-sm">
          <p className="small">
            Fora de casa, a conta precisa da <strong>verificação em 2 etapas</strong>. Escaneie o QR com um app autenticador (Google Authenticator, Microsoft Authenticator, Aegis…) e digite o código que aparecer.
          </p>
          <img className="totp-qr" src={`data:image/svg+xml;utf8,${encodeURIComponent(totp.qr_svg)}`} alt="QR code da verificação em 2 etapas" />
          <p className="muted small">
            Sem câmera? Digite a chave no app: <code className="totp-secret">{totp.secret.replace(/(.{4})/g, "$1 ").trim()}</code>
          </p>
          <OtpField value={code} onChange={setCode} hint="O código muda a cada 30 segundos." />
        </div>
      )}
      <ErrorNote text={error} />
      <Submit busy={busy}>{totp ? "Confirmar e entrar" : isPublicZone() ? "Continuar" : "Criar senha e entrar"}</Submit>
      <button type="button" className="btn ghost block" onClick={onBack}>
        Voltar para o login
      </button>
    </form>
  );
}

function SetupForm({ onDone }: { onDone: (u: User) => void }) {
  const [username, setUsername] = useState("admin");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    const bad = policyError(password);
    if (bad) return setError(bad);
    if (password !== confirm) return setError("As senhas não são iguais.");
    setBusy(true);
    try {
      const r = await api<{ user: User }>("POST", "/api/setup", { username, password });
      onDone(r.user);
    } catch (err) {
      setError(loginError((err as Error).message));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} className="auth-form">
      <div className="field">
        <label htmlFor="su">Usuário</label>
        <input id="su" className="input" autoComplete="username" autoCapitalize="none" spellCheck={false} value={username} onChange={(e) => setUsername(e.target.value)} required />
      </div>
      <PasswordField label="Senha" value={password} onChange={setPassword} autoComplete="new-password" meter autoFocus />
      <PasswordField label="Repita a senha" value={confirm} onChange={setConfirm} autoComplete="new-password" matches={password} />
      <ErrorNote text={error} />
      <Submit busy={busy}>Criar administrador</Submit>
    </form>
  );
}
