// Notifications on this device (ADR-0013): permission, subscription, test.
// Browsers and the PWA use Web Push; the Android app has no Push API in its
// WebView and asks its native SentinelaPush plugin for a UnifiedPush endpoint
// (through the ntfy app) instead. The server treats both the same way.
import { api, get } from "./api";

export type PushState = "unsupported" | "insecure" | "denied" | "off" | "on" | "no-ntfy" | "no-relay";

interface PushInfo {
  public_key: string;
  devices: number;
  ntfy_url: string;
}

interface NativeSub {
  endpoint?: string;
  keys?: { p256dh: string; auth: string };
}

interface NativeStatus extends NativeSub {
  distributor: boolean;
  permission: "granted" | "denied" | "prompt";
}

type Bridge = { isNativePlatform?: () => boolean; nativePromise?: <T>(plugin: string, method: string, opts?: object) => Promise<T> };

/** Calls the Android app's SentinelaPush plugin, or undefined in a browser. */
function native(): (<T>(method: string, opts?: object) => Promise<T>) | undefined {
  const cap = (window as unknown as { Capacitor?: Bridge }).Capacitor;
  if (!cap?.isNativePlatform?.() || !cap.nativePromise) return undefined;
  const call = cap.nativePromise;
  return <T>(method: string, opts: object = {}) => call<T>("SentinelaPush", method, opts);
}

export const isApp = () => native() !== undefined;

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
  const n = native()!;
  const st = await n<NativeStatus>("status");
  if (!st.distributor) return "no-ntfy";
  if (st.permission === "denied") return "denied";
  return st.endpoint ? "on" : "off";
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

async function enableNative(): Promise<PushState> {
  const n = native()!;
  const info = await get<PushInfo>("/api/push");
  if (!info.ntfy_url) return "no-relay";
  const sub = await n<NativeSub>("register", { vapid: info.public_key });
  if (!sub.endpoint?.startsWith(info.ntfy_url + "/")) {
    // ntfy registered on another server (ntfy.sh by default): the server would refuse it.
    await n("unregister").catch(() => {});
    throw new Error(`No app ntfy, mude o "Servidor padrão" para ${info.ntfy_url} e tente de novo.`);
  }
  await api("POST", "/api/push/subscribe", sub);
  return "on";
}

/** Re-sends the app's endpoint (ntfy may rotate it); no-op in browsers. */
export async function refreshNativePush() {
  const n = native();
  if (!n) return;
  const st = await n<NativeStatus>("status");
  if (st.endpoint) await api("POST", "/api/push/subscribe", { endpoint: st.endpoint, keys: st.keys });
}

export async function enablePush(): Promise<PushState> {
  if (native()) return enableNative();
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
    const st = await n<NativeStatus>("status");
    if (st.endpoint) await api("POST", "/api/push/unsubscribe", { endpoint: st.endpoint }).catch(() => {});
    await n("unregister");
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
