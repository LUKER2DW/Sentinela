import { useMemo, useRef, useState, type ReactNode } from "react";
import type { Timeline } from "../lib/api";
import { DAY_MS, fmtHM, fmtTime, mergeRanges } from "../lib/time";
import { Moon, Sun } from "./Doodles";

import { t, plural } from "../lib/i18n";
interface Props {
  /** Local midnight of the day shown. */
  dayStart: Date;
  data?: Timeline;
  /** Selected instant (ms) or null. */
  cursor?: number | null;
  now: number;
  onPick?: (ms: number) => void;
  label: string;
  /** The detailed ruler's visible window, outlined on the ribbon. */
  view?: [number, number] | null;
  /** The picture at the hovered (mouse) or held (finger) time. */
  preview?: (ms: number) => ReactNode;
}

export function DayRibbon({ dayStart, data, cursor, now, onPick, label, view, preview }: Props) {
  const ref = useRef<HTMLDivElement>(null);
  const [hover, setHover] = useState<number | null>(null);
  const held = useRef(false);
  const tapped = useRef(0); // a finger's lift already picked; skip its click
  const t0 = dayStart.getTime();
  const t1 = t0 + DAY_MS;
  const pct = (ms: number) => `${(((ms - t0) / DAY_MS) * 100).toFixed(3)}%`;
  const width = (a: number, b: number) => `${(((b - a) / DAY_MS) * 100).toFixed(3)}%`;

  const coverage = useMemo(() => {
    const ranges = (data?.segments ?? []).map((s) => [new Date(s.start).getTime(), new Date(s.end).getTime()] as [number, number]);
    return mergeRanges(ranges)
      .map(([a, b]) => [Math.max(a, t0), Math.min(b, t1)] as [number, number])
      .filter(([a, b]) => b > a);
  }, [data, t0, t1]);

  const events = useMemo(
    () =>
      (data?.events ?? [])
        .map((e) => {
          const a = new Date(e.start).getTime();
          const b = e.end ? new Date(e.end).getTime() : Math.min(now, a + 60_000);
          return { id: e.id, a: Math.max(a, t0), b: Math.min(Math.max(b, a + 20_000), t1) };
        })
        .filter((e) => e.b > t0 && e.a < t1),
    [data, t0, t1, now],
  );

  // Events started per hour: the activity strip under the ribbon.
  const hours = useMemo(() => {
    const out = Array.from({ length: 24 }, () => ({ n: 0, first: 0 }));
    for (const e of data?.events ?? []) {
      const a = new Date(e.start).getTime();
      if (a < t0 || a >= t1) continue;
      const h = new Date(a).getHours();
      if (!out[h].n || a < out[h].first) out[h].first = a;
      out[h].n++;
    }
    return out;
  }, [data, t0, t1]);
  const busiest = Math.max(1, ...hours.map((h) => h.n));

  const toMs = (clientX: number) => {
    const r = ref.current!.getBoundingClientRect();
    const x = Math.min(Math.max(clientX - r.left, 0), r.width);
    return t0 + (x / r.width) * DAY_MS;
  };

  const showNow = now > t0 && now < t1;
  const limit = showNow ? now : t1;
  const pick = (ms: number) => onPick?.(Math.min(Math.max(ms, t0), limit - 1000));
  const current = cursor ?? (showNow ? now : t0);

  return (
    <div className="ribbon-wrap">
      {hover !== null && preview && hover < limit && (
        <div className="tl-preview-at" style={{ left: `clamp(92px, ${pct(hover)}, calc(100% - 92px))` }}>
          {preview(hover)}
        </div>
      )}
      <div
        ref={ref}
        className="ribbon"
        role="slider"
        tabIndex={0}
        aria-label={label}
        aria-valuemin={t0}
        aria-valuemax={limit}
        aria-valuenow={current}
        aria-valuetext={fmtTime(current)}
        onPointerDown={(e) => {
          if (e.pointerType === "mouse" || !preview) return;
          // A finger: hold to see the picture, slide to look around, lift to play.
          held.current = true;
          setHover(toMs(e.clientX));
        }}
        onPointerMove={(e) => setHover(e.pointerType === "mouse" || !preview || held.current ? toMs(e.clientX) : null)}
        onPointerUp={(e) => {
          if (!held.current) return;
          held.current = false;
          setHover(null);
          tapped.current = Date.now();
          pick(toMs(e.clientX));
        }}
        // The page took the gesture over (a vertical scroll): nothing picked.
        onPointerCancel={() => {
          held.current = false;
          setHover(null);
        }}
        onPointerLeave={() => !held.current && setHover(null)}
        onClick={(e) => Date.now() - tapped.current > 600 && pick(toMs(e.clientX))}
        onKeyDown={(e) => {
          const step = { ArrowLeft: -60_000, ArrowRight: 60_000, PageDown: -600_000, PageUp: 600_000 }[e.key];
          if (step) {
            e.preventDefault();
            pick(current + step);
          }
        }}
      >
        {/* The sky gradient itself comes from the theme (CSS --sky-*). */}
        <Moon className="sky-doodle" style={{ left: "4%" }} />
        <Sun className="sky-doodle" style={{ left: "50%" }} />
        <Moon className="sky-doodle" style={{ left: "96%" }} />
        <div className="gap-hatch" style={{ width: pct(limit) }} />
        {coverage.map(([a, b]) => (
          <div key={a} className="cov" style={{ left: pct(a), width: width(a, b) }} />
        ))}
        {events.map((e) => (
          <div key={e.id} className="ev" style={{ left: pct(e.a), width: width(e.a, e.b) }} />
        ))}
        {showNow && <div className="now" style={{ left: pct(now) }} />}
        {view && <div className="view-window" style={{ left: pct(view[0]), width: width(view[0], view[1]) }} />}
        {hover !== null && <div className="hover-line" style={{ left: pct(hover) }} />}
        {cursor != null && cursor >= t0 && cursor < t1 && (
          <>
            <div className="cursor" style={{ left: pct(cursor) }} />
            <div className="cursor-label" style={{ left: `clamp(28px, ${pct(cursor)}, calc(100% - 28px))` }}>
              {fmtHM(cursor)}
            </div>
          </>
        )}
        {hover !== null && cursor == null && (
          <div className="cursor-label" style={{ left: `clamp(28px, ${pct(hover)}, calc(100% - 28px))` }}>
            {fmtHM(hover)}
          </div>
        )}
      </div>
      {hours.some((h) => h.n) && (
        <div className="activity" role="group" aria-label={t("Activity per hour")}>
          {hours.map((h, i) => (
            <button
              key={i}
              disabled={!h.n}
              title={h.n ? `${String(i).padStart(2, "0")}h: ${plural(h.n, "{n} event", "{n} events")}` : undefined}
              aria-label={h.n ? t("{i} hours, {n} events", { i, n: h.n }) : t("{i} hours, nothing", { i })}
              onClick={() => h.n && pick(h.first - 5000)}
            >
              {/* sqrt keeps a single event visible next to a busy hour */}
              <i style={{ height: h.n ? `${Math.max(18, Math.sqrt(h.n / busiest) * 100)}%` : 0 }} />
            </button>
          ))}
        </div>
      )}
      <div className="ribbon-ticks" aria-hidden="true" />
      <div className="ribbon-scale" aria-hidden="true">
        {[0, 3, 6, 9, 12, 15, 18, 21, 24].map((h) => (
          <span key={h} className={h % 6 ? "minor" : undefined} style={{ left: `clamp(8px, ${(h / 24) * 100}%, calc(100% - 10px))` }}>
            {String(h).padStart(2, "0")}h
          </span>
        ))}
      </div>
    </div>
  );
}

export function RibbonLegend() {
  return (
    <div className="ribbon-legend">
      <span>
        <i style={{ background: "var(--sea)" }} />
        {t("Recorded")}
      </span>
      <span>
        <i style={{ background: "repeating-linear-gradient(135deg, var(--line-strong) 0 2px, transparent 2px 5px)" }} />
        {t("No recording")}
      </span>
      <span>
        <i style={{ background: "var(--coral)" }} />
        {t("Motion")}
      </span>
    </div>
  );
}
