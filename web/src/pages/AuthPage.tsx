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

/** An invite link: #/invite?u=<login>&c=<code> (the fragment never reaches a server). */
function inviteFromLink(): Invite | null {
  const raw = window.location.hash.replace(/^#\/?/, "");
  const [page, query = ""] = raw.split("?");
  // "convite": the page's name in invites sent before 2026-11.
  if (page !== "invite" && page !== "convite") return null;
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
  if (msg.includes("invalid username")) return t("Wrong username, email or password.");
  if (msg.includes("invalid code")) return t("Wrong or already used code. Wait for the next code and try again.");
  if (msg.includes("from this address")) return t("Too many wrong attempts from this address. Wait an hour.");
  if (msg.includes("for this account")) return t("Too many attempts on this account. Wait an hour.");
  if (msg.includes("too many")) return t("Too many attempts. Wait a minute and try again.");
  if (msg.includes("invalid or expired invite")) return t("Invalid or expired invite. Ask the administrator for a new one.");
  if (msg.includes("disabled")) return t("This account is disabled.");
  if (msg.includes("at least") || msg.includes("too weak") || msg.includes("too common")) return t("Weak password: use 10+ characters mixing letters with numbers or symbols.");
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
              <h1>{t("Sentinela")}</h1>
            </div>
            <p className="auth-lede">
              {t(
                downMessage
                  ? t("The server did not respond. Check this device's internet connection and try again.")
                  : setup
                    ? t("First access. Create the administrator account: it protects the cameras, the recordings and the files.")
                    : invite
                      ? t("First access: create your password to sign in to Sentinela.")
                      : t("The house lighthouse: live cameras, recordings and your cloud, in one place."),
              )}
            </p>
          </div>
          {downMessage ? (
            <div className="stack-sm">
              <button className="btn block primary" onClick={onRetry}>
                {t("Try again")}
              </button>
              {inApp && (
                <button className="btn block" onClick={changeConnection}>
                  {t("Change server address")}
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
                  {t("Open in the Sentinela app")}
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
                  {t("This invite link is incomplete. Ask the administrator to send it again.")}
                </p>
                <button className="btn block" onClick={back}>
                  {t("Go to sign-in")}
                </button>
              </div>
            )
          ) : (
            <LoginForm onDone={onDone} />
          )}
          {!downMessage && !setup && (
            <p className="muted small auth-via">
              {t("Connected by:")}{" "}{zoneLabel()}
              {inApp && (
                <>
                  {" · "}
                  <button type="button" className="linkish" onClick={changeConnection}>
                    {t("change")}
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
        {t("Stay signed in on this device")}
        <span className="muted small">{" "}{t("while in use")}</span>
      </span>
    </label>
  );
}

function OtpField({ value, onChange, hint }: { value: string; onChange: (v: string) => void; hint: string }) {
  return (
    <div className="field">
      <label htmlFor="otp">{t("Verification code")}</label>
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
        if (askCode) setError(t("Enter the 6-digit code from the authenticator app."));
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
        <label htmlFor="u">{t("Username or e-mail")}</label>
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
      <PasswordField label={t("Password")} value={password} onChange={setPassword} autoComplete="current-password" autoFocus={!!initial && !askCode} />
      {askCode && <OtpField value={code} onChange={setCode} hint={t("This account uses two-step verification: open the authenticator app and enter the Sentinela code.")} />}
      {!inApp && <RememberBox value={remember} onChange={setRemember} />}
      <ErrorNote text={error} />
      <Submit busy={busy}>{t("Sign in")}</Submit>
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
        {t("Account:")}{" "}<strong>{login}</strong>
      </p>
      {/* Hidden, but lets the password manager save the new password under this name. */}
      <input className="sr-only" name="username" autoComplete="username" value={login} readOnly tabIndex={-1} aria-hidden="true" />
      <PasswordField label={t("New password")} value={password} onChange={setPassword} autoComplete="new-password" meter autoFocus />
      <p className="muted small">{t("Tap the eye to check what you typed.")}</p>
      {!canChangeConnection() && <RememberBox value={remember} onChange={setRemember} />}
      <ErrorNote text={error} />
      <Submit busy={busy}>{t("Save password and sign in")}</Submit>
      <button type="button" className="btn ghost block" onClick={onBack}>
        {t("Back to sign-in")}
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
    if (password !== confirm) return setError(t("The passwords do not match."));
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
        <label htmlFor="su">{t("User")}</label>
        <input id="su" className="input" autoComplete="username" autoCapitalize="none" spellCheck={false} value={username} onChange={(e) => setUsername(e.target.value)} required />
      </div>
      <PasswordField label={t("Password")} value={password} onChange={setPassword} autoComplete="new-password" meter autoFocus />
      <PasswordField label={t("Repeat the password")} value={confirm} onChange={setConfirm} autoComplete="new-password" matches={password} />
      <ErrorNote text={error} />
      <Submit busy={busy}>{t("Create administrator")}</Submit>
    </form>
  );
}
