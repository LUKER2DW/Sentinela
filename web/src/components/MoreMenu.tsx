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
      <a className="phone-who" href={href("conta")} aria-label="Minha conta">
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

      <section className="menu-group" aria-label="Minha conta">
        <h2 className="menu-title">{t("Minha conta")}</h2>
        <div className="menu-list">
          {ACCOUNT_SECTIONS.map((x) => (
            <Row
              key={x.id}
              to={href("conta", { s: x.id })}
              icon={x.icon}
              label={x.label}
              hint={x.hint}
            />
          ))}
        </div>
      </section>

      <section className="menu-group" aria-label="Sistema">
        <h2 className="menu-title">{t("Sistema")}</h2>
        <div className="menu-list">
          <Row to={href("system", { s: "overview" })} icon="gauge" label="Visão geral" hint="Avisos e saúde do servidor" badge={count(unread)} />
          <Row to={href("system", { s: "recording" })} icon="recordings" label="Gravação" hint="Espaço e gravadores" />
          <Row to={href("system", { s: "detection" })} icon="scan" label="Detecção" hint="Modo da casa, pessoas, veículos e áreas" />
          <Row
            to={href("system", { s: "app" })}
            icon="app"
            label="Aplicativo"
            hint="Conectar e atualizar o app"
            badge={newer ? <span className="count-badge">1</span> : undefined}
          />
          <Row to={href("system", { s: "server" })} icon="server" label="Servidor e discos" hint="Memória, discos e backups" />
        </div>
      </section>

      <section className="menu-group" aria-label="Ferramentas">
        <h2 className="menu-title">{t("Ferramentas")}</h2>
        <div className="menu-list">
          <Row to={href("relogio")} icon="timer" label="Medir atraso" hint="Compara o relógio da câmera com o do servidor" />
        </div>
      </section>

      <div className="menu-list">
        <Row icon="logout" label="Sair desta conta" onClick={logout} danger trailing={<span />} />
      </div>

      <p className="muted small more-foot">
        Sentinela {version ?? ""}
        {info ? `, app ${info.versionName}` : ""}. Câmeras e usuários são configurados no computador.
      </p>
    </div>
  );
}
