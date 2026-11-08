import { useEffect, useMemo, useState, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import changelog from "../../../CHANGELOG.md?raw";
import { api, get, type SystemInfo } from "../lib/api";
import { useToast } from "../lib/session";
import { isPublicZone } from "../lib/access";
import { Icon } from "./Icon";

import { t, getLang } from "../lib/i18n";
/** One version of CHANGELOG.md: its title and its "### group" lists. */
export interface ChangelogVersion {
  title: string;
  intro: string[];
  groups: { title: string; items: string[] }[];
}

/** Parses the Keep a Changelog subset the project uses: "## [version]",
 *  "### group", "- item" (continuation lines indented), plain paragraphs. */
export function parseChangelog(md: string): ChangelogVersion[] {
  const out: ChangelogVersion[] = [];
  let v: ChangelogVersion | null = null;
  let g: ChangelogVersion["groups"][number] | null = null;
  for (const raw of md.split(/\r?\n/)) {
    const line = raw.trimEnd();
    if (line.startsWith("## ")) {
      v = { title: line.slice(3).replace(/^\[|\]/g, "").trim(), intro: [], groups: [] };
      out.push(v);
      g = null;
    } else if (!v || line.startsWith("[") || line.startsWith("# ")) {
      continue;
    } else if (line.startsWith("### ")) {
      g = { title: line.slice(4).trim(), items: [] };
      v.groups.push(g);
    } else if (line.startsWith("- ")) {
      if (!g) {
        g = { title: "", items: [] };
        v.groups.push(g);
      }
      g.items.push(line.slice(2).trim());
    } else if (/^\s+\S/.test(line) && g && g.items.length) {
      g.items[g.items.length - 1] += " " + line.trim();
    } else if (line.trim() && !g) {
      v.intro.push(line.trim());
    }
  }
  return out;
}

/** `code` spans only; everything else stays plain text (no HTML injection). */
function inline(text: string): ReactNode[] {
  return text.split(/(`[^`]+`)/).map((part, i) =>
    part.startsWith("`") && part.endsWith("`") ? <code key={i}>{part.slice(1, -1)}</code> : part,
  );
}

const versionLabel = (v: string) => (v.toLowerCase() === "unreleased" ? t("Next version") : v);

/** "Novidades": the changelog shipped with this build. */
export function ChangelogCard() {
  const versions = useMemo(() => parseChangelog(changelog), []);
  const [open, setOpen] = useState(1);
  return (
    <section className="card" aria-label={t("What's new")}>
      <div className="card-head">
        <h2>{t("What's new")}</h2>
        <span className="muted small">{t("What changed in each version")}</span>
      </div>
      {versions.slice(0, open).map((v) => (
        <article key={v.title} className="changelog-version">
          <h3>{versionLabel(v.title)}</h3>
          {v.intro.map((p, i) => (
            <p key={i} className="muted small">
              {inline(p)}
            </p>
          ))}
          {v.groups.map((g, i) => (
            <div key={i} className="changelog-group">
              {g.title && <h4>{g.title}</h4>}
              <ul>
                {g.items.map((it, j) => (
                  <li key={j}>{inline(it)}</li>
                ))}
              </ul>
            </div>
          ))}
        </article>
      ))}
      {open < versions.length && (
        <button className="btn ghost small" onClick={() => setOpen(versions.length)}>
          {t("Show earlier versions")}
        </button>
      )}
    </section>
  );
}

interface UpdateLast {
  checked: string;
  state: "current" | "available" | "updated" | "updating" | "manual" | "major" | "failed" | "error";
  current: string;
  latest: string;
  message: string;
}
interface UpdateSettings {
  enabled: boolean;
  at: string;
  major: boolean;
  replace_manual: boolean;
}
interface UpdatesInfo {
  version: string;
  settings: UpdateSettings;
  last: UpdateLast | null;
  /** A check or install asked for and not yet picked up by the host. */
  pending: boolean;
  /** Set while the host installs an update: how far it is and what it is doing. */
  progress?: { percent: number; step: string } | null;
  /** The Android app the server offers to phones, and its last check. */
  app: { offered?: string; checked?: string; error?: string };
  host: boolean;
}

const stateText: Record<UpdateLast["state"], string> = {
  current: t("Up to date"),
  available: t("A new version is available"),
  updated: t("Updated"),
  updating: t("Updating now"),
  manual: t("Version installed from a computer: the automatic update leaves it alone unless you allow it below"),
  major: t("There is a new version with big changes: read the notes and allow major versions to install"),
  failed: t("The last update failed and was rolled back"),
  error: t("Could not check"),
};

const stepText: Record<string, string> = {
  download: t("Downloading the update files"),
  verify: t("Checking the downloads"),
  image: t("Downloading the server image"),
  install: t("Installing"),
  restart: t("Restarting the server and checking it"),
};

const hours = Array.from({ length: 24 }, (_, h) => `${String(h).padStart(2, "0")}:00`);

/** Server version and automatic updates (ADR-0023). Admins switch them on,
 *  pick the hour and can check right away; the host does the work. */
export function UpdatesCard({ s, admin }: { s: SystemInfo; admin: boolean }) {
  const toast = useToast();
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["updates"], queryFn: () => get<UpdatesInfo>("/api/updates"), enabled: admin, refetchInterval: 30_000 });
  const save = useMutation({
    mutationFn: (next: UpdateSettings) => api<UpdateSettings>("PUT", "/api/updates", next),
    onSuccess: () => {
      toast(t("Update preferences saved"));
      void qc.invalidateQueries({ queryKey: ["updates"] });
    },
    onError: (e: Error) => toast(t("Updates: {message}", { message: e.message }), true),
  });
  // After a request, the card refreshes every few seconds until the host
  // has answered it (or two minutes passed).
  const [watching, setWatching] = useState(false);
  useEffect(() => {
    if (!watching) return;
    const id = window.setTimeout(() => setWatching(false), 600_000);
    return () => window.clearTimeout(id);
  }, [watching]);
  // An update running (started here or elsewhere) is followed every second.
  const running = !!q.data?.progress || q.data?.last?.state === "updating";
  const following = watching || running;
  const fast = useQuery({
    queryKey: ["updates", "watch"],
    queryFn: () => get<UpdatesInfo>("/api/updates"),
    enabled: admin && following,
    refetchInterval: 1_000,
    retry: true,
  });
  const check = useMutation({
    mutationFn: (install: boolean) => api("POST", "/api/updates/check", { install }),
    onSuccess: (_, install) => {
      setWatching(true);
      toast(install ? t("Updating: the server restarts in a moment and this page reconnects") : t("Checking for updates…"));
    },
    onError: (e: Error) => toast(t("Updates: {message}", { message: e.message }), true),
  });
  const [confirmInstall, setConfirmInstall] = useState(false);
  const info = (following && fast.data) || q.data;
  const st = info?.settings;
  const last = (info?.last ?? (s.update?.last as UpdateLast | null | undefined)) || null;
  const set = (patch: Partial<UpdateSettings>) => st && save.mutate({ ...st, ...patch });
  const outside = isPublicZone();

  return (
    <section className="card" aria-label={t("Updates")}>
      <div className="card-head">
        <h2>{t("Updates")}</h2>
        <span className="pill">{t("Version")}{" "}{s.version}</span>
      </div>
      {admin && st && (
        <>
          <div className="toggle-row">
            <span>
              <Icon name="refresh" />{" "}{t("Update automatically")}
            </span>
            <button
              className={`switch${st.enabled ? " on" : ""}`}
              role="switch"
              aria-checked={st.enabled}
              aria-label={t("Update automatically")}
              disabled={save.isPending || outside}
              onClick={() => set({ enabled: !st.enabled })}
            />
          </div>
          <div className="row-gap" style={{ marginTop: 8 }}>
            <label className="field">
              <span>{t("Time (when the house uses it least)")}</span>
              <select className="input" value={st.at} disabled={save.isPending || outside} onChange={(e) => set({ at: e.target.value })}>
                {hours.map((h) => (
                  <option key={h} value={h}>
                    {h}
                  </option>
                ))}
              </select>
            </label>
          </div>
          <label className="check-row">
            <input type="checkbox" checked={st.major} disabled={save.isPending || outside} onChange={(e) => set({ major: e.target.checked })} />
            {t("Also install major versions (they may change settings)")}
          </label>
          <label className="check-row">
            <input
              type="checkbox"
              checked={st.replace_manual}
              disabled={save.isPending || outside}
              onChange={(e) => set({ replace_manual: e.target.checked })}
            />
            {t("Replace a version installed from a computer")}
          </label>
          <div className="row-gap" style={{ marginTop: 10 }}>
            <button className="btn small" disabled={check.isPending || outside || !info?.host || info?.pending} onClick={() => check.mutate(false)}>
              <Icon name="refresh" />{" "}{info?.pending ? t("Checking…") : t("Check for updates")}
            </button>
            {last?.latest && (last.state === "available" || last.state === "manual" || last.state === "major") && (
              <button className="btn small primary" disabled={check.isPending || outside || !info?.host || info?.pending} onClick={() => setConfirmInstall(true)}>
                {t("Update now to {version}", { version: last.latest })}
              </button>
            )}
          </div>
          {(info?.progress || last?.state === "updating") && (
            <div className="update-progress" role="status">
              <div className="update-progress-head">
                <span>{info?.progress ? stepText[info.progress.step] ?? t("Updating") : fast.isError ? t("Restarting the server and checking it") : t("Updating")}</span>
                <span className="muted small">{info?.progress ? `${info.progress.percent}%` : ""}</span>
              </div>
              <div
                className="progress-track"
                role="progressbar"
                aria-valuemin={0}
                aria-valuemax={100}
                aria-valuenow={info?.progress?.percent ?? 90}
                aria-label={t("Update progress")}
              >
                <div className="progress-fill" style={{ width: `${info?.progress?.percent ?? 90}%` }} />
              </div>
              <p className="muted small">{t("Live view and recording pause for a few seconds near the end. This page reconnects by itself.")}</p>
            </div>
          )}
          {last?.state === "updated" && info?.version && info.version !== s.version && (
            <p className="notice">
              {t("Updated to {version}.", { version: info.version })}{" "}
              <button className="btn small" onClick={() => window.location.reload()}>
                {t("Reload the page")}
              </button>
            </p>
          )}
          {confirmInstall && last?.latest && (
            <div className="dialog-backdrop" role="dialog" aria-modal="true" aria-labelledby="upd-title" onClick={(e) => e.target === e.currentTarget && setConfirmInstall(false)}>
              <div className="dialog">
                <h2 id="upd-title">{t("Update to {version} now?", { version: last.latest })}</h2>
                <p className="muted">
                  {t("The server downloads the published version, checks it and restarts. Live view and recording pause for a few seconds; if anything fails it goes back to the current version.")}
                </p>
                <div className="dialog-actions">
                  <button className="btn ghost" onClick={() => setConfirmInstall(false)}>
                    {t("Cancel")}
                  </button>
                  <button
                    className="btn primary"
                    onClick={() => {
                      setConfirmInstall(false);
                      check.mutate(true);
                    }}
                  >
                    {t("Update now")}
                  </button>
                </div>
              </div>
            </div>
          )}
          {outside && <p className="muted small">{t("Change this at home or over Tailscale.")}</p>}
          {info && !info.host && (
            <p className="notice">
              {t("This server does not have the update service yet. Run once")}{" "}
              <code>sudo /opt/sentinela/scripts/setup-auto-update.sh install</code>{" "}{t("(or run a deploy).")}
            </p>
          )}
        </>
      )}
      <dl className="setup-facts">
        <dt>{t("This server")}</dt>
        <dd>{last?.current && last.current !== "unknown" ? last.current : s.version}</dd>
        {last?.latest && (
          <>
            <dt>{t("Latest published version")}</dt>
            <dd>{last.latest}</dd>
          </>
        )}
        {last && (
          <>
            <dt>{t("Status")}</dt>
            <dd>
              {stateText[last.state] ?? last.state}
              {last.message && last.state !== "manual" && last.state !== "major" ? ` (${last.message})` : ""}
            </dd>
            <dt>{t("Last check")}</dt>
            <dd>{new Date(last.checked).toLocaleString(getLang())}</dd>
          </>
        )}
        {admin && info?.app && (
          <>
            <dt>{t("App offered to phones")}</dt>
            <dd>
              {info.app.offered ?? "—"}
              {info.app.error ? <span className="muted small">{" "}{t("(could not check: {message})", { message: info.app.error })}</span> : null}
            </dd>
          </>
        )}
      </dl>
      {admin && st && (
        <p className="muted small">
          {st.enabled
            ? t("Every day at {at} the server checks for a new version and installs it; the app for phones is checked at the same time. Check for updates and Update now act right away.", { at: st.at })
            : t("Every day at {at} the app for phones is checked. Turn on Update automatically to install server versions at that time too. Check for updates and Update now act right away.", { at: st.at })}
        </p>
      )}
      <p className="muted small">
        {t("The server only downloads ready versions (nothing is compiled on it), checks the SHA-256 sums, restarts in seconds and goes back to the previous version if anything fails.")}
      </p>
    </section>
  );
}
