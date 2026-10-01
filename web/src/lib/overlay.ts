// How detections are drawn over the live view. A display preference of
// this device (like the theme), not a camera setting.
import { useSyncExternalStore } from "react";

export interface OverlayPrefs {
  /** Boxes around people, vehicles and animals. */
  boxes: boolean;
  /** "Pessoa", "Veículo"… on each box. */
  labels: boolean;
  /** The detector's confidence, in %. */
  score: boolean;
  /** Also what does not warn (outside the zones, parked). */
  all: boolean;
  /** The zones' outlines. */
  zones: boolean;
}

export const OVERLAY_DEFAULTS: OverlayPrefs = { boxes: true, labels: true, score: false, all: false, zones: false };
const KEY = "sentinela.overlay";
const listeners = new Set<() => void>();

function read(): OverlayPrefs {
  try {
    const raw = localStorage.getItem(KEY);
    if (raw) return { ...OVERLAY_DEFAULTS, ...(JSON.parse(raw) as Partial<OverlayPrefs>) };
  } catch {
    /* no storage or bad JSON: defaults */
  }
  return OVERLAY_DEFAULTS;
}

let current = read();

export function setOverlay(patch: Partial<OverlayPrefs>) {
  current = { ...current, ...patch };
  try {
    localStorage.setItem(KEY, JSON.stringify(current));
  } catch {
    /* kept for this visit only */
  }
  listeners.forEach((l) => l());
}

export function useOverlay(): OverlayPrefs {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l);
      return () => listeners.delete(l);
    },
    () => current,
  );
}
