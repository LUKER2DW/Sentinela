// Notifications on this device (ADR-0013): permission, subscription, test.
// Browsers and the PWA use Web Push. The Android app's WebView has no Push
// API: its native SentinelaNotify plugin uses Firebase as a wake-up signal,
// then fetches event details directly from the house. On phones without
// Google Play services it keeps the direct foreground connection.
import { api, get } from "./api";

export type PushState = "unsupported" | "insecure" | "denied" | "off" | "on";

interface NativeStatus {
  permission: "granted" | "denied" | "prompt";
  running: boolean;
  transport?: "firebase" | "direct" | "off"; // apps before 1.11 omit it
  /** Android may pause the service to save battery. */
  batteryOptimized: boolean;
}

type Bridge = { isNativePlatform?: () => boolean; nativePromise?: <T>(plugin: string, method: string, opts?: object) => Promise<T> };

/** Calls the Android app's SentinelaNotify plugin, or undefined in a browser. */
function native(): (<T>(method: string, opts?: object) => Promise<T>) | undefined {
  const cap = (window as unknown as { Capacitor?: Bridge }).Capacitor;
  if (!cap?.isNativePlatform?.() || !cap.nativePromise) return undefined;
  const call = cap.nativePromise;
  return <T>(method: string, opts: object = {}) => call<T>("SentinelaNotify", method, opts);
}

export const isApp = () => native() !== undefined;

/** The app's service status (undefined in a browser). */
export const nativeStatus = () => native()?.<NativeStatus>("status");

/** Refresh the native copy of the signed-in session after app login. */
export const syncNativePush = () => native()?.<NativeStatus>("sync");

/** Opens Android's "ignore battery optimisation" prompt for the app. */
export const allowBackground = () => native()?.("allowBackground");

/** Opens Android's switch for the direct fallback's fixed notice. */
export const hideOngoing = () => native()?.("hideOngoing");

/** What each event type is called on the settings card. */
export const TYPE_LABEL: Record<string, string> = {
  human: "Pessoa",
  vehicle: "Veículo",
  animal: "Animal",
  line: "Linha cruzada",
  intrusion: "Entrada na área",
  tamper: "Câmera mexida ou tapada",
  motion: "Movimento",
};

export interface NotifyPrefs {
  follow_mode: boolean;
  types: string[];
}

export const getPrefs = () => get<{ prefs: NotifyPrefs; types: string[] }>("/api/me/notify");
export const putPrefs = (p: NotifyPrefs) => api<{ prefs: NotifyPrefs; types: string[] }>("PUT", "/api/me/notify", p);

export interface ModeSettings {
  mode: "home" | "night" | "away";
  night_auto: boolean;
  night_from: string;
  night_to: string;
}

export interface ModeInfo {
  settings: ModeSettings;
  effective: ModeSettings["mode"];
}

export const getMode = () => get<ModeInfo>("/api/notify-mode");
export const putMode = (m: ModeSettings) => api<ModeInfo>("PUT", "/api/notify-mode", m);

function supported() {
  return "serviceWorker" in navigator && "PushManager" in window && "Notification" in window;
}

async function registration() {
  return navigator.serviceWorker.ready;
}

async function nativeState(): Promise<PushState> {
  const st = await native()!<NativeStatus>("status");
  if (st.permission === "denied") return "denied";
  return st.running ? "on" : "off";
}

export async function pushState(): Promise<PushState> {
  if (native()) return nativeState();
  if (!window.isSecureContext) return "insecure";
  if (!supported()) return "unsupported";
  if (Notification.permission === "denied") return "denied";
  const sub = await (await registration()).pushManager.getSubscription();
  return sub ? "on" : "off";
}

function keyBytes(b64url: string): Uint8Array<ArrayBuffer> {
  const pad = "=".repeat((4 - (b64url.length % 4)) % 4);
  const raw = atob((b64url + pad).replace(/-/g, "+").replace(/_/g, "/"));
  const out = new Uint8Array(new ArrayBuffer(raw.length));
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
  return out;
}

export async function enablePush(): Promise<PushState> {
  const n = native();
  if (n) {
    await n("enable");
    return nativeState();
  }
  if (!supported() || !window.isSecureContext) return pushState();
  if ((await Notification.requestPermission()) !== "granted") return pushState();
  const { public_key } = await get<{ public_key: string }>("/api/push");
  const reg = await registration();
  let sub = await reg.pushManager.getSubscription();
  if (!sub) sub = await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: keyBytes(public_key) });
  await api("POST", "/api/push/subscribe", sub.toJSON());
  return "on";
}

export async function disablePush(): Promise<PushState> {
  const n = native();
  if (n) {
    await n("disable");
    return pushState();
  }
  const sub = await (await registration()).pushManager.getSubscription();
  if (sub) {
    await api("POST", "/api/push/unsubscribe", { endpoint: sub.endpoint }).catch(() => {});
    await sub.unsubscribe();
  }
  return pushState();
}

export const testPush = () => api<{ sent: number }>("POST", "/api/push/test");
