import type { ReactNode } from "react";
import { t } from "../lib/i18n";
import { href } from "../lib/router";
import { useSession } from "../lib/session";
import { useAppUpdate } from "../lib/appUpdate";
import { ACCOUNT_SECTIONS } from "../pages/AccountPage";
import { OceanScene } from "./Doodles";
import { Icon } from "./Icon";
import { Avatar } from "./Avatar";
import { roleLabel } from "./UsersAdmin";

function Row({
  to,
  icon,
  label,
  hint,
  badge,
  onClick,
  danger,
  trailing,
}: {
  to?: string;
  icon: string;
  label: string;
  hint?: string;
  badge?: ReactNode;
  onClick?: () => void;
  danger?: boolean;
  trailing?: ReactNode;
}) {
  const body = (
    <>
      <span className="menu-ico" aria-hidden="true">
        <Icon name={icon} />
      </span>
      <span className="menu-text">
        <span>{t(label)}</span>
        {hint && <span className="muted small">{t(hint)}</span>}
      </span>
      {badge}
      {trailing ?? (to && <Icon name="chevronRight" />)}
    </>
  );
  const cls = `menu-row${danger ? " danger" : ""}`;
  return to ? (
    <a className={cls} href={to}>
      {body}
    </a>
  ) : (
    <button className={cls} onClick={onClick}>
      {body}
    </button>
  );
}

/**
 * The phone's "Mais" tab: the account and every settings page that is not
 * on the tab bar, as a grouped list (the pattern phones already know from
 * their own settings app). Cameras, users and the audit log stay on the
 * computer.
 */
export function MoreMenu({ version, unread }: { version?: string; unread: number }) {
  const { user, logout } = useSession();
  const { newer, info } = useAppUpdate();
  const count = (n: number) => (n > 0 ? <span className="count-badge">{n}</span> : null);
  return (
    <div className="more">
      <a className="phone-who" href={href("account")} aria-label={t("My account")}>
        <OceanScene className="phone-ocean" />
        <div className="who">
          <Avatar name={user.username} />
          <div className="who-text">
            <div className="who-name">{user.username}</div>
            <div className="who-role">{roleLabel[user.role]}</div>
          </div>
          <Icon name="chevronRight" />
        </div>
      </a>

      <section className="menu-group" aria-label={t("My account")}>
        <h2 className="menu-title">{t("My account")}</h2>
        <div className="menu-list">
          {ACCOUNT_SECTIONS.map((x) => (
            <Row
              key={x.id}
              to={href("account", { s: x.id })}
              icon={x.icon}
              label={x.label}
              hint={x.hint}
            />
          ))}
        </div>
      </section>

      <section className="menu-group" aria-label={t("System")}>
        <h2 className="menu-title">{t("System")}</h2>
        <div className="menu-list">
          <Row to={href("system", { s: "overview" })} icon="gauge" label={t("Overview")} hint={t("Notices and server health")} badge={count(unread)} />
          <Row to={href("system", { s: "recording" })} icon="recordings" label={t("Recording")} hint={t("Space and recorders")} />
          <Row to={href("system", { s: "detection" })} icon="scan" label={t("Detection")} hint={t("House mode, people, vehicles and zones")} />
          <Row
            to={href("system", { s: "app" })}
            icon="app"
            label={t("App")}
            hint={t("Connect and update the app")}
            badge={newer ? <span className="count-badge">1</span> : undefined}
          />
          <Row to={href("system", { s: "server" })} icon="server" label={t("Server and disks")} hint={t("Memory, disks and backups")} />
        </div>
      </section>

      <section className="menu-group" aria-label={t("Tools")}>
        <h2 className="menu-title">{t("Tools")}</h2>
        <div className="menu-list">
          <Row to={href("clock")} icon="timer" label={t("Measure delay")} hint={t("Compares the camera's clock with the server's")} />
        </div>
      </section>

      <div className="menu-list">
        <Row icon="logout" label={t("Sign out of this account")} onClick={logout} danger trailing={<span />} />
      </div>

      <p className="muted small more-foot">
        {t("Sentinela")}{" "}{version ?? ""}
        {info ? t(", app {versionName}", { versionName: info.versionName }) : ""}{t(". Cameras and users are configured on the computer.")}
      </p>
    </div>
  );
}
