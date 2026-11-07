import { useCallback, useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, get, type ControlState, type DayNight, type Preset, type TourState } from "../lib/api";
import { useToast } from "../lib/session";
import { dayNightLabel } from "../lib/time";
import { Icon } from "./Icon";
import { LighthouseMark } from "./Lighthouse";
import { haptic } from "../lib/gestures";
import { t } from "../lib/i18n";

type Op = "move" | "zoom" | "focus";

/** Press-and-hold: start, repeat every 400 ms (server deadman is 1 s), stop on release. */
function useHold(cameraId: string, speed: number, onError: (m: string) => void) {
  const timer = useRef<number | undefined>(undefined);
  const active = useRef<string | null>(null);
  const [pressed, setPressed] = useState<string | null>(null);

  const send = useCallback(
    (body: object) => api("POST", `/api/cameras/${cameraId}/ptz`, body),
    [cameraId],
  );

  const stop = useCallback(() => {
    window.clearInterval(timer.current);
    if (active.current) {
      active.current = null;
      setPressed(null);
      send({ action: "stop" }).catch(() => {});
    }
  }, [send]);

  const start = useCallback(
    (op: Op, dir: string) => {
      stop();
      haptic();
      const key = `${op}:${dir}`;
      active.current = key;
      setPressed(key);
      const body = { action: "start", op, dir, speed };
      send(body).catch((e: Error) => {
        onError(e.message);
        stop();
      });
      timer.current = window.setInterval(() => {
        if (active.current === key) send(body).catch(() => {});
      }, 400);
    },
    [send, speed, stop, onError],
  );

  useEffect(() => {
    window.addEventListener("blur", stop);
    return () => {
      window.removeEventListener("blur", stop);
      stop();
    };
  }, [stop]);

  const bind = (op: Op, dir: string) => ({
    onPointerDown: (e: React.PointerEvent) => {
      e.preventDefault();
      (e.target as Element).setPointerCapture?.(e.pointerId);
      start(op, dir);
    },
    onPointerUp: stop,
    onPointerCancel: stop,
    onLostPointerCapture: stop,
    onKeyDown: (e: React.KeyboardEvent) => {
      if ((e.key === "Enter" || e.key === " ") && !e.repeat) start(op, dir);
    },
    onKeyUp: (e: React.KeyboardEvent) => {
      if (e.key === "Enter" || e.key === " ") stop();
    },
    "data-active": pressed === `${op}:${dir}` ? "true" : "false",
  });
  return { bind, start, stop, pressed };
}

const DIRS: { dir: string; label: string; angle: number }[] = [
  { dir: "up", label: t("Up"), angle: -90 },
  { dir: "up_right", label: t("Up and right"), angle: -45 },
  { dir: "right", label: t("Right"), angle: 0 },
  { dir: "down_right", label: t("Down and right"), angle: 45 },
  { dir: "down", label: t("Down"), angle: 90 },
  { dir: "down_left", label: t("Down and left"), angle: 135 },
  { dir: "left", label: t("Left"), angle: 180 },
  { dir: "up_left", label: t("Up and left"), angle: -135 },
];

// Keyboard: arrows (two at once = diagonals), + / − zoom, [ ] focus.
const KEY_DIR: Record<string, [number, number]> = { ArrowUp: [0, -1], ArrowDown: [0, 1], ArrowLeft: [-1, 0], ArrowRight: [1, 0] };
const LENS_KEY: Record<string, [Op, string]> = { "+": ["zoom", "in"], "=": ["zoom", "in"], "-": ["zoom", "out"], "]": ["focus", "far"], "[": ["focus", "near"] };

/** Direction for the arrow keys held right now (null = none). */
export function keyDir(held: Iterable<string>): string | null {
  let x = 0;
  let y = 0;
  for (const k of held) {
    const d = KEY_DIR[k];
    if (d) {
      x += d[0];
      y += d[1];
    }
  }
  const v = y < 0 ? "up" : y > 0 ? "down" : "";
  const h = x < 0 ? "left" : x > 0 ? "right" : "";
  return v && h ? `${v}_${h}` : v || h || null;
}

function speedLabel(v: number) {
  return v <= 2 ? t("Slow ({v})", { v }) : v >= 7 ? t("Fast ({v})", { v }) : t("Normal ({v})", { v });
}

export function PtzPanel({ cameraId, canControl }: { cameraId: string; canControl: boolean }) {
  const toast = useToast();
  const [speed, setSpeed] = useState(4);
  const onError = useCallback((m: string) => toast(t("PTZ: {m}", { m }), true), [toast]);
  const { bind, start, stop, pressed } = useHold(cameraId, speed, onError);
  const moving = pressed?.startsWith("move:") ? DIRS.find((d) => `move:${d.dir}` === pressed) : undefined;

  useEffect(() => {
    if (!canControl) return;
    const held = new Set<string>();
    let lens: string | null = null;
    const typing = (e: KeyboardEvent) =>
      !!(e.target as HTMLElement).closest("input, select, textarea, [contenteditable], dialog, .dialog") || e.ctrlKey || e.metaKey || e.altKey;
    const down = (e: KeyboardEvent) => {
      if (typing(e)) return;
      if (KEY_DIR[e.key]) {
        e.preventDefault();
        if (e.repeat) return;
        held.add(e.key);
        const d = keyDir(held);
        if (d) start("move", d);
      } else if (LENS_KEY[e.key] && !e.repeat) {
        lens = e.key;
        start(...LENS_KEY[e.key]);
      }
    };
    const up = (e: KeyboardEvent) => {
      if (KEY_DIR[e.key]) {
        held.delete(e.key);
        const d = keyDir(held);
        if (d) start("move", d);
        else stop();
      } else if (e.key === lens) {
        lens = null;
        stop();
      }
    };
    const clear = () => held.clear();
    window.addEventListener("keydown", down);
    window.addEventListener("keyup", up);
    window.addEventListener("blur", clear);
    return () => {
      window.removeEventListener("keydown", down);
      window.removeEventListener("keyup", up);
      window.removeEventListener("blur", clear);
    };
  }, [canControl, start, stop]);

  return (
    <section className="card ptz" aria-label={t("Motion control")}>
      <div className="card-head">
        <h2>{t("Motion")}</h2>
        {canControl && (
          <span className="ptz-keys muted small" title={t("Arrow keys move (two together = diagonal). + and − zoom, [ and ] adjust focus.")}>
            <Icon name="keyboard" />{" "}<span className="hide-sm">{t("Arrows, + and −")}</span>
          </span>
        )}
      </div>
      <div className={`dial${moving ? " moving" : ""}`} role="group" aria-label={t("Directions")} style={moving ? ({ "--dir": `${moving.angle}deg` } as React.CSSProperties) : undefined}>
        {DIRS.map(({ dir, label, angle }) => {
          const rad = (angle * Math.PI) / 180;
          return (
            <button
              key={dir}
              style={{ left: `${50 + 36 * Math.cos(rad)}%`, top: `${50 + 36 * Math.sin(rad)}%` }}
              aria-label={label}
              title={label}
              disabled={!canControl}
              {...bind("move", dir)}
            >
              <Icon name={dir} />
            </button>
          );
        })}
        <div className="hub" aria-hidden="true">
          <LighthouseMark />
        </div>
      </div>

      <label className="speed">
        <span className="muted">{t("Speed")}</span>
        <input type="range" min={1} max={8} value={speed} onChange={(e) => setSpeed(Number(e.target.value))} disabled={!canControl} aria-valuetext={speedLabel(speed)} />
        <span className="speed-val">{speedLabel(speed)}</span>
      </label>

      <div className="ptz-lens">
        <div className="ptz-row">
          <span className="label">{t("Zoom")}</span>
          <button className="btn icon small" aria-label={t("Zoom out")} title={t("Zoom out (−)")} disabled={!canControl} {...bind("zoom", "out")}>
            <Icon name="minus" />
          </button>
          <button className="btn icon small" aria-label={t("Zoom in")} title={t("Zoom in (+)")} disabled={!canControl} {...bind("zoom", "in")}>
            <Icon name="plus" />
          </button>
        </div>
        <div className="ptz-row">
          <span className="label">{t("Focus")}</span>
          <button className="btn icon small" aria-label={t("Focus near")} title={t("Focus near ([)")} disabled={!canControl} {...bind("focus", "near")}>
            <Icon name="minus" />
          </button>
          <button className="btn icon small" aria-label={t("Focus far")} title={t("Focus far (])")} disabled={!canControl} {...bind("focus", "far")}>
            <Icon name="plus" />
          </button>
        </div>
      </div>
      {!canControl && <p className="muted small">{t("Your profile only lets you watch.")}</p>}
    </section>
  );
}

/** Saved positions (cameras with PTZ) and image settings; ptz/light come from the camera's features. */
export function CameraSettings({
  cameraId,
  canControl,
  isAdmin,
  ptz = true,
  light: hasLight = true,
}: {
  cameraId: string;
  canControl: boolean;
  isAdmin: boolean;
  ptz?: boolean;
  light?: boolean;
}) {
  const toast = useToast();
  const qc = useQueryClient();
  const key = ["control", cameraId];
  const state = useQuery({
    queryKey: key,
    queryFn: () => get<ControlState>(`/api/cameras/${cameraId}/control`),
    // Each read is a DVRIP login on the camera: not on every window focus.
    staleTime: 60_000,
    refetchOnWindowFocus: false,
    retry: 1,
  });
  const [confirmSave, setConfirmSave] = useState<number | null>(null);
  const [confirmClear, setConfirmClear] = useState<number | null>(null);
  const [editing, setEditing] = useState(false);

  const light = useMutation({
    mutationFn: (on: boolean) => api("PUT", `/api/cameras/${cameraId}/light`, { on }),
    onSuccess: (_, on) => {
      toast(on ? t("White light on") : t("White light off"));
      void qc.invalidateQueries({ queryKey: key });
    },
    onError: (e: Error) => toast(t("Light: {message}", { message: e.message }), true),
  });
  const dayNight = useMutation({
    mutationFn: (mode: DayNight) => api("PUT", `/api/cameras/${cameraId}/daynight`, { mode }),
    onSuccess: (_, mode) => {
      toast(t("Picture mode: {v0}", { v0: dayNightLabel[mode] }));
      void qc.invalidateQueries({ queryKey: key });
    },
    onError: (e: Error) => toast(t("Picture mode: {message}", { message: e.message }), true),
  });
  const preset = useMutation({
    mutationFn: ({ id, action }: { id: number; action: "goto" | "set" | "clear" }) =>
      api("POST", `/api/cameras/${cameraId}/presets/${id}`, { action }),
    onSuccess: (_, v) => {
      if (v.action === "set") toast(t("Position {id} saved", { id: v.id }));
      if (v.action === "clear") toast(t("Position {id} deleted", { id: v.id }));
      if (v.action !== "goto") void qc.invalidateQueries({ queryKey: key });
    },
    onError: (e: Error) => toast(t("Position: {message}", { message: e.message }), true),
  });

  const st = state.data;
  const busy = light.isPending || dayNight.isPending;
  const presets = st?.presets ?? [];
  const nextFree = (() => {
    const used = new Set(presets.map((p) => p.id));
    for (let i = 1; i <= 255; i++) if (!used.has(i)) return i;
    return null;
  })();

  return (
    <>
      {ptz && (
      <section className="card" aria-label={t("Saved positions")}>
        <div className="card-head">
          <h2>{t("Saved positions")}</h2>
          {canControl && isAdmin && presets.length > 0 && (
            <button className={`btn small ghost${editing ? " on" : ""}`} aria-pressed={editing} onClick={() => setEditing((v) => !v)}>
              {editing ? t("Finish") : t("Edit")}
            </button>
          )}
        </div>
        {state.isLoading && <p className="muted">{t("Reading the camera…")}</p>}
        {state.isError && <p className="notice error">{t("Camera did not respond.")} {(state.error as Error).message}</p>}
        {st && presets.length === 0 && <p className="muted">{t("No position saved yet.")}</p>}
        <div className={`presets${editing ? " editing" : ""}`}>
          {presets.map((p) => {
            // The firmware names untouched presets "Preset N"; show the number then.
            const named = p.name && !/^preset\s*\d+$/i.test(p.name.trim());
            return editing ? (
              <button
                key={p.id}
                className="chip remove"
                disabled={preset.isPending}
                onClick={() => setConfirmClear(p.id)}
                title={t("Delete position {id}", { id: p.id })}
                aria-label={t("Delete position {id}", { id: p.id })}
              >
                <span className="num">{p.id}</span>
                {named && <span className="chip-name">{p.name}</span>}
                <Icon name="x" />
              </button>
            ) : (
              <button
                key={p.id}
                className={`chip${named ? " named" : ""}`}
                disabled={!canControl || preset.isPending}
                onClick={() => {
                  haptic();
                  preset.mutate({ id: p.id, action: "goto" });
                }}
                title={t("Go to position {id}{v1}", {
                    id: p.id,
                    v1: named ? `: ${p.name}` : "",
                  })}
              >
                <span className="num">{p.id}</span>
                {named && <span className="chip-name">{p.name}</span>}
              </button>
            );
          })}
        </div>
        {canControl && isAdmin && nextFree !== null && !editing && (
          <button className="btn ghost small" style={{ marginTop: 10 }} onClick={() => setConfirmSave(nextFree)}>
            <Icon name="plus" /> {t("Save current position")}
          </button>
        )}
        {presets.length >= 2 && !editing && <PresetTour cameraId={cameraId} presets={presets} canControl={canControl} />}
      </section>
      )}

      {confirmClear !== null && (
        <div className="dialog-backdrop" role="dialog" aria-modal="true" aria-labelledby="clear-title" onClick={(e) => e.target === e.currentTarget && setConfirmClear(null)}>
          <div className="dialog">
            <h2 id="clear-title">{t("Delete the position")} {confirmClear}?</h2>
            <p className="muted">{t("The camera forgets this position. The camera does not move.")}</p>
            <div className="dialog-actions">
              <button className="btn ghost" onClick={() => setConfirmClear(null)}>
                {t("Cancel")}
              </button>
              <button
                className="btn primary danger-fill"
                onClick={() => {
                  preset.mutate({ id: confirmClear, action: "clear" });
                  setConfirmClear(null);
                }}
              >
                {t("Delete position")}
              </button>
            </div>
          </div>
        </div>
      )}

      <section className="card" aria-label={t("Image and light")}>
        <h2>{t("Image")}</h2>
        {hasLight && (
        <div className="toggle-row">
          <span>
            <Icon name="light" /> {t("White light")}
          </span>
          <button
            className="switch"
            role="switch"
            aria-checked={!!st?.white_light}
            aria-label={t("White light")}
            disabled={!canControl || !st || busy}
            onClick={() => st && light.mutate(!st.white_light)}
          />
        </div>
        )}
        <div className="field" style={{ marginTop: 8 }}>
          <label htmlFor={`dn-${cameraId}`}>{t("Day and night")}</label>
          <select
            id={`dn-${cameraId}`}
            className="input"
            value={st?.day_night || ""}
            disabled={!canControl || !st || busy}
            onChange={(e) => dayNight.mutate(e.target.value as DayNight)}
          >
            {!st?.day_night && <option value="">—</option>}
            {Object.entries(dayNightLabel).map(([v, l]) => (
              <option key={v} value={v}>
                {l}
              </option>
            ))}
          </select>
        </div>
        {st && (
          <p className="faint" style={{ fontSize: "0.82rem", marginTop: 10 }}>
            {st.model}
          </p>
        )}
      </section>

      {confirmSave !== null && (
        <div className="dialog-backdrop" role="dialog" aria-modal="true" aria-labelledby="save-title">
          <div className="dialog">
            <h2 id="save-title">{t("Save position")} {confirmSave}?</h2>
            <p className="muted">{t("The camera's current position is saved in the camera as position")} {confirmSave}.</p>
            <div className="dialog-actions">
              <button className="btn ghost" onClick={() => setConfirmSave(null)}>
                {t("Cancel")}
              </button>
              <button
                className="btn primary"
                onClick={() => {
                  preset.mutate({ id: confirmSave, action: "set" });
                  setConfirmSave(null);
                }}
              >
                {t("Save position")}
              </button>
            </div>
          </div>
        </div>
      )}
    </>
  );
}

const tourDwells = [10, 20, 30, 60, 120];
const tourHours = [0.5, 1, 4, 12];
const tourHourLabel = (h: number) => (h < 1 ? `${h * 60} min` : `${h} h`);

/** Preset tour: the camera visits the chosen positions in turn until stopped,
 *  until its time is up, or until someone moves it by hand. */
function PresetTour({ cameraId, presets, canControl }: { cameraId: string; presets: Preset[]; canControl: boolean }) {
  const toast = useToast();
  const qc = useQueryClient();
  const key = ["tour", cameraId];
  const tour = useQuery({
    queryKey: key,
    queryFn: () => get<TourState>(`/api/cameras/${cameraId}/tour`),
    refetchInterval: (q) => (q.state.data?.running ? 5_000 : false),
  });
  const [picked, setPicked] = useState<number[]>([]);
  const [dwell, setDwell] = useState(20);
  const [hours, setHours] = useState(1);
  const chosen = picked.length ? picked : presets.map((p) => p.id);
  const start = useMutation({
    mutationFn: () => api("PUT", `/api/cameras/${cameraId}/tour`, { presets: chosen, dwell_seconds: dwell, hours }),
    onSuccess: () => {
      toast(t("Tour started"));
      void qc.invalidateQueries({ queryKey: key });
    },
    onError: (e: Error) => toast(t("Tour: {message}", { message: e.message }), true),
  });
  const stop = useMutation({
    mutationFn: () => api("DELETE", `/api/cameras/${cameraId}/tour`),
    onSuccess: () => {
      toast(t("Tour stopped"));
      void qc.invalidateQueries({ queryKey: key });
    },
    onError: (e: Error) => toast(t("Tour: {message}", { message: e.message }), true),
  });
  const st = tour.data;
  const toggle = (id: number) =>
    setPicked((cur) => {
      const base = cur.length ? cur : presets.map((p) => p.id);
      return base.includes(id) ? base.filter((x) => x !== id) : [...base, id];
    });

  if (st?.running) {
    const ends = st.ends_at ? new Date(st.ends_at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) : "";
    return (
      <div className="tour" aria-live="polite">
        <p>
          <strong>{t("Tour active")}</strong>
          {t(": positions")} {st.presets?.join(" → ")}, {st.dwell_seconds}{" "}
          {t("s each")}
          {st.current ? t("; now at {current}", { current: st.current }) : ""}
          {ends ? t("; ends at {ends}", { ends }) : ""}.
        </p>
        {st.last_error && <p className="muted small">{t("Last error:")} {st.last_error}</p>}
        <button className="btn small" disabled={!canControl || stop.isPending} onClick={() => stop.mutate()}>
          <Icon name="x" /> {t("Stop tour")}
        </button>
      </div>
    );
  }
  if (!canControl) return null;
  return (
    <div className="tour">
      <h3>{t("Tour")}</h3>
      <p className="muted small">{t("The camera visits the marked positions, in order. Moving it by hand ends the tour.")}</p>
      <div className="presets" role="group" aria-label={t("Tour positions")}>
        {presets.map((p) => (
          <button
            key={p.id}
            className={`chip${chosen.includes(p.id) ? " on" : ""}`}
            aria-pressed={chosen.includes(p.id)}
            onClick={() => toggle(p.id)}
          >
            <span className="num">{p.id}</span>
          </button>
        ))}
      </div>
      <div className="row-gap" style={{ marginTop: 8 }}>
        <label className="field">
          <span>{t("Time in each")}</span>
          <select className="input" value={dwell} onChange={(e) => setDwell(Number(e.target.value))}>
            {tourDwells.map((d) => (
              <option key={d} value={d}>
                {d < 60 ? `${d} s` : `${d / 60} min`}
              </option>
            ))}
          </select>
        </label>
        <label className="field">
          <span>{t("Duration")}</span>
          <select className="input" value={hours} onChange={(e) => setHours(Number(e.target.value))}>
            {tourHours.map((h) => (
              <option key={h} value={h}>
                {tourHourLabel(h)}
              </option>
            ))}
          </select>
        </label>
      </div>
      <button
        className="btn small"
        style={{ marginTop: 10 }}
        disabled={chosen.length < 2 || start.isPending}
        onClick={() => start.mutate()}
      >
        {t("Start tour")}
      </button>
      {st && !st.running && st.stop_reason && <p className="muted small">{t("Last tour:")} {tourReason(st.stop_reason)}.</p>}
    </div>
  );
}

function tourReason(r: string): string {
  if (r === "time is up") return t("finished on time");
  if (r === "the camera did not answer") return t("the camera did not respond");
  if (r.startsWith("manual control by ")) return t("ended by the manual control of {v0}", { v0: r.slice(18) });
  if (r.startsWith("stopped by ")) return t("stopped by {name}", { name: r.slice(11) });
  if (r === "server stopping") return t("interrupted when the server restarted");
  return t("stopped");
}
