import { useEffect, useRef, useState } from "react";
import { t } from "../lib/i18n";
import { ApiError, api, type User } from "../lib/api";
import { canChangeConnection, changeConnection, zoneLabel } from "../lib/access";
import { policyError } from "../lib/password";
import { offerToSavePassword, pickSavedPassword } from "../lib/credentials";
import { appIntentURL, currentAppPath, isAndroidBrowser, tryOpenInAppOnce } from "../lib/openInApp";
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

interface Invite {
  login: string;
  code: string;
}

/** An invite link: #/convite?u=<login>&c=<code> (the fragment never reaches a server). */
function inviteFromLink(): Invite | null {
  const raw = window.location.hash.replace(/^#\/?/, "");
  const [page, query = ""] = raw.split("?");
  if (page !== "convite") return null;
  const q = new URLSearchParams(query);
  const login = q.get("u")?.trim() ?? "";
  const code = q.get("c")?.trim() ?? "";
  return login && code ? { login, code } : { login: "", code: "" };
}

function leaveInvite() {
  history.replaceState(null, "", window.location.pathname + window.location.search);
}

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
  const [invite, setInvite] = useState<Invite | null>(inviteFromLink);
  const inApp = canChangeConnection();
  // A link opened in the phone's browser jumps to the app (once).
  useEffect(() => {
    if (invite?.code) tryOpenInAppOnce();
  }, [invite]);
  const back = () => {
    leaveInvite();
    setInvite(null);
  };

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
              {t(
                downMessage
                  ? "O servidor não respondeu. Confira a internet deste aparelho e tente de novo."
                  : setup
                    ? "Primeiro acesso. Crie a conta de administrador: ela protege as câmeras, as gravações e os arquivos."
                    : invite
                      ? "Primeiro acesso: crie a sua senha para entrar no Sentinela."
                      : "O farol da casa: câmeras ao vivo, gravações e a sua nuvem, num só lugar.",
              )}
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
          ) : invite ? (
            invite.code ? (
              <>
              {isAndroidBrowser() && (
                <a className="btn block primary auth-open-app" href={appIntentURL(currentAppPath(), window.location.href)}>
                  Abrir no aplicativo Sentinela
                </a>
              )}
              <InviteForm
                invite={invite}
                onDone={(u) => {
                  leaveInvite();
                  onDone(u);
                }}
                onBack={back}
              />
              </>
            ) : (
              <div className="stack-sm">
                <p className="notice error" role="alert">
                  Este link de convite está incompleto. Peça ao administrador para enviar de novo.
                </p>
                <button className="btn block" onClick={back}>
                  Ir para o login
                </button>
              </div>
            )
          ) : (
            <LoginForm onDone={onDone} />
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
        <span className="muted small"> enquanto for usado</span>
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

function LoginForm({ onDone }: { onDone: (u: User) => void }) {
  const initial = savedLogin();
  const [login, setLogin] = useState(initial);
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [askCode, setAskCode] = useState(false);
  const [remember, setRemember] = useState(true);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const inApp = canChangeConnection();
  const picked = useRef(false);

  // In the app, offer the passwords saved in Android's password manager.
  useEffect(() => {
    if (!inApp || picked.current) return;
    picked.current = true;
    void pickSavedPassword().then((s) => {
      if (!s) return;
      setLogin((cur) => cur || s.username);
      setPassword((cur) => cur || s.password);
    });
  }, [inApp]);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    setBusy(true);
    const username = login.trim();
    try {
      const body = { username, password, remember: remember || inApp, ...(askCode ? { code: code.replace(/\s/g, "") } : {}) };
      const r = await api<{ user: User }>("POST", "/api/login", body);
      saveLogin(username, remember || inApp);
      await offerToSavePassword(username, password);
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
        <label htmlFor="u">{t("Usuário ou e-mail")}</label>
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
      <PasswordField label={t("Senha")} value={password} onChange={setPassword} autoComplete="current-password" autoFocus={!!initial && !askCode} />
      {askCode && <OtpField value={code} onChange={setCode} hint="Esta conta usa verificação em 2 etapas: abra o app autenticador e digite o código do Sentinela." />}
      {!inApp && <RememberBox value={remember} onChange={setRemember} />}
      <ErrorNote text={error} />
      <Submit busy={busy}>{t("Entrar")}</Submit>
    </form>
  );
}

function InviteForm({ invite, onDone, onBack }: { invite: Invite; onDone: (u: User) => void; onBack: () => void }) {
  const login = invite.login;
  const [password, setPassword] = useState("");
  const [remember, setRemember] = useState(true);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    const bad = policyError(password);
    if (bad) return setError(bad);
    setBusy(true);
    try {
      const r = await api<{ user: User }>("POST", "/api/invite/accept", { login, code: invite.code, password, remember });
      saveLogin(login, remember);
      await offerToSavePassword(login, password);
      onDone(r.user);
    } catch (err) {
      setError(loginError((err as Error).message));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} className="auth-form">
      <p className="small">
        Conta: <strong>{login}</strong>
      </p>
      {/* Hidden, but lets the password manager save the new password under this name. */}
      <input className="sr-only" name="username" autoComplete="username" value={login} readOnly tabIndex={-1} aria-hidden="true" />
      <PasswordField label="Nova senha" value={password} onChange={setPassword} autoComplete="new-password" meter autoFocus />
      <p className="muted small">Toque no olho para conferir o que digitou.</p>
      {!canChangeConnection() && <RememberBox value={remember} onChange={setRemember} />}
      <ErrorNote text={error} />
      <Submit busy={busy}>Salvar senha e entrar</Submit>
      <button type="button" className="btn ghost block" onClick={onBack}>
        Voltar para o login
      </button>
    </form>
  );
}

function SetupForm({ onDone }: { onDone: (u: User) => void }) {
  const [username, setUsername] = useState("");
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
        <label htmlFor="su">{t("Usuário")}</label>
        <input id="su" className="input" autoComplete="username" autoCapitalize="none" spellCheck={false} value={username} onChange={(e) => setUsername(e.target.value)} required />
      </div>
      <PasswordField label={t("Senha")} value={password} onChange={setPassword} autoComplete="new-password" meter autoFocus />
      <PasswordField label={t("Repita a senha")} value={confirm} onChange={setConfirm} autoComplete="new-password" matches={password} />
      <ErrorNote text={error} />
      <Submit busy={busy}>{t("Criar administrador")}</Submit>
    </form>
  );
}
