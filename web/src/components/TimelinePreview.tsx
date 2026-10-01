import { useEffect, useState } from "react";
import type { Segment } from "../lib/api";
import { findSegment } from "../lib/segments";
import { fmtTime } from "../lib/time";

/**
 * The picture at a moment of the timeline, shown while hovering the day
 * ribbon or dragging the ruler. The server sends one keyframe, video only
 * (/api/recordings/{id}?at=&gop=1, copied bytes, built once and kept in
 * memory), so the picture is from up to one keyframe interval before the
 * time under the pointer. The last picture stays up while the next loads.
 */
export function TimelinePreview({ segments, ms }: { segments: Segment[]; ms: number }) {
  const hit = findSegment(segments, ms);
  const inside = !!hit && new Date(hit.seg.start).getTime() <= ms;
  // A short rest before asking, so sweeping across the day does not fetch
  // a file per pixel; rounded to 2 s so nearby positions share one picture.
  const want = inside && hit ? `/api/recordings/${hit.seg.id}?at=${(Math.floor(hit.offset / 2) * 2).toFixed(0)}&gop=1` : null;
  const [src, setSrc] = useState<string | null>(null);
  const [shown, setShown] = useState<string | null>(null);
  const [failed, setFailed] = useState<string | null>(null);
  useEffect(() => {
    const t = window.setTimeout(() => setSrc(want), 60);
    return () => window.clearTimeout(t);
  }, [want]);

  return (
    <div className="tl-preview" aria-hidden="true">
      <div className="tl-preview-frame">
        {!inside ? (
          <span className="tl-preview-none">Sem gravação</span>
        ) : (
          <>
            {shown && <video key={shown} src={shown} muted playsInline preload="auto" />}
            {src && src !== shown && failed !== src && (
              <video key={src} className="loading" src={src} muted playsInline preload="auto" onLoadedData={() => setShown(src)} onError={() => setFailed(src)} />
            )}
            {failed === src ? <span className="tl-preview-none">Sem prévia</span> : !shown && <span className="spinner small" />}
          </>
        )}
      </div>
      <span className="tl-preview-time">{fmtTime(ms)}</span>
    </div>
  );
}
