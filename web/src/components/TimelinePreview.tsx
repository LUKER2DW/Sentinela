import { useEffect, useState } from "react";
import type { Segment } from "../lib/api";
import { findSegment } from "../lib/segments";
import { fmtTime } from "../lib/time";

/**
 * The picture at a moment of the timeline, shown while hovering the day
 * ribbon or dragging the ruler. The server sends only the keyframe group at
 * that time (/api/recordings/{id}?at=&gop=1, bytes copied, not decoded) and
 * the browser shows its first frame, so the picture is from up to one
 * keyframe interval before the time under the pointer.
 */
export function TimelinePreview({ segments, ms }: { segments: Segment[]; ms: number }) {
  const hit = findSegment(segments, ms);
  const inside = !!hit && new Date(hit.seg.start).getTime() <= ms;
  // Wait until the pointer rests a moment: moving across the day must not
  // fetch a file per pixel. Rounded to 2 s so nearby positions share one.
  const want = inside && hit ? `/api/recordings/${hit.seg.id}?at=${(Math.floor(hit.offset / 2) * 2).toFixed(0)}&gop=1` : null;
  const [src, setSrc] = useState<string | null>(null);
  const [ready, setReady] = useState<string | null>(null);
  const [failed, setFailed] = useState<string | null>(null);
  useEffect(() => {
    const t = window.setTimeout(() => setSrc(want), 160);
    return () => window.clearTimeout(t);
  }, [want]);

  return (
    <div className="tl-preview" aria-hidden="true">
      <div className="tl-preview-frame">
        {!inside ? (
          <span className="tl-preview-none">Sem gravação</span>
        ) : (
          <>
            {src && failed !== src && <video key={src} src={src} muted playsInline preload="auto" onLoadedData={() => setReady(src)} onError={() => setFailed(src)} />}
            {failed === src ? <span className="tl-preview-none">Sem prévia</span> : ready !== src && <span className="spinner small" />}
          </>
        )}
      </div>
      <span className="tl-preview-time">{fmtTime(ms)}</span>
    </div>
  );
}
