// How this page reached the server (ADR-0019 trial). The zone comes from the
// server (by listener); the embedded-tailnet app is recognised by its local
// proxy origin.

export type Zone = "lan" | "tailnet" | "funnel" | "public";

let zone: Zone = "lan";

export const setZone = (z: string | undefined) => {
  if (z === "lan" || z === "tailnet" || z === "funnel" || z === "public") zone = z;
};
export const getZone = () => zone;

/** Anyone on the internet can reach this path: two-step login applies. */
export const isPublicZone = (z: Zone = zone) => z === "funnel" || z === "public";

/** The Android app's own tailnet node (local proxy on 127.0.0.1:47080). */
export const embeddedTailnet = () =>
  typeof location !== "undefined" && location.hostname === "127.0.0.1" && location.port === "47080";

export function zoneLabel(z: Zone = zone): string {
  if (embeddedTailnet()) return "Tailscale embutido no app";
  switch (z) {
    case "lan":
      return "Rede de casa";
    case "tailnet":
      return "Tailscale";
    case "funnel":
      return "Tailscale Funnel (público)";
    case "public":
      return "Domínio da casa (público)";
  }
}

type Bridge = { isNativePlatform?: () => boolean; nativePromise?: <T>(plugin: string, method: string, opts?: object) => Promise<T> };

function bridge(): Bridge | undefined {
  const cap = (window as unknown as { Capacitor?: Bridge }).Capacitor;
  return cap?.isNativePlatform?.() && cap.nativePromise ? cap : undefined;
}

/** True inside the Android app with the connection start screen. */
export const canChangeConnection = () => bridge() !== undefined;

/** Back to the app's start screen to pick another path. */
export function changeConnection() {
  void bridge()?.nativePromise?.("SentinelaConnection", "change");
}
