
import { t } from "./i18n";// How this page reached the server (ADR-0019): the zone comes from the
// server, by the listener the request arrived on.

export type Zone = "lan" | "tailnet" | "funnel" | "public" | "proxy";

let zone: Zone = "lan";

export const setZone = (z: string | undefined) => {
  if (z === "lan" || z === "tailnet" || z === "funnel" || z === "public" || z === "proxy") zone = z;
};
export const getZone = () => zone;

/** Anyone on the internet can reach this path: two-step login applies. */
export const isPublicZone = (z: Zone = zone) => z === "funnel" || z === "public" || z === "proxy";

export function zoneLabel(z: Zone = zone): string {
  switch (z) {
    case "lan":
      return t("Home network");
    case "tailnet":
      return "Tailscale";
    case "funnel":
      return t("Tailscale Funnel (public)");
    case "public":
      return t("Public address of the home");
    case "proxy":
      return t("Tunnel or reverse proxy (public)");
  }
}

type Bridge = { isNativePlatform?: () => boolean; nativePromise?: <T>(plugin: string, method: string, opts?: object) => Promise<T> };

function bridge(): Bridge | undefined {
  const cap = (window as unknown as { Capacitor?: Bridge }).Capacitor;
  return cap?.isNativePlatform?.() && cap.nativePromise ? cap : undefined;
}

/** True inside the Android app (it has a start screen with the server address). */
export const canChangeConnection = () => bridge() !== undefined;

/** Back to the app's start screen to pick another path. */
export function changeConnection() {
  void bridge()?.nativePromise?.("SentinelaConnection", "change");
}
