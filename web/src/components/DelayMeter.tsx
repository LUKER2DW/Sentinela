import { useEffect, useRef, useState } from "react";
import { get } from "../lib/api";
import { serverNow } from "../lib/time";

import { fmtNum, t } from "../lib/i18n";
// Live-delay check. The camera burns its own clock into the image (top right).
// This overlay sits right under it and shows what that clock should read *now*
// if the picture had no delay: server time (NTP) corrected by the camera's
// clock offset (read over ONVIF). Delay = this reading − the camera's.

const fmt = (ms: number) => {
  const d = new Date(ms);
  const p = (n: number, w = 2) => String(n).padStart(w, "0");
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())},${Math.floor(d.getMilliseconds() / 100)}`;
};

export function DelayMeter({ cameraId, onClose }: { cameraId: string; onClose: () => void }) {
  const [offset, setOffset] = useState<number | null>(null);
  const [failed, setFailed] = useState(false);
  const clockRef = useRef<HTMLSpanElement>(null);

  useEffect(() => {
    let live = true;
    const load = () =>
      get<{ offset_ms: number }>(`/api/cameras/${encodeURIComponent(cameraId)}/clock`)
        .then((c) => {
          if (!live) return;
          setOffset(c.offset_ms);
          setFailed(false);
        })
        .catch(() => live && setFailed(true));
    void load();
    const t = window.setInterval(load, 60_000);
    return () => {
      live = false;
      window.clearInterval(t);
    };
  }, [cameraId]);

  // Tenths of a second: written straight to the DOM, no React render per frame.
  useEffect(() => {
    let raf = 0;
    const tick = () => {
      if (clockRef.current) clockRef.current.textContent = fmt(serverNow() + (offset ?? 0));
      raf = requestAnimationFrame(tick);
    };
    tick();
    return () => cancelAnimationFrame(raf);
  }, [offset]);

  const off = offset === null ? null : offset / 1000;
  return (
    <div className="delay-meter" role="status" onPointerDown={(e) => e.stopPropagation()} onDoubleClick={(e) => e.stopPropagation()}>
      <span className="dm-clock num" ref={clockRef} />
      <span className="dm-label">{t("with no delay, the camera would show")}</span>
      <span className="dm-note">
        {failed
          ? t("Camera clock did not respond; using the server's.")
          : off === null
            ? t("Reading the camera's clock…")
            : Math.abs(off) < 0.6
              ? t("Camera clock is right.")
              : t(off < 0 ? "Camera clock {s} s behind (already subtracted)." : "Camera clock {s} s ahead (already subtracted).", { s: fmtNum(Math.abs(off), 1, 1) })}
      </span>
      <span className="dm-note">
        {t("Maximum precision: open")}{" "}<a href="#/clock" target="_blank" rel="noreferrer">{t("the clock")}</a>{" "}{t("on the phone and show it to the camera.")}
      </span>
      <button className="dm-close" onClick={onClose} aria-label={t("Close delay meter")} title={t("Close")}>
        ×
      </button>
    </div>
  );
}

/** Full-screen server clock with hundredths, to film with the camera. */
export function ClockPage() {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    let raf = 0;
    const tick = () => {
      const d = new Date(serverNow());
      const p = (n: number, w = 2) => String(n).padStart(w, "0");
      if (ref.current)
        ref.current.textContent = `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())},${p(Math.floor(d.getMilliseconds() / 10))}`;
      raf = requestAnimationFrame(tick);
    };
    tick();
    return () => cancelAnimationFrame(raf);
  }, []);
  return (
    <div className="clock-page">
      <div ref={ref} className="clock-big num" />
      <p className="muted">
        {t("Show this screen to the camera and compare it with the live view: the difference between this clock and the same clock inside the video is the real end-to-end delay.")}
      </p>
      <a className="btn" href="#/live">
        {t("Back to live")}
      </a>
    </div>
  );
}
