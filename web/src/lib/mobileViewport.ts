import { useSyncExternalStore } from "react";

// A phone stays mobile after rotation, even when its width exceeds the tab
// bar breakpoint. A short desktop window with a mouse remains desktop.
const MOBILE = "(max-width: 760px), (pointer: coarse) and (max-height: 760px)";
const LANDSCAPE = "(orientation: landscape)";

type NativeWindow = Window & { Capacitor?: { isNativePlatform?: () => boolean } };

export function isMobileViewport(): boolean {
  return typeof window !== "undefined" && (
    /SentinelaApp/.test(navigator.userAgent) ||
    !!(window as NativeWindow).Capacitor?.isNativePlatform?.() ||
    window.matchMedia(MOBILE).matches
  );
}

function subscribe(cb: () => void) {
  const queries = [window.matchMedia(MOBILE), window.matchMedia(LANDSCAPE)];
  queries.forEach((mq) => mq.addEventListener("change", cb));
  return () => queries.forEach((mq) => mq.removeEventListener("change", cb));
}

export function useMobileViewport(): boolean {
  return useSyncExternalStore(subscribe, isMobileViewport, () => false);
}

export function useMobileLandscape(): boolean {
  return useSyncExternalStore(
    subscribe,
    () => isMobileViewport() && window.matchMedia(LANDSCAPE).matches,
    () => false,
  );
}

/** Services are available in the desktop UI only, including direct links. */
export function mobilePage(page: string, mobile: boolean): string {
  return mobile && page === "services" ? "system" : page;
}
