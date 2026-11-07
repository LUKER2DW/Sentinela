import type { ReactNode } from "react";
import { Icon } from "./Icon";

/**
 * The one header every settings card uses (Minha conta, notifications):
 * icon tile, title, one line of explanation and an optional status on the
 * right, so cards side by side line up.
 */
export function CardHead({
  icon,
  title,
  sub,
  aside,
  tone,
}: {
  icon: string;
  title: string;
  sub?: ReactNode;
  aside?: ReactNode;
  tone?: "sea" | "coral" | "amber" | "kelp";
}) {
  return (
    <div className="set-head">
      <span className={`set-ico${tone ? ` ${tone}` : ""}`} aria-hidden="true">
        <Icon name={icon} />
      </span>
      <div className="set-title">
        <h2>{title}</h2>
        {sub && <p className="muted small">{sub}</p>}
      </div>
      {aside && <div className="set-aside">{aside}</div>}
    </div>
  );
}
