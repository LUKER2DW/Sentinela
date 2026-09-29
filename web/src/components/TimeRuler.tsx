import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type { Timeline } from "../lib/api";
import { DAY_MS, fmtHM, fmtTime, mergeRanges } from "../lib/time";
import { haptic } from "../lib/gestures";
import { Icon } from "./Icon";

// Pixels per hour at each zoom step: a whole day on a wide screen down to
// a tick every minute.
const SCALES = [60, 120, 240, 480, 960];
const SCALE_KEY = "sentinela.rulerScale";
const HOUR = 3600_000;

interface Props {
  dayStart: Date;
  data?: Timeline;
  /** Where playback is now (ms), or the picked time before it starts. */
  cursor: number | null;
  now: number;
  onPick: (ms: number) => void;
  /** Visible window, for the overview ribbon above. */
  onView?: (from: number, to: number) => void;
}

function savedScale(): number {
  try {
    const v = localStorage.getItem(SCALE_KEY);
    const i = v === null ? -1 : Number(v);
    if (i >= 0 && i < SCALES.length) return i;
  } catch {
    /* no storage */
  }
  return 2;
}

/**
 * The detailed timeline under the day ribbon: a ruler that scrolls sideways
 * under a fixed needle. Drag (or swipe) to a time and let go to play from
 * there; while playing it follows the video. Ticks every 5 min (every
 * minute when zoomed in), labels every hour or half hour.
 */
export function TimeRuler({ dayStart, data, cursor, now, onPick, onView }: Props) {
  const scroller = useRef<HTMLDivElement>(null);
  const [scaleIdx, setScaleIdx] = useState(savedScale);
  const pxPerHour = SCALES[scaleIdx];
  const t0 = dayStart.getTime();
  const t1 = t0 + DAY_MS;
  const limit = Math.min(t1, now) - 1000;
  const x = (ms: number) => ((ms - t0) / HOUR) * pxPerHour;
  const width = 24 * pxPerHour;

  // User is dragging/scrolling: the needle shows the time under it and the
  // ruler stops following playback until they let go.
  const [scrub, setScrub] = useState<number | null>(null);
  const touching = useRef(false);
  const settle = useRef(0);
  const ours = useRef(false); // scroll events we caused ourselves
  const drag = useRef<{ x: number; left: number; moved: boolean } | null>(null);

  const timeAtCenter = () => {
    const el = scroller.current!;
    return t0 + ((el.scrollLeft + el.clientWidth / 2 - el.clientWidth / 2) / pxPerHour) * HOUR;
  };
  const scrollTo = (ms: number, smooth = false) => {
    const el = scroller.current;
    if (!el) return;
    const left = x(ms);
    if (Math.abs(el.scrollLeft - left) < 1) return;
    ours.current = true;
    el.scrollTo({ left, behavior: smooth ? "smooth" : "auto" });
    window.setTimeout(() => (ours.current = false), smooth ? 450 : 60);
  };
  const report = () => {
    const el = scroller.current;
    if (el && onView) {
      const half = (el.clientWidth / 2 / pxPerHour) * HOUR;
      const c = timeAtCenter();
      onView(Math.max(t0, c - half), Math.min(t1, c + half));
    }
  };

  const target = cursor ?? Math.min(now, t1) - HOUR / 2;
  // Follow playback (and keep the place across zoom changes).
  useLayoutEffect(() => {
    if (scrub === null && !touching.current) scrollTo(target);
    report();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [target, pxPerHour, t0]);

  const commit = () => {
    window.clearTimeout(settle.current);
    settle.current = window.setTimeout(() => {
      if (touching.current) return;
      const ms = Math.min(Math.max(timeAtCenter(), t0), limit);
      setScrub(null);
      if (Math.abs(ms - (cursor ?? -1)) > 1500) {
        haptic();
        onPick(ms);
      }
    }, 220);
  };

  const onScroll = () => {
    report();
    if (ours.current) return;
    setScrub(Math.min(Math.max(timeAtCenter(), t0), t1));
    commit();
  };

  const zoom = (d: number) => {
    const i = Math.min(SCALES.length - 1, Math.max(0, scaleIdx + d));
    setScaleIdx(i);
    try {
      localStorage.setItem(SCALE_KEY, String(i));
    } catch {
      /* not remembered */
    }
  };

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
          return { id: e.id, a, b: Math.max(b, a + 15_000) };
        })
        .filter((e) => e.b > t0 && e.a < t1),
    [data, t0, t1, now],
  );

  // Labels: every hour, plus half hours once there is room.
  const labels = useMemo(() => {
    const step = pxPerHour >= 240 ? HOUR / 2 : HOUR;
    const out: number[] = [];
    for (let t = t0; t <= t1; t += step) out.push(t);
    return out;
  }, [t0, t1, pxPerHour]);
  const minute = pxPerHour / 60;
  // Tick spacing: 1 min when zoomed in, else 5 min; mid ticks every 15 min.
  const minor = minute >= 8 ? minute : minute * 5;

  useEffect(() => () => window.clearTimeout(settle.current), []);

  const shown = scrub ?? cursor;
  return (
    <div className="ruler-wrap">
      <div className="ruler-head">
        <span className={`ruler-time${scrub !== null ? " scrubbing" : ""}`} aria-live="polite">
          {shown != null ? fmtTime(shown) : "--:--:--"}
        </span>
        <span className="grow" />
        <div className="segmented small ruler-zoom" role="group" aria-label="Zoom da régua">
          <button onClick={() => zoom(-1)} disabled={scaleIdx === 0} aria-label="Afastar">
            <Icon name="minus" />
          </button>
          <button onClick={() => zoom(1)} disabled={scaleIdx === SCALES.length - 1} aria-label="Aproximar">
            <Icon name="plus" />
          </button>
        </div>
      </div>
      <div className="ruler-frame">
        <div
          ref={scroller}
          className="ruler hscroll"
          onScroll={onScroll}
          onPointerDown={(e) => {
            touching.current = true;
            window.clearTimeout(settle.current);
            if (e.pointerType === "mouse") {
              drag.current = { x: e.clientX, left: scroller.current!.scrollLeft, moved: false };
              e.currentTarget.setPointerCapture(e.pointerId);
            }
          }}
          onPointerMove={(e) => {
            const d = drag.current;
            if (!d) return;
            if (Math.abs(e.clientX - d.x) > 3) d.moved = true;
            scroller.current!.scrollLeft = d.left - (e.clientX - d.x);
          }}
          onPointerUp={(e) => {
            const d = drag.current;
            drag.current = null;
            touching.current = false;
            if (d && !d.moved) {
              // A click picks the time under the pointer.
              const r = scroller.current!.getBoundingClientRect();
              const ms = timeAtCenter() + ((e.clientX - r.left - r.width / 2) / pxPerHour) * HOUR;
              setScrub(Math.min(Math.max(ms, t0), limit));
              scrollTo(ms, true);
              window.setTimeout(() => {
                setScrub(null);
                onPick(Math.min(Math.max(ms, t0), limit));
              }, 300);
              return;
            }
            commit();
          }}
          onPointerCancel={() => {
            drag.current = null;
            touching.current = false;
            commit();
          }}
          onTouchStart={() => {
            touching.current = true;
            window.clearTimeout(settle.current);
          }}
          onTouchEnd={() => {
            touching.current = false;
            commit();
          }}
          onWheel={(e) => {
            if (e.ctrlKey) return;
            if (Math.abs(e.deltaY) > Math.abs(e.deltaX)) {
              scroller.current!.scrollLeft += e.deltaY;
            }
          }}
          role="slider"
          tabIndex={0}
          aria-label="Régua do dia: arraste até um horário e solte para assistir"
          aria-valuemin={t0}
          aria-valuemax={limit}
          aria-valuenow={shown ?? t0}
          aria-valuetext={shown != null ? fmtTime(shown) : "sem horário"}
          onKeyDown={(e) => {
            const step = { ArrowLeft: -60_000, ArrowRight: 60_000, PageDown: -600_000, PageUp: 600_000 }[e.key];
            if (step) {
              e.preventDefault();
              const ms = Math.min(Math.max((cursor ?? timeAtCenter()) + step, t0), limit);
              scrollTo(ms, true);
              onPick(ms);
            }
          }}
        >
          <div
            className="ruler-track"
            style={
              {
                width,
                "--minor": `${minor}px`,
                "--mid": `${minute * 15}px`,
                "--hour": `${pxPerHour}px`,
              } as React.CSSProperties
            }
          >
            <div className="ruler-future" style={{ left: x(Math.min(now, t1)) }} />
            {coverage.map(([a, b]) => (
              <div key={a} className="ruler-cov" style={{ left: x(a), width: Math.max(1, x(b) - x(a)) }} />
            ))}
            {events.map((e) => (
              <div key={e.id} className="ruler-ev" style={{ left: x(e.a), width: Math.max(4, x(e.b) - x(e.a)) }} />
            ))}
            {labels.map((t) => (
              <span key={t} className={`ruler-label${(t - t0) % HOUR ? " half" : ""}`} style={{ left: x(t) }}>
                {fmtHM(t)}
              </span>
            ))}
          </div>
        </div>
        <div className="ruler-needle" aria-hidden="true" />
      </div>
    </div>
  );
}
