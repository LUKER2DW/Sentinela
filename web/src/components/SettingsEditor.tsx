import { useState, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, get } from "../lib/api";
import { useToast } from "../lib/session";
import { isPublicZone } from "../lib/access";
import { Icon } from "./Icon";

import { t } from "../lib/i18n";
// Settings editors for System (ADR-0024). The API validates a change and
// leaves it for the host, which applies it, restarts what needs it and
// rolls back on failure; the dashboard reloads by itself meanwhile.

export interface SettingsCamera {
  id: string;
  name: string;
  host: string;
  vendor: string;
  channel: number;
  rtsp_main: string;
  rtsp_sub: string;
  rtsp_port: number;
  dvrip_port: number;
  onvif_port: number;
  retention_days: number;
  record: boolean;
  record_audio: boolean;
  preload: boolean;
  invert_pan_tilt: boolean;
  /** What the camera has: its controls show only when on (PTZ and light need XMEye/DVRIP). */
  ptz: boolean;
  light: boolean;
  talk: boolean;
  /** Where motion comes from: auto (ONVIF events while they flow, else the video), camera or video. */
  motion: string;
  user?: string;
  password?: string;
}
export interface SettingsVolume {
  id: string;
  name: string;
  path: string;
  kind: string;
  share: string;
}
export interface SettingsDoc {
  /** Remote access (System → Remote access); kept as is by this editor. */
  remote?: import("./RemoteAccess").SettingsRemote;
  timezone: string;
  nas_host: string;
  tailnet_url: string;
  recording: {
    segment_seconds: number;
    max_disk_percent: number;
    default_retention_days: number;
    alert_retention_days: number;
    volume_marker: string;
  };
  cameras: SettingsCamera[];
  volumes: SettingsVolume[];
}
/** One stream profile reported by the camera over ONVIF (no credentials). */
interface OnvifStream {
  token: string;
  name: string;
  encoding: string;
  width: number;
  height: number;
  port: number;
  path: string;
}
interface SettingsInfo {
  settings: SettingsDoc;
  vendors: string[];
  pending: boolean;
  last: { at: string; state: "applying" | "applied" | "failed"; message: string } | null;
  host: boolean;
}

const vendorLabel: Record<string, string> = {
  generic: t("Any camera (ONVIF / RTSP)"),
  xmeye: t("XMEye / Xiongmai (with PTZ and light)"),
  hikvision: "Hikvision",
  dahua: "Dahua / Amcrest",
};

function useSettings() {
  return useQuery({
    queryKey: ["settings"],
    queryFn: () => get<SettingsInfo>("/api/settings"),
    // While the host applies a change the API restarts: keep asking.
    refetchInterval: (q) => (q.state.data?.pending || q.state.data?.last?.state === "applying" || q.state.error ? 3000 : false),
    retry: 10,
  });
}

function useApply() {
  const toast = useToast();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (doc: SettingsDoc) => api("PUT", "/api/settings", doc),
    onSuccess: () => {
      toast(t("Saved. Applying on the server; the panel comes back in a few seconds."));
      void qc.invalidateQueries({ queryKey: ["settings"] });
    },
    onError: (e: Error) => toast(e.message, true),
  });
}

/** Where a change stands, above each editor. */
function ApplyState({ info }: { info?: SettingsInfo }) {
  if (!info) return null;
  if (!info.host)
    return (
      <p className="notice">
        {t("This server does not apply panel changes yet. Run once")}{" "}
        <code>sudo /opt/sentinela/scripts/setup-config-apply.sh</code>{" "}{t("(or run a deploy).")}
      </p>
    );
  if (info.pending || info.last?.state === "applying")
    return (
      <p className="setup-status">
        <Icon name="refresh" />{" "}{t("Applying changes… recording and live come back in seconds.")}
      </p>
    );
  if (info.last?.state === "failed")
    return (
      <p className="notice error">
        {t("The last change was not applied and the server went back to the previous one:")}{" "}{info.last.message}
      </p>
    );
  return null;
}

/** Shared shell: a card with the editor and its save button. */
function EditorCard(props: {
  title: string;
  hint: string;
  info?: SettingsInfo;
  dirty: boolean;
  onSave: () => void;
  onReset: () => void;
  busy: boolean;
  children: ReactNode;
}) {
  const locked = !props.info?.host || props.info.pending || isPublicZone();
  return (
    <section className="card settings-card">
      <div className="card-head">
        <h2>{props.title}</h2>
        <span className="muted small">{props.hint}</span>
      </div>
      <ApplyState info={props.info} />
      {props.children}
      <div className="settings-actions">
        {isPublicZone() && <span className="muted small">{t("Change the settings at home or over Tailscale.")}</span>}
        <span className="spacer" />
        {props.dirty && (
          <button className="btn ghost small" onClick={props.onReset} disabled={props.busy}>
            {t("Discard")}
          </button>
        )}
        <button className="btn primary small" onClick={props.onSave} disabled={!props.dirty || props.busy || locked}>
          {t("Save and apply")}
        </button>
      </div>
    </section>
  );
}

/** The editor's working copy. It follows the server whenever the saved
 *  settings change (after an apply), and keeps unsaved edits otherwise. */
function useDraft(info?: SettingsInfo) {
  const saved = info && !info.pending ? JSON.stringify(info.settings) : "";
  const [state, setState] = useState<{ saved: string; draft: SettingsDoc | null }>({ saved: "", draft: null });
  if (saved && saved !== state.saved) setState({ saved, draft: JSON.parse(saved) as SettingsDoc });
  const draft = state.draft;
  const setDraft = (d: SettingsDoc) => setState((s) => ({ ...s, draft: d }));
  const dirty = !!draft && !!saved && JSON.stringify(draft) !== saved;
  return { draft, setDraft, dirty, reset: () => saved && setDraft(JSON.parse(saved) as SettingsDoc) };
}

function NumField(p: { label: string; value: number; min?: number; max?: number; onChange: (n: number) => void; hint?: string }) {
  return (
    <label className="field">
      <span>{p.label}</span>
      <input
        className="input"
        type="number"
        min={p.min}
        max={p.max}
        value={Number.isFinite(p.value) ? p.value : 0}
        onChange={(e) => p.onChange(Number(e.target.value))}
      />
      {p.hint && <span className="muted small">{p.hint}</span>}
    </label>
  );
}

function TextField(p: { label: string; value: string; onChange: (v: string) => void; placeholder?: string; hint?: string; type?: string; autoComplete?: string }) {
  return (
    <label className="field">
      <span>{p.label}</span>
      <input
        className="input"
        type={p.type ?? "text"}
        value={p.value}
        placeholder={p.placeholder}
        autoComplete={p.autoComplete ?? "off"}
        spellCheck={false}
        onChange={(e) => p.onChange(e.target.value)}
      />
      {p.hint && <span className="muted small">{p.hint}</span>}
    </label>
  );
}

function Check(p: { label: string; checked: boolean; onChange: (v: boolean) => void }) {
  return (
    <label className="check-row">
      <input type="checkbox" checked={p.checked} onChange={(e) => p.onChange(e.target.checked)} /> {p.label}
    </label>
  );
}

/** While the settings load, or when this server cannot edit them. */
function EditorUnavailable({ q }: { q: ReturnType<typeof useSettings> }) {
  if (q.isLoading) return null;
  return (
    <p className="notice">
      {t("The settings could not be read")}{q.error ? `: ${(q.error as Error).message}` : ""}{t(". Update the server (deploy) to edit here.")}
    </p>
  );
}

// ---------- recording ----------

export function RecordingEditor() {
  const q = useSettings();
  const apply = useApply();
  const { draft, setDraft, dirty, reset } = useDraft(q.data);
  if (!draft) return <EditorUnavailable q={q} />;
  const r = draft.recording;
  const set = (patch: Partial<SettingsDoc["recording"]>) => setDraft({ ...draft, recording: { ...r, ...patch } });
  return (
    <EditorCard title={t("Recording settings")} hint={t("Applies to all cameras")} info={q.data} dirty={dirty} onSave={() => apply.mutate(draft)} onReset={reset} busy={apply.isPending}>
      <div className="settings-grid">
        <NumField label={t("Use the disk up to (%)")} min={50} max={98} value={r.max_disk_percent} onChange={(n) => set({ max_disk_percent: n })} hint={t("Above that, the oldest recordings are replaced.")} />
        <NumField label={t("Keep at most (days)")} min={0} value={r.default_retention_days} onChange={(n) => set({ default_retention_days: n })} hint={t("0 = until the disk is full.")} />
        <NumField label={t("Keep alerts for (days)")} min={0} value={r.alert_retention_days} onChange={(n) => set({ alert_retention_days: n })} hint={t("Clips with a person, vehicle or line crossed. 0 = same as the others.")} />
        <NumField label={t("Size of each file (s)")} min={30} max={3600} value={r.segment_seconds} onChange={(n) => set({ segment_seconds: n })} />
      </div>
      <Check
        label={t("Record only to the recordings disk (pauses if it does not mount)")}
        checked={r.volume_marker !== "none"}
        onChange={(v) => set({ volume_marker: v ? ".sentinela-volume" : "none" })}
      />
    </EditorCard>
  );
}

// ---------- cameras ----------

const blankCamera = (n: number): SettingsCamera => ({
  id: `cam${n}`,
  name: t("Camera {n}", { n }),
  host: "",
  vendor: "generic",
  channel: 1,
  rtsp_main: "",
  rtsp_sub: "",
  rtsp_port: 554,
  dvrip_port: 34567,
  onvif_port: 80,
  retention_days: 0,
  record: true,
  record_audio: true,
  preload: true,
  invert_pan_tilt: false,
  // Vendor defaults (generic): no speaker until the person says so.
  ptz: true,
  light: true,
  talk: false,
  motion: "auto",
  user: "admin",
  password: "",
});

export function CamerasEditor() {
  const q = useSettings();
  const apply = useApply();
  const { draft, setDraft, dirty, reset } = useDraft(q.data);
  const [open, setOpen] = useState<string | null>(null);
  const [remove, setRemove] = useState<string | null>(null);
  const toast = useToast();
  const [found, setFound] = useState<Record<string, OnvifStream[]>>({});
  const discover = useMutation({
    mutationFn: (c: SettingsCamera) =>
      api<{ streams: OnvifStream[] }>("POST", "/api/cameras/onvif-streams", {
        host: c.host,
        onvif_port: c.onvif_port || 80,
        user: c.user ?? "",
        password: c.password ?? "",
      }),
    onError: (e: Error) => toast(t("ONVIF: {message}", { message: e.message }), true),
  });
  if (!draft) return <EditorUnavailable q={q} />;
  const known = new Set(q.data?.settings.cameras.map((c) => c.id));
  const setCam = (i: number, patch: Partial<SettingsCamera>) =>
    setDraft({ ...draft, cameras: draft.cameras.map((c, j) => (j === i ? { ...c, ...patch } : c)) });
  // ONVIF fills the stream paths of any camera: the largest picture is the
  // main stream, the next one the light stream.
  const fromOnvif = (i: number) => {
    const c = draft.cameras[i];
    discover.mutate(c, {
      onSuccess: (r) => {
        const [main, sub] = r.streams;
        setFound((f) => ({ ...f, [c.id]: r.streams }));
        setCam(i, { vendor: "generic", rtsp_main: main.path, rtsp_sub: sub?.path ?? "", rtsp_port: main.port, onvif_port: c.onvif_port || 80 });
        toast(t("{length} stream(s) found over ONVIF", { length: r.streams.length }));
      },
    });
  };
  const add = () => {
    let n = draft.cameras.length + 1;
    while (draft.cameras.some((c) => c.id === `cam${n}`)) n++;
    const c = blankCamera(n);
    setDraft({ ...draft, cameras: [...draft.cameras, c] });
    setOpen(c.id);
  };
  return (
    <EditorCard title={t("Camera settings")} hint={t("Address, brand, recording and access")} info={q.data} dirty={dirty} onSave={() => apply.mutate(draft)} onReset={reset} busy={apply.isPending}>
      <ul className="settings-list">
        {draft.cameras.map((c, i) => {
          const isNew = !known.has(c.id);
          const expanded = open === c.id;
          return (
            <li key={i} className={expanded ? "open" : ""}>
              <button className="settings-row" onClick={() => setOpen(expanded ? null : c.id)} aria-expanded={expanded}>
                <Icon name="video" />
                <strong>{c.name || c.id}</strong>
                <span className="muted small">
                  {vendorLabel[c.vendor] ?? c.vendor} · {c.host || t("no address")} · {c.record ? t("recording") : t("no recording")}
                  {isNew ? t(" · new") : ""}
                </span>
                <Icon name={expanded ? "up" : "down"} />
              </button>
              {expanded && (
                <div className="settings-body">
                  <div className="settings-grid">
                    <TextField label={t("Name")} value={c.name} onChange={(v) => setCam(i, { name: v })} />
                    <TextField label={t("Identifier")} value={c.id} onChange={(v) => setCam(i, { id: v.toLowerCase() })} hint={isNew ? t("Letters, numbers, - and _ (cannot be changed later).") : t("Cannot be changed.")} />
                    <TextField label={t("IP address")} value={c.host} onChange={(v) => setCam(i, { host: v.trim() })} placeholder="192.168.1.20" />
                    <label className="field">
                      <span>{t("Brand")}</span>
                      <select className="input" value={c.vendor} onChange={(e) => setCam(i, { vendor: e.target.value, talk: e.target.value === "xmeye" })}>
                        {(q.data?.vendors ?? []).map((v) => (
                          <option key={v} value={v}>
                            {vendorLabel[v] ?? v}
                          </option>
                        ))}
                      </select>
                    </label>
                    {c.vendor === "generic" && (
                      <>
                        <TextField label={t("Main stream path")} value={c.rtsp_main} onChange={(v) => setCam(i, { rtsp_main: v })} placeholder="/stream1" />
                        <TextField label={t("Light stream path (optional)")} value={c.rtsp_sub} onChange={(v) => setCam(i, { rtsp_sub: v })} placeholder="/stream2" />
                      </>
                    )}
                    <NumField label={t("Channel (NVR/DVR)")} min={1} max={64} value={c.channel} onChange={(n) => setCam(i, { channel: n })} />
                    <NumField label={t("RTSP port")} min={1} max={65535} value={c.rtsp_port} onChange={(n) => setCam(i, { rtsp_port: n })} />
                    {c.vendor === "xmeye" && <NumField label={t("DVRIP port")} min={1} max={65535} value={c.dvrip_port} onChange={(n) => setCam(i, { dvrip_port: n })} />}
                    <NumField label={t("ONVIF port (events)")} min={0} max={65535} value={c.onvif_port} onChange={(n) => setCam(i, { onvif_port: n })} hint={t("0 = brand default.")} />
                    <label className="field">
                      <span>{t("Motion detection")}</span>
                      <select className="input" value={c.motion || "auto"} onChange={(e) => setCam(i, { motion: e.target.value })}>
                        <option value="auto">{t("Automatic (the camera, else the video)")}</option>
                        <option value="camera">{t("From the camera (ONVIF events)")}</option>
                        <option value="video">{t("From the video")}</option>
                      </select>
                      <span className="muted small">{t("Cameras that send no motion events (RTSP only) get it from the video.")}</span>
                    </label>
                    <NumField label={t("Keep for (days)")} min={0} value={c.retention_days} onChange={(n) => setCam(i, { retention_days: n })} hint={t("0 = until the disk is full.")} />
                  </div>
                  <div className="settings-checks">
                    <Check label={t("Record")} checked={c.record} onChange={(v) => setCam(i, { record: v })} />
                    <Check label={t("Record audio")} checked={c.record_audio} onChange={(v) => setCam(i, { record_audio: v })} />
                    <Check label={t("Instant live (keeps the connection open)")} checked={c.preload} onChange={(v) => setCam(i, { preload: v })} />
                    {c.vendor === "xmeye" && <Check label={t("Has PTZ (moves and zooms)")} checked={c.ptz} onChange={(v) => setCam(i, { ptz: v })} />}
                    {c.vendor === "xmeye" && c.ptz && <Check label={t("Camera upside down (inverts PTZ)")} checked={c.invert_pan_tilt} onChange={(v) => setCam(i, { invert_pan_tilt: v })} />}
                    {c.vendor === "xmeye" && <Check label={t("Has a white light")} checked={c.light} onChange={(v) => setCam(i, { light: v })} />}
                    <Check label={c.vendor === "xmeye" ? t("Has a speaker (hold to talk)") : t("Has a speaker (hold to talk, over the camera's ONVIF/RTSP two-way audio)")} checked={c.talk} onChange={(v) => setCam(i, { talk: v })} />
                  </div>
                  <div className="settings-grid">
                    <TextField label={t("Camera username")} value={c.user ?? ""} onChange={(v) => setCam(i, { user: v })} placeholder={isNew ? "admin" : t("leave blank to keep")} />
                    <TextField
                      label={t("Camera password")}
                      type="password"
                      autoComplete="new-password"
                      value={c.password ?? ""}
                      onChange={(v) => setCam(i, { password: v })}
                      placeholder={isNew ? "" : t("leave blank to keep")}
                      hint={t("Stays only on the server (secrets.env); never goes back to the browser.")}
                    />
                  </div>
                  <div className="onvif-discover">
                    <button
                      className="btn small"
                      disabled={discover.isPending || !c.host || !c.user || !c.password}
                      onClick={() => fromOnvif(i)}
                      title={!c.password ? t("Enter the camera's username and password above") : undefined}
                    >
                      <Icon name="scan" /> {discover.isPending ? t("Searching…") : t("Find the streams over ONVIF")}
                    </button>
                    <span className="muted small">
                      {t("Works with most IP cameras (ONVIF): fills in the RTSP paths by itself. Use the address, the ONVIF port (usually 80) and the camera's username and password.")}
                    </span>
                    {found[c.id] && (
                      <ul className="onvif-found">
                        {found[c.id].map((s, k) => (
                          <li key={s.token}>
                            <strong>{k === 0 ? t("Main") : k === 1 ? t("Lightweight") : s.name}</strong> {s.encoding} {s.width}×{s.height}{" "}
                            <code>
                              :{s.port}
                              {s.path}
                            </code>
                          </li>
                        ))}
                      </ul>
                    )}
                  </div>
                  <button className="btn ghost small danger" onClick={() => setRemove(c.id)}>
                    <Icon name="trash" />{" "}{t("Remove camera")}
                  </button>
                </div>
              )}
            </li>
          );
        })}
      </ul>
      <button className="btn ghost small" onClick={add}>
        <Icon name="plus" />{" "}{t("Add camera")}
      </button>
      {remove && (
        <div className="dialog-backdrop" role="dialog" aria-modal="true" onClick={(e) => e.target === e.currentTarget && setRemove(null)}>
          <div className="dialog">
            <h2>{t("Remove the camera")}{" "}{remove}?</h2>
            <p className="muted">{t("It stops recording and disappears from live view when you save. Recordings already made stay on disk until normal cleanup.")}</p>
            <div className="dialog-actions">
              <button className="btn ghost" onClick={() => setRemove(null)}>
                {t("Cancel")}
              </button>
              <button
                className="btn primary danger-fill"
                onClick={() => {
                  setDraft({ ...draft, cameras: draft.cameras.filter((c) => c.id !== remove) });
                  setRemove(null);
                }}
              >
                {t("Remove")}
              </button>
            </div>
          </div>
        </div>
      )}
    </EditorCard>
  );
}

// ---------- server and disks ----------

const kinds: Record<string, string> = { recordings: t("Recordings"), nas: t("Cloud"), backup: "Backups" };

function timezones(current: string): string[] {
  const list = typeof Intl.supportedValuesOf === "function" ? Intl.supportedValuesOf("timeZone") : [];
  return list.includes(current) ? list : [current, ...list];
}

export function ServerEditor() {
  const q = useSettings();
  const apply = useApply();
  const { draft, setDraft, dirty, reset } = useDraft(q.data);
  if (!draft) return <EditorUnavailable q={q} />;
  const setVol = (i: number, patch: Partial<SettingsVolume>) => setDraft({ ...draft, volumes: draft.volumes.map((v, j) => (j === i ? { ...v, ...patch } : v)) });
  return (
    <EditorCard title={t("Server and disk settings")} hint={t("Time zone, network names and disks shown")} info={q.data} dirty={dirty} onSave={() => apply.mutate(draft)} onReset={reset} busy={apply.isPending}>
      <div className="settings-grid">
        <label className="field">
          <span>{t("Time zone")}</span>
          <select className="input" value={draft.timezone} onChange={(e) => setDraft({ ...draft, timezone: e.target.value })}>
            {timezones(draft.timezone).map((z) => (
              <option key={z} value={z}>
                {z}
              </option>
            ))}
          </select>
        </label>
        <TextField label={t("Server name on the network (Cloud)")} value={draft.nas_host} onChange={(v) => setDraft({ ...draft, nas_host: v })} placeholder="SENTINELA" />
        <TextField
          label={t("Tailscale address")}
          value={draft.tailnet_url}
          onChange={(v) => setDraft({ ...draft, tailnet_url: v.trim() })}
          placeholder="https://sentinela.example.ts.net"
          hint={t("Offered to the app in the pairing code.")}
        />
      </div>
      <h3 className="settings-sub">{t("Disks")}</h3>
      <p className="muted small">
        {t("The disks are mounted on the server (see the installation guide); here you choose how they appear in the panel and in Cloud. The path is the one inside the container (")}<code>/data</code>, <code>/srv/nas</code>, <code>/srv/backup</code>).
      </p>
      <ul className="settings-list">
        {draft.volumes.map((v, i) => (
          <li key={i} className="open">
            <div className="settings-body settings-grid">
              <TextField label={t("Name")} value={v.name} onChange={(x) => setVol(i, { name: x })} />
              <TextField label={t("Path")} value={v.path} onChange={(x) => setVol(i, { path: x.trim() })} />
              <label className="field">
                <span>{t("Usage")}</span>
                <select className="input" value={v.kind} onChange={(e) => setVol(i, { kind: e.target.value })}>
                  {Object.entries(kinds).map(([k, l]) => (
                    <option key={k} value={k}>
                      {l}
                    </option>
                  ))}
                </select>
              </label>
              <TextField label={t("Network folder (SMB)")} value={v.share} onChange={(x) => setVol(i, { share: x })} placeholder={t("optional")} />
              <button className="btn ghost small danger" onClick={() => setDraft({ ...draft, volumes: draft.volumes.filter((_, j) => j !== i) })}>
                <Icon name="trash" />{" "}{t("Remove from the list")}
              </button>
            </div>
          </li>
        ))}
      </ul>
      <button
        className="btn ghost small"
        onClick={() => setDraft({ ...draft, volumes: [...draft.volumes, { id: `vol${draft.volumes.length + 1}`, name: "", path: "/srv/", kind: "nas", share: "" }] })}
      >
        <Icon name="plus" />{" "}{t("Add disk")}
      </button>
    </EditorCard>
  );
}
