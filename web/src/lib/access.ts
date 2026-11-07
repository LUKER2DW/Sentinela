// How this page reached the server (ADR-0019): the zone comes from the
// server, by the listener the request arrived on.

export type Zone = "lan" | "tailnet" | "funnel" | "public";

let zone: Zone = "lan";

export const setZone = (z: string | undefined) => {
  if (z === "lan" || z === "tailnet" || z === "funnel" || z === "public") zone = z;
};
export const getZone = () => zone;

/** Anyone on the internet can reach this path: two-step login applies. */
export const isPublicZone = (z: Zone = zone) => z === "funnel" || z === "public";

export function zoneLabel(z: Zone = zone): string {
  switch (z) {
    case "lan":
      return "Rede de casa";
    case "tailnet":
      return "Tailscale";
    case "funnel":
      return "Tailscale Funnel (público)";
    case "public":
      return "Endereço público da casa";
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
