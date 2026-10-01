// What the object detector sees right now, per camera (ADR-0016). Fed by
// the "objects" events of the server's SSE stream (liveEvents.tsx) and read
// by the live overlay and the zone editor. Kept outside React state so a
// box update every ~2 s only re-renders the overlays.
import { useSyncExternalStore } from "react";
import type { SeenObject } from "./zones";

export interface Seen {
  /** When the browser received it (ms). */
  at: number;
  objects: SeenObject[];
}

const seen = new Map<string, Seen>();
const listeners = new Set<() => void>();

export function pushSeen(cameraId: string, objects: SeenObject[]) {
  seen.set(cameraId, { at: Date.now(), objects });
  listeners.forEach((l) => l());
}

function subscribe(l: () => void) {
  listeners.add(l);
  return () => listeners.delete(l);
}

export function useSeen(cameraId: string): Seen | undefined {
  return useSyncExternalStore(subscribe, () => seen.get(cameraId));
}
