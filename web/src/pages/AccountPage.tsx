import { useState } from "react";
import { t } from "../lib/i18n";
import { useMutation, useQuery } from "@tanstack/react-query";
import { api, get, type User } from "../lib/api";
import { useSession, useToast } from "../lib/session";
import { fmtDate } from "../lib/time";
import { PasswordField } from "../components/PasswordField";
import { policyError } from "../lib/password";
import { SecurityCard } from "../components/Security";
import { DevicesCard } from "../components/Devices";
import { DeviceNotifyCard, NotifyTypesCard, usePushState } from "../components/Notifications";
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
  admin: t("You manage everything: users, cameras and the system."),
  operator: t("You watch, move the camera, turn on the light, talk and upload files."),
  viewer: t("You watch live and recordings and see events."),
} as const;

export type AccountSection = "security" | "devices" | "notify" | "look";
type Section = AccountSection;
export const ACCOUNT_SECTIONS: { id: Section; label: string; icon: string; hint: string }[] = [
  { id: "security", label: t("Password and access"), icon: "key", hint: t("Password and two-step login") },
  { id: "devices", label: t("Devices"), icon: "monitor", hint: t("Where this account is signed in") },
  { id: "notify", label: t("Notifications"), icon: "bell", hint: t("What notifies you") },
  { id: "look", label: t("Appearance"), icon: "sun", hint: t("Light, dark or automatic theme, and language") },
];

/** Minha conta: the signed-in user's own settings, by category. */
export function AccountPage({ params }: { params: URLSearchParams }) {
  const { user, logout, can } = useSession();
  const compact = useCompact();
  const asked = params.get("s") as Section | null;
  const section: Section = ACCOUNT_SECTIONS.some((x) => x.id === asked) ? asked! : "security";
  // On phones each category is its own screen: keep it in the history, so
  // "back" returns to Minha conta instead of the start page.
  const pick = (id: Section) => navigate("account", { s: id }, { push: compact });
  // Phone, opened from a "Mais" row: just that page, like a settings app.
  const sub = compact && !!asked;

  return (
    <>
      <header className="page-head">
        <div className="title">
          <h1>{sub ? ACCOUNT_SECTIONS.find((x) => x.id === section)?.label : t("My account")}</h1>
          {!sub && <span className="sub">{t("Access, devices and notices for this account. Nothing here changes other people's accounts.")}</span>}
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
                  {roleLabel[user.role]}{t(", account since")}{" "}{fmtDate(user.created_at)}
                </p>
                <p className="small">{roleText[user.role]}</p>
              </div>
              <div className="account-hero-actions">
                {can("admin") && !compact && (
                  <a className="btn small" href={href("system", { s: "users" })}>
                    <Icon name="users" />{" "}{t("Users")}
                  </a>
                )}
                <button className="btn small ghost danger" onClick={logout} aria-label={t("Sign out")} title={t("Sign out")}>
                  <Icon name="logout" /> <span className="signout-label">{t("Sign out")}</span>
                </button>
              </div>
            </div>
            <AccountFacts onPick={pick} />
          </section>
        )}

        <div className="sys-layout account-sections">
          {!sub && (
            <nav className={compact ? "section-tabs" : "sys-nav"} role="tablist" aria-label={t("Account categories")}>
              {ACCOUNT_SECTIONS.map((x) => (
                <button key={x.id} role="tab" aria-selected={section === x.id} onClick={() => pick(x.id)}>
                  {!compact && <Icon name={x.icon} />}
                  <span>{x.label}</span>
                </button>
              ))}
            </nav>
          )}
          <div className="sys-body" key={section} role="tabpanel" aria-label={ACCOUNT_SECTIONS.find((x) => x.id === section)?.label}>
            <AccountBody section={section} />
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
      .then(() => toast(t("Profile picture updated.")))
      .catch((e: Error) => toast(e.message, true))
      .finally(() => setBusy(false));
  };
  return (
    <div className="avatar-picker">
      <label className={`avatar-edit${busy ? " busy" : ""}`} title={t("Change profile picture")}>
        <Avatar name={name} className={`big role-${role}`} />
        <span className="avatar-cam" aria-hidden="true">
          <Icon name="camera" />
        </span>
        <input type="file" accept="image/*" className="sr-only" aria-label={t("Choose profile picture")} disabled={busy} onChange={(e) => (pick(e.target.files?.[0]), (e.target.value = ""))} />
      </label>
      <button
        className="linkish small"
        disabled={busy}
        onClick={() =>
          removeAvatar()
            .then(() => toast(t("Picture removed.")))
            .catch((e: Error) => toast(e.message, true))
        }
      >
        {t("Remove picture")}
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
    { s: "security", icon: "shield", label: t("Two-step verification"), value: totp === undefined ? "…" : totp ? t("Active") : t("Off"), ok: totp ?? null },
    { s: "devices", icon: "monitor", label: t("Connected devices"), value: devices.data ? String(devices.data.length) : "…", ok: null },
    {
      s: "notify",
      icon: "bell",
      label: isApp() ? t("Notices in the app") : t("Notices in this browser"),
      value: push === null ? "…" : push === "on" ? t("On") : push === "off" ? t("Off") : t("Unavailable"),
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
      toast(t("Password changed. Other devices were signed out."));
      setCur("");
      setNext("");
      setAgain("");
    },
    onError: (e: Error) => toast(e.message.includes("current password") ? t("Current password is wrong.") : policyError(next) || e.message, true),
  });
  return (
    <section className="card set-card" aria-label={t("Password")}>
      <CardHead icon="key" title={t("Password")} sub={t("Changing the password signs out the other devices on this account.")} />
      <form
        className="form-stack"
        onSubmit={(e) => {
          e.preventDefault();
          if (next === again) change.mutate();
        }}
      >
        <PasswordField label={t("Current password")} value={cur} onChange={setCur} autoComplete="current-password" />
        <PasswordField label={t("New password")} value={next} onChange={setNext} autoComplete="new-password" meter />
        <PasswordField label={t("Repeat the new password")} value={again} onChange={setAgain} autoComplete="new-password" matches={next} />
        <div className="card-foot row-gap">
          <button className="btn primary" disabled={change.isPending || !cur || policyError(next) !== "" || next !== again}>
            {t("Change password")}
          </button>
        </div>
      </form>
    </section>
  );
}

/** One category of the signed-in user's settings (also shown inside the
 *  System page on computers). */
export function AccountBody({ section }: { section: AccountSection }) {
  return (
    <>
        {section === "security" && (
          <div className="grid-2 set-grid">
            <PasswordCard />
            <SecurityCard />
          </div>
        )}
        {section === "devices" && <DevicesCard />}
        {section === "notify" && (
          <div className="grid-2 set-grid">
            <DeviceNotifyCard />
            <NotifyTypesCard />
          </div>
        )}
        {section === "look" && (
          <div className="set-grid single">
            <AppearanceCard />
          </div>
        )}
    </>
  );
}

/** Who is signed in, with the picture, role and "Sair": the top of the
 *  account categories inside the System page. */
export function ProfileCard() {
  const { user, logout } = useSession();
  return (
    <section className="card profile-card">
      <AvatarPicker name={user.username} role={user.role} />
      <div className="account-who">
        <h2>{user.username}</h2>
        <p className="muted small">
          {roleLabel[user.role]}{t(", account since")}{" "}{fmtDate(user.created_at)}. {roleText[user.role]}
        </p>
      </div>
      <button className="btn small ghost danger" onClick={logout} aria-label={t("Sign out")} title={t("Sign out")}>
        <Icon name="logout" /> <span className="signout-label">{t("Sign out")}</span>
      </button>
    </section>
  );
}

/** Everything of "Minha conta" on one page (System on computers): the
 *  profile, then each category under its own heading. */
export function AccountAll() {
  return (
    <div className="stack account-all">
      <ProfileCard />
      {ACCOUNT_SECTIONS.map((x) => (
        <section key={x.id} className="account-block" aria-labelledby={`acc-${x.id}`}>
          <div className="account-block-head">
            <Icon name={x.icon} />
            <h2 id={`acc-${x.id}`}>{t(x.label)}</h2>
            <span className="muted small">{t(x.hint)}</span>
          </div>
          <AccountBody section={x.id} />
        </section>
      ))}
    </div>
  );
}
