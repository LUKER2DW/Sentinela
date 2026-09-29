// Web Push on this device (ADR-0013): permission, subscription, test.
import { api, get } from "./api";

export type PushState = "unsupported" | "insecure" | "denied" | "off" | "on";

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

export async function pushState(): Promise<PushState> {
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
  const sub = await (await registration()).pushManager.getSubscription();
  if (sub) {
    await api("POST", "/api/push/unsubscribe", { endpoint: sub.endpoint }).catch(() => {});
    await sub.unsubscribe();
  }
  return pushState();
}

export const testPush = () => api<{ sent: number }>("POST", "/api/push/test");
