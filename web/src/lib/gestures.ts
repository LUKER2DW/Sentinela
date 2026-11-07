// Touch helpers for the live view: pinch zoom math, stream choice on
// cellular, haptics. Kept free of React so they are easy to test.

export interface Zoom {
  scale: number;
  x: number;
  y: number;
}

export const NO_ZOOM: Zoom = { scale: 1, x: 0, y: 0 };
export const MAX_ZOOM = 4;

/** Keeps the zoomed picture covering the frame (no empty borders). */
export function clampZoom(z: Zoom, width: number, height: number): Zoom {
  const scale = Math.min(MAX_ZOOM, Math.max(1, z.scale));
  if (scale === 1) return NO_ZOOM;
  const mx = ((scale - 1) * width) / 2;
  const my = ((scale - 1) * height) / 2;
  return { scale, x: Math.min(mx, Math.max(-mx, z.x)), y: Math.min(my, Math.max(-my, z.y)) };
}

type Conn = { type?: string; effectiveType?: string; saveData?: boolean };

/** True on mobile data or when the user asked the browser to save data. */
export function onMeteredNetwork(conn: Conn | undefined = (navigator as Navigator & { connection?: Conn }).connection): boolean {
  if (!conn) return false;
  return conn.saveData === true || conn.type === "cellular" || /^(slow-2g|2g|3g)$/.test(conn.effectiveType ?? "");
}

const STREAM_KEY = "sentinela.stream";

/** HD/SD for the single-camera view: the user's last choice on this device,
 * else SD on phones and metered networks, HD elsewhere. */
export function initialStream(phone: boolean): "main" | "sub" {
  try {
    const v = localStorage.getItem(STREAM_KEY);
    if (v === "main" || v === "sub") return v;
  } catch {
    /* no storage: fall through */
  }
  return phone || onMeteredNetwork() ? "sub" : "main";
}

export function saveStream(s: "main" | "sub") {
  try {
    localStorage.setItem(STREAM_KEY, s);
  } catch {
    /* not remembered */
  }
}

/** A short vibration on touch actions, where the phone supports it. */
export function haptic(ms = 12) {
  try {
    navigator.vibrate?.(ms);
  } catch {
    /* ignored */
  }
}
