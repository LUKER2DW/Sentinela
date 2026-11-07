import { useEffect, useReducer, useState } from "react";
import type { Segment } from "../lib/api";
import { frameFailed, frameFor, onFrames, previewUrl, wantFrame } from "../lib/previewFrames";
import { fmtTime } from "../lib/time";

/**
 * The picture at a moment of the timeline, shown while hovering the day
 * ribbon or holding/dragging on the ribbon or ruler. The picture is from up
 * to one keyframe interval before the time under the pointer (see
 * lib/previewFrames). The last picture stays up while the next loads.
 */
export function TimelinePreview({ segments, ms }: { segments: Segment[]; ms: number }) {
  const want = previewUrl(segments, ms);
  const [, redraw] = useReducer((n: number) => n + 1, 0);
  useEffect(() => onFrames(redraw), []);
  useEffect(() => {
    if (!want || frameFor(want)) return;
    // A short rest before asking, so sweeping across the day does not queue
    // a file per pixel.
    const t = window.setTimeout(() => wantFrame(want), 40);
    return () => window.clearTimeout(t);
  }, [want]);

  const frame = frameFor(want);
  const [shown, setShown] = useState(frame);
  if (frame && frame !== shown) setShown(frame);

  return (
    <div className="tl-preview" aria-hidden="true">
      <div className="tl-preview-frame">
        {!want ? (
          <span className="tl-preview-none">Sem gravação</span>
        ) : (
          <>
            {shown && <img src={shown} alt="" />}
            {frameFailed(want) ? <span className="tl-preview-none">Sem prévia</span> : !shown && <span className="spinner small" />}
          </>
        )}
      </div>
      <span className="tl-preview-time">{fmtTime(ms)}</span>
    </div>
  );
}
