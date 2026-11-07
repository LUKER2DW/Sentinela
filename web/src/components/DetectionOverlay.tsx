import { useEffect, useRef, useState, type CSSProperties } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, get } from "../lib/api";
import { t } from "../lib/i18n";
import { useSeen } from "../lib/detections";
import { useOverlay } from "../lib/overlay";
import { KIND_ONE, ZONE_COLORS, svgPoints, type DetectionSettings } from "../lib/zones";

interface Props {
  cameraId: string;
  /** The video's intrinsic size: the picture sits inside the player like object-fit: contain. */
  videoW: number;
  videoH: number;
  /** Same transform as the video (pinch zoom), so boxes stay on their objects. */
  style?: CSSProperties;
  /** Keep the detector analysing this camera while it is on screen. */
  probe?: boolean;
}

/**
 * What the detector sees, drawn over the live picture: a box per person,
 * vehicle or animal, with a dot where it stands (the feet decide the zone).
 * Boxes come with each analysed keyframe (about every 2 s) and fade out when
 * no new analysis arrives. The detector normally analyses only on camera
 * motion; while boxes are shown here it is asked to keep analysing.
 */
export function DetectionOverlay({ cameraId, videoW, videoH, style, probe }: Props) {
  const prefs = useOverlay();
  const seen = useSeen(cameraId);
  const layer = useRef<HTMLDivElement>(null);
  const [box, setBox] = useState({ w: 16, h: 9 });
  const zones = useQuery({
    queryKey: ["detection", cameraId],
    queryFn: () => get<DetectionSettings>(`/api/cameras/${cameraId}/detection`),
    enabled: prefs.zones,
    staleTime: 60_000,
  });

  // Ask the detector to analyse while this view is on screen and visible.
  useEffect(() => {
    if (!probe || !prefs.boxes) return;
    const ask = () => {
      if (document.visibilityState === "visible") void api("POST", `/api/cameras/${encodeURIComponent(cameraId)}/detection/probe`).catch(() => {});
    };
    ask();
    const t = window.setInterval(ask, 15_000);
    document.addEventListener("visibilitychange", ask);
    return () => {
      window.clearInterval(t);
      document.removeEventListener("visibilitychange", ask);
    };
  }, [probe, prefs.boxes, cameraId]);

  useEffect(() => {
    const el = layer.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setBox({ w: el.clientWidth || 16, h: el.clientHeight || 9 }));
    ro.observe(el);
    return () => ro.disconnect();
  }, [prefs.boxes, prefs.zones]); // the layer exists only while something is shown

  if (!prefs.boxes && !prefs.zones) return null;

  // The picture's rectangle inside the layer (letterbox / pillarbox).
  const a = videoW && videoH ? videoW / videoH : 16 / 9;
  const wide = box.w / box.h > a;
  const frame: CSSProperties = wide
    ? { width: `${((box.h * a) / box.w) * 100}%`, height: "100%", left: `${((1 - (box.h * a) / box.w) / 2) * 100}%`, top: 0 }
    : { width: "100%", height: `${(box.w / a / box.h) * 100}%`, top: `${((1 - box.w / a / box.h) / 2) * 100}%`, left: 0 };

  const objects = prefs.boxes ? (seen?.objects ?? []).filter((o) => prefs.all || o.counted) : [];
  const shownZones = prefs.zones ? (zones.data?.zones ?? []) : [];

  return (
    <div ref={layer} className="det-layer" style={style} aria-hidden="true">
      <div className="det-frame" style={frame}>
        {shownZones.length > 0 && (
          <svg className="det-zones" viewBox="0 0 1000 1000" preserveAspectRatio="none">
            {shownZones.map((z, i) => (
              <polygon key={z.name} points={svgPoints(z.points, 1000, 1000)} style={{ "--zc": ZONE_COLORS[i % ZONE_COLORS.length] } as CSSProperties} />
            ))}
          </svg>
        )}
        {/* A new key per analysis restarts the fade. */}
        <div key={seen?.at ?? 0} className="det-boxes">
          {objects.map((o, i) => (
            <div
              key={i}
              className={`det-box k-${o.kind}${o.counted ? "" : " quiet"}`}
              style={{ left: `${o.x * 100}%`, top: `${o.y * 100}%`, width: `${o.w * 100}%`, height: `${o.h * 100}%` }}
            >
              {(prefs.labels || prefs.score) && (
                <span className={`det-label${o.y < 0.08 ? " inside" : ""}`}>
                  {prefs.labels && KIND_ONE[o.kind]}
                  {prefs.labels && o.parked && ` ${t("still")}`}
                  {prefs.score && <span className="num"> {Math.round(o.score * 100)}%</span>}
                </span>
              )}
              <span className="det-feet" />
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
