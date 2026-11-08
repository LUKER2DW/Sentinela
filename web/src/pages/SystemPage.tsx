import { type ReactNode } from "react";
import { getLang, plural, t } from "../lib/i18n";
import { useQuery } from "@tanstack/react-query";
import { get, type AuditEntry, type SystemInfo, fetchSystem } from "../lib/api";
import { useSession } from "../lib/session";
import { fmtBytes, fmtDuration, fmtTime, relative, spanDays } from "../lib/time";
import { useCameras } from "./LivePage";
import { VolumeMeter } from "./CloudPage";
import { AddCameraCard } from "../components/AddCamera";
import { ReopenSetupGuide } from "../components/SetupGuide";
import { ChangelogCard, UpdatesCard } from "../components/Changelog";
import { AccountAll } from "./AccountPage";
import { HouseModeCard } from "../components/Notifications";
import { AppCard } from "../components/AppUpdate";
import { AppPairingCard } from "../components/AppPairing";
import { CamerasEditor, RecordingEditor, ServerEditor } from "../components/SettingsEditor";
import { TailscaleCard } from "../components/TailscaleSetup";
import { PublicAccessCard, TunnelCard } from "../components/RemoteAccess";
import { DetectionCard, OverlayCard } from "../components/Detection";
import { useCompact } from "../lib/viewport";
import { MoreMenu } from "../components/MoreMenu";
import { Icon } from "../components/Icon";
import { NoticesPanel, useSystemNotices } from "../components/Notices";
import { UsersAdmin, auditLabel } from "../components/UsersAdmin";
import { href, navigate } from "../lib/router";

const stateLabel: Record<string, string> = {
  recording: t("Recording now"),
  starting: t("Connecting"),
  backoff: t("Trying to reconnect"),
  stopped: t("Stopped"),
};

/** How long recordings are kept: 0 days means until the disk is full, then the oldest are overwritten. */
export function retentionLabel(cams: { retention_days: number }[]): string {
  if (cams.length === 0 || cams.some((c) => c.retention_days <= 0)) return t("until the disk is full (the oldest are replaced)");
  return t("up to {v0} days", { v0: Math.max(...cams.map((c) => c.retention_days)) });
}

/** Every camera's motion events are arriving (unknown counts as fine until the watcher reports). */
export function eventsOk(s: Pick<SystemInfo, "event_watch">): boolean {
  return Object.values(s.event_watch ?? {}).every((w) => w.ok);
}

/** Days of recording that fit, from the real rate; null while unknown. */
export function recordingDays(s: SystemInfo): number | null {
  const recBytes = s.storage.reduce((a, x) => a + x.bytes, 0);
  const daily = Object.values(s.daily_bytes).reduce((a, x) => a + x, 0);
  // Space recordings may use: the retention limit minus everything else on the disk.
  const budget = s.disk.total * (s.max_disk_pct / 100) - (s.disk.used - recBytes);
  return daily > 0 && s.disk.total > 0 ? Math.max(0, Math.floor(budget / daily)) : null;
}

type HouseSection = "overview" | "recording" | "cameras" | "detection" | "app" | "remote" | "server" | "updates" | "users" | "audit";
// The signed-in user's own settings (t("My account")) are one entry of the
// same menu on computers, all on one page.
type Section = HouseSection | "me";
const ME_SECTIONS: { id: Section; label: string; icon: string; admin?: boolean }[] = [
  { id: "me", label: t("My account"), icon: "user" },
];
const SECTIONS: { id: Section; label: string; icon: string; admin?: boolean }[] = [
  { id: "overview", label: t("Overview"), icon: "gauge" },
  { id: "recording", label: t("Recording"), icon: "recordings" },
  { id: "cameras", label: t("Cameras"), icon: "video" },
  { id: "detection", label: t("Detection"), icon: "scan" },
  { id: "app", label: t("App"), icon: "app" },
  { id: "remote", label: t("Remote access"), icon: "network", admin: true },
  { id: "server", label: t("Server and disks"), icon: "server" },
  { id: "updates", label: t("Updates"), icon: "refresh" },
  { id: "users", label: t("Users"), icon: "users", admin: true },
  { id: "audit", label: t("Activity log"), icon: "history", admin: true },
];

export function SystemPage({ params }: { params: URLSearchParams }) {
  const { can } = useSession();
  const compact = useCompact();
  const sys = useQuery({ queryKey: ["system"], queryFn: fetchSystem, refetchInterval: 10_000 });
  const s = sys.data;
  const { unread } = useSystemNotices();
  // Users, the audit log and cameras stay on the computer (owner's choice):
  // rare, wide, and risky to fat-finger on a phone.
  const sections = SECTIONS.filter((x) => (!x.admin || can("admin")) && !(compact && (x.admin || x.id === "cameras")));
  const all = compact ? sections : [...ME_SECTIONS, ...sections];
  const asked = params.get("s") as Section | null;
  const section: Section = all.some((x) => x.id === asked) ? asked! : "overview";
  const mine = section === "me";
  const pick = (id: Section) => navigate("system", { s: id }, { push: compact });

  // Phone: "Mais" is a menu of everything that is not on the tab bar; each
  // row opens its page with a way back.
  if (compact && !asked) return <MoreMenu version={s?.version} unread={unread.length} />;

  return (
    <>
      <header className="page-head">
        <div className="title">
          <h1>{compact ? t(SECTIONS.find((x) => x.id === section)?.label ?? "") : t("System")}</h1>
          {!compact && <span className="sub">{t("Your account, the house and the server, in one place.")}</span>}
        </div>
        {s && !compact && <span className="pill">{t("Version {v}", { v: s.version })}</span>}
      </header>
      <div className="sys-layout">
        {!compact && (
          <nav className="sys-nav" role="tablist" aria-label={t("Categories")}>
            {ME_SECTIONS.map((x) => (
              <button key={x.id} role="tab" aria-selected={section === x.id} onClick={() => pick(x.id)}>
                <Icon name={x.icon} />
                <span>{t(x.label)}</span>
              </button>
            ))}
            <span className="sys-nav-group">{t("Home")}</span>
            {sections.map((x) => (
              <button key={x.id} role="tab" aria-selected={section === x.id} onClick={() => pick(x.id)}>
                <Icon name={x.icon} />
                <span>{t(x.label)}</span>
                {x.id === "overview" && unread.length > 0 && <span className="count-badge">{unread.length}</span>}
              </button>
            ))}
          </nav>
        )}
        <div className="sys-body" key={section} role="tabpanel">
          {mine ? (
            <AccountAll />
          ) : (
            <>
              {sys.isError && <p className="notice error">{t("Could not read the server state.")}</p>}
              {s && <SectionBody section={section as HouseSection} s={s} admin={can("admin")} />}
            </>
          )}
        </div>
      </div>
    </>
  );
}

function SectionBody({ section, s, admin }: { section: HouseSection; s: SystemInfo; admin: boolean }): ReactNode {
  switch (section) {
    case "overview":
      return (
        <div className="stack">
          {admin && (
            <div className="row-gap">
              <ReopenSetupGuide />
            </div>
          )}
          <NoticesPanel />
          <StatusTiles s={s} />
          <div className="grid-2">
            <Storage s={s} />
            <Recorders s={s} />
          </div>
        </div>
      );
    case "app":
      return (
        <div className="sys-cols">
          <AppPairingCard />
          <AppCard />
        </div>
      );
    case "updates":
      return (
        <div className="sys-cols even">
          <UpdatesCard s={s} admin={admin} />
          <ChangelogCard />
        </div>
      );
    case "recording":
      return (
        <div className="stack">
          {admin && <RecordingEditor />}
          <div className="grid-2">
            <Storage s={s} />
            <Recorders s={s} />
          </div>
        </div>
      );
    case "cameras":
      return (
        <div className="stack">
          <div className="grid-2">
            <CamerasCard />
            <LiveService s={s} />
          </div>
          {admin && <CamerasEditor />}
          {admin && <AddCameraCard />}
        </div>
      );
    case "detection":
      return (
        <div className="sys-cols">
          <DetectionCard s={s} />
          <div className="stack">
            <HouseModeCard />
            <OverlayCard />
          </div>
        </div>
      );
    case "server":
      return (
        <div className="stack">
          {admin && <ServerEditor />}
          <div className="grid-2">
            <Server s={s} />
            <Disks s={s} />
          </div>
        </div>
      );
    case "remote":
      return (
        <div className="stack">
          <p className="muted small remote-intro">
            {t("Sentinela answers only on the home network until you choose a way in. Tailscale is the simplest and most private; a public address or a tunnel lets any phone in, behind the sign-in screen (turn on two-step verification). You can use more than one.")}
          </p>
          <div className="sys-cols even">
            <TailscaleCard />
            <div className="stack">
              <TunnelCard />
              <PublicAccessCard />
            </div>
          </div>
        </div>
      );
    case "users":
      return <UsersAdmin />;
    case "audit":
      return <Audit />;
  }
}

/** One line per area, green or not, on the overview. */
function StatusTiles({ s }: { s: SystemInfo }) {
  const days = recordingDays(s);
  const recOk =
    s.recorders.length > 0 && s.recorders.every((r) => r.state === "recording" && new Date(s.now).getTime() - new Date(r.heartbeat).getTime() < 60_000);
  const disksOk = Object.entries(s.disk_health?.mounts ?? {}).every(([m, h]) => m === "/" || h.state === "ok");
  const tiles = [
    { icon: "recordings", label: t("Recording"), ok: recOk, value: recOk ? t("Recording now") : t("With a problem"), to: "recording" },
    { icon: "disk", label: t("Space"), ok: days === null || days >= 7, value: days === null ? t("calculating") : `${days} ${days === 1 ? t("day fits") : t("days fit")}`, to: "recording" },
    {
      icon: "server",
      label: t("Server"),
      ok: !s.host.supported || s.host.cpu_pct < 85,
      value: s.host.supported ? t("{cpu}% CPU · {size} free", { cpu: s.host.cpu_pct.toFixed(0), size: fmtBytes(s.host.mem_available) }) : "—",
      to: "server",
    },
    { icon: "disk", label: t("Disks"), ok: disksOk, value: disksOk ? t("Healthy") : t("Attention"), to: "server" },
    {
      icon: "scan",
      label: "Detector",
      ok: !s.detector || s.detector.healthy,
      value: !s.detector ? t("off") : s.detector.healthy ? t("Analyzing") : t("No signal"),
      to: "detection",
    },
    {
      icon: "bell",
      label: t("Camera events"),
      ok: eventsOk(s),
      value: eventsOk(s) ? t("Receiving") : t("No motion events"),
      to: "detection",
    },
  ];
  return (
    <div className="status-tiles">
      {tiles.map((t) => (
        <a key={t.label} className={`status-tile${t.ok ? "" : " bad"}`} href={href("system", { s: t.to })}>
          <Icon name={t.icon} />
          <span className="muted small">{t.label}</span>
          <strong>{t.value}</strong>
        </a>
      ))}
    </div>
  );
}

function CamerasCard() {
  const cams = useCameras();
  return (
    <section className="card">
      <h2>{t("Cameras")}</h2>
      <div className="rows">
        {(cams.data ?? []).map((c) => (
          <div key={c.id} className="status-row">
            <div>
              <strong>{c.name}</strong>
              <p className="muted small">
                {c.id} · {c.retention_days > 0 ? t("keeps {retention_days} days", { retention_days: c.retention_days }) : t("keeps until the disk is full")}{" "}{t("· audio")}{" "}{c.record_audio ? t("recorded") : t("off")}
              </p>
            </div>
            <a className="btn small ghost" href={href("live", { view: "single", cam: c.id })}>
              {t("Live")}
            </a>
          </div>
        ))}
      </div>
      <p className="muted small card-foot">{t("Name, retention and audio are kept in /etc/sentinela/config.yaml on the server.")}</p>
    </section>
  );
}

function Audit() {
  const audit = useQuery({ queryKey: ["audit"], queryFn: () => get<AuditEntry[]>("/api/audit"), refetchInterval: 30_000 });
  return (
    <section className="card">
      <h2>{t("Activity log")}</h2>
      <div className="table-wrap audit-wrap">
        <table className="table">
          <thead>
            <tr>
              <th>{t("When")}</th>
              <th>{t("Who")}</th>
              <th>{t("What")}</th>
              <th>{t("Detail")}</th>
            </tr>
          </thead>
          <tbody>
            {(audit.data ?? []).map((a) => (
              <tr key={a.id}>
                <td className="num" title={new Date(a.at).toLocaleString(getLang())}>
                  {relative(a.at)}
                </td>
                <td>{a.username || "—"}</td>
                <td>
                  <span className={`dot ${a.ok ? "ok" : "bad"}`} style={{ display: "inline-block", marginRight: 8 }} />
                  {auditLabel(a.action)}
                </td>
                <td className="muted">{[a.target, a.detail].filter(Boolean).join(" ")}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

function Storage({ s }: { s: SystemInfo }) {
  const recBytes = s.storage.reduce((a, x) => a + x.bytes, 0);
  const daily = Object.values(s.daily_bytes).reduce((a, x) => a + x, 0);
  const days = recordingDays(s);
  const oldest = s.storage.map((x) => x.oldest).sort()[0];
  const onDisk = spanDays(s.storage);
  return (
    <section className="card">
      <h2>{t("Storage")}</h2>
      {days !== null ? (
        <p className="big">
          <b>{days}</b>
          <span className="muted">{days === 1 ? t("day of recording fits") : t("days of recording fit")}{" "}{t("on disk")}</span>
        </p>
      ) : (
        <p className="big">
          <b>–</b>
          <span className="muted">{s.disk.total === 0 ? t("disk measured only on the server") : t("calculating the pace…")}</span>
        </p>
      )}
      <dl className="kv card-foot">
        <dt>{t("Recordings")}</dt>
        <dd>{fmtBytes(recBytes)}</dd>
        <dt>{t("Per day")}</dt>
        <dd>{daily > 0 ? fmtBytes(daily) : t("calculating")}</dd>
        <dt>{t("On disk now")}</dt>
        <dd>{onDisk !== null ? plural(onDisk, "{n} day", "{n} days") : t("none")}</dd>
        <dt>{t("Oldest")}</dt>
        <dd>{oldest ? relative(oldest) : t("none")}</dd>
      </dl>
    </section>
  );
}

function Recorders({ s }: { s: SystemInfo }) {
  const cams = useCameras();
  return (
    <section className="card">
      <h2>{t("Recorders")}</h2>
      <div className="rows">
        {(cams.data ?? []).map((c) => {
          const r = s.recorders.find((x) => x.camera_id === c.id);
          const stale = !r || new Date(s.now).getTime() - new Date(r.heartbeat).getTime() > 60_000;
          const ok = r?.state === "recording" && !stale;
          return (
            <div key={c.id} className="status-row">
              <div>
                <strong>{c.name}</strong>
                <p className="muted small">
                  {r ? t("Since {v0}, {restarts} {v2}", { v0: fmtTime(r.since), restarts: r.restarts, v2: r.restarts === 1 ? t("restart") : t("restarts") }) : t("Waiting for the recorder")}
                  {r && r.restarts_1h > 0 && t(" ({restarts_1h} in the last hour)", { restarts_1h: r.restarts_1h })}
                </p>
                <p className="muted small">{gapText(s.gaps_24h?.[c.id])}</p>
                {r?.last_error && !ok && <p className="notice error small">{r.last_error}</p>}
              </div>
              <span className="pill">
                <span className={`dot ${ok ? "rec" : "bad"}`} />
                {stale ? t("No signal") : stateLabel[r!.state] ?? r!.state}
              </span>
            </div>
          );
        })}
      </div>
      <dl className="kv card-foot">
        <dt>{t("Audio")}</dt>
        <dd>{cams.data?.every((c) => c.record_audio) ? t("recorded") : t("off")}</dd>
        <dt>{t("Keep for")}</dt>
        <dd>{retentionLabel(cams.data ?? [])}</dd>
      </dl>
    </section>
  );
}

function LiveService({ s }: { s: SystemInfo }) {
  return (
    <section className="card">
      <h2>{t("Live video")}</h2>
      {s.go2rtc_error ? (
        <p className="notice error">{t("Live video service (go2rtc) is down.")}</p>
      ) : (
        <div className="rows">
          {Object.entries(s.go2rtc ?? {})
            .sort(([a], [b]) => a.localeCompare(b))
            .map(([name, st]) => (
              <div key={name} className="status-row">
                <strong>{name.endsWith("_sub") ? t("{v0} (SD)", { v0: name.replace(/_sub$/, "") }) : t("{name} (HD)", { name })}</strong>
                <span className="pill">
                  <span className={`dot ${st.online ? "ok" : ""}`} />
                  {st.online ? `${st.consumers} assistindo` : t("On hold")}
                </span>
              </div>
            ))}
        </div>
      )}
      <p className="muted small card-foot">{t("The video is only pulled from the camera while someone watches.")}</p>
    </section>
  );
}

function Disks({ s }: { s: SystemInfo }) {
  return (
    <section className="card">
      <h2>{t("Disks")}</h2>
      <div className="stack-sm">
        {(s.volumes ?? []).length === 0 && <p className="muted">{t("No disk configured.")}</p>}
        {(s.volumes ?? []).map((v) => (
          <VolumeMeter key={v.id} v={v} health={s.disk_health?.mounts[volumeMount(v.id)]} />
        ))}
      </div>
      <Backup b={s.backup} now={s.now} />
      <p className="muted small card-foot">{t("Recordings above")}{" "}{s.max_disk_pct}{t("% of the disk, the oldest are deleted.")}</p>
    </section>
  );
}

const resticLabel: Record<string, string> = {
  ok: t("up to date"),
  off: t("no external disk configured"),
  disk_missing: t("external disk disconnected"),
  failed: t("failed"),
};

function Backup({ b, now }: { b: SystemInfo["backup"]; now: string }) {
  if (!b) return null;
  // Nightly job: more than 36 h without a good copy means it is not running.
  const stale = !b.db_at || new Date(now).getTime() - new Date(b.db_at).getTime() > 36 * 3_600_000;
  return (
    <dl className="kv card-foot">
      <dt>{t("Database copy")}</dt>
      <dd className={stale || !b.db_ok ? "bad" : undefined}>{b.db_at ? relative(b.db_at) : t("never")}</dd>
      <dt>{t("External disk")}</dt>
      <dd className={b.restic === "failed" || b.restic === "disk_missing" ? "bad" : undefined}>
        {b.restic === "ok" && b.restic_at ? relative(b.restic_at) : resticLabel[b.restic] ?? b.restic}
      </dd>
    </dl>
  );
}

// Volume id -> host mount point used by scripts/disk-health.sh.
function volumeMount(id: string) {
  return ({ recordings: "/data", nuvem: "/srv/nas", backup: "/srv/backup" } as Record<string, string>)[id] ?? "";
}

function gapText(g: SystemInfo["gaps_24h"][string] | undefined) {
  if (!g) return "";
  if (g.count === 0) return t("No failures in the last 24 h");
  return t("{count} {v1} in the last 24 h (highest: {v2})", { count: g.count, v1: g.count === 1 ? "falha" : "falhas", v2: fmtDuration(g.longest_secs * 1000) });
}

function Server({ s }: { s: SystemInfo }) {
  return (
    <section className="card">
      <h2>{t("Server")}</h2>
      <dl className="kv">
        {s.host.supported && (
          <>
            <dt>{t("Processor")}</dt>
            <dd>
              {s.host.cpu_pct.toFixed(0)}{t("% of")}{" "}{s.host.num_cpu}{" "}{t("cores")}
            </dd>
            <dt>{t("Free memory")}</dt>
            <dd>
              {t("{a} of {b}", { a: fmtBytes(s.host.mem_available), b: fmtBytes(s.host.mem_total) })}
            </dd>
            <dt>{t("On for")}</dt>
            <dd>{fmtDuration(s.host.uptime_ns / 1e6)}</dd>
          </>
        )}
        <dt>{t("Sentinela memory")}</dt>
        <dd>{fmtBytes(s.host.proc_rss)}</dd>
        <dt>{t("Service started")}</dt>
        <dd>{relative(s.started)}</dd>
      </dl>
    </section>
  );
}

