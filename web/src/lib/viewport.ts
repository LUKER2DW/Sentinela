import { useSyncExternalStore } from "react";

// Phone layout: the tab bar breakpoint in styles.css, or the Android app on
// any screen size (a tablet running the app is still "the phone app").
const PHONE = "(max-width: 760px)";

const inApp = () => typeof navigator !== "undefined" && /SentinelaApp/.test(navigator.userAgent);

function subscribe(cb: () => void) {
  const mq = window.matchMedia(PHONE);
  mq.addEventListener("change", cb);
  return () => mq.removeEventListener("change", cb);
}

/** Right now: a phone-sized screen or the Android app. */
export const isPhone = () => inApp() || (typeof window !== "undefined" && window.matchMedia(PHONE).matches);

/** True on phone-sized screens and in the Android app. */
export function useCompact(): boolean {
  return useSyncExternalStore(
    subscribe,
    () => inApp() || window.matchMedia(PHONE).matches,
    () => false,
  );
}
