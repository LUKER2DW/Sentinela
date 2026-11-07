import type { Segment } from "./api";

/** The segment playing at ms, or the next one after it. */
export function findSegment(segs: Segment[], ms: number): { seg: Segment; offset: number } | null {
  for (const s of segs) {
    const a = new Date(s.start).getTime();
    const b = new Date(s.end).getTime();
    if (ms >= a && ms < b) return { seg: s, offset: (ms - a) / 1000 };
    if (a > ms) return { seg: s, offset: 0 };
  }
  return null;
}
