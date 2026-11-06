import { useEffect, useRef, useState, type RefObject } from "react";

export const EXIT_PLAYER_FULLSCREEN = "sentinela:exit-fullscreen";

/** Keeps the same video mounted while expanding it to the viewport. */
export function createPlayerFullscreen(el: HTMLElement, onChange: (expanded: boolean) => void) {
  let automatic = false;
  let dismissed = false;
  let manual = false;
  let native = document.fullscreenElement === el;
  let destroyed = false;

  const expanded = () => manual || (automatic && !dismissed) || native;
  const update = () => {
    if (destroyed) return;
    el.classList.toggle("viewport-fullscreen", expanded());
    document.body.classList.toggle("live-fullscreen", !!document.querySelector(".viewport-fullscreen"));
    onChange(expanded());
  };
  const exitNative = () => {
    if (document.fullscreenElement === el) void document.exitFullscreen().catch(() => {});
  };
  const close = () => {
    manual = false;
    dismissed = automatic;
    native = false;
    exitNative();
    update();
  };
  const onNative = () => {
    const now = document.fullscreenElement === el;
    if (native && !now) {
      manual = false;
      dismissed = automatic;
    }
    native = now;
    update();
  };
  const onKey = (e: KeyboardEvent) => {
    if (e.key === "Escape" && expanded()) {
      e.preventDefault();
      close();
    }
  };
  el.addEventListener(EXIT_PLAYER_FULLSCREEN, close);
  document.addEventListener("fullscreenchange", onNative);
  document.addEventListener("keydown", onKey);

  return {
    setAutomatic(wanted: boolean) {
      if (wanted === automatic) return;
      automatic = wanted;
      dismissed = false;
      // Rotation uses viewport fullscreen: browser fullscreen normally needs
      // a tap, and an orientation lock would prevent returning to portrait.
      update();
    },
    toggle() {
      if (expanded()) {
        close();
        return;
      }
      manual = true;
      update();
      // A tap can also hide browser/system chrome where supported. Rejection
      // leaves the viewport fallback active, including HTTP and iPhone Safari.
      if (el.requestFullscreen) {
        void el.requestFullscreen().then(() => {
          // A pending request may finish after Back or navigation.
          if (destroyed || !manual) exitNative();
        }).catch(() => {});
      }
    },
    destroy() {
      destroyed = true;
      el.removeEventListener(EXIT_PLAYER_FULLSCREEN, close);
      document.removeEventListener("fullscreenchange", onNative);
      document.removeEventListener("keydown", onKey);
      el.classList.remove("viewport-fullscreen");
      document.body.classList.toggle("live-fullscreen", !!document.querySelector(".viewport-fullscreen"));
      exitNative();
    },
  };
}

export function usePlayerFullscreen(ref: RefObject<HTMLElement | null>, automatic: boolean) {
  const controller = useRef<ReturnType<typeof createPlayerFullscreen> | null>(null);
  const [expanded, setExpanded] = useState(false);
  useEffect(() => {
    if (!ref.current) return;
    const c = createPlayerFullscreen(ref.current, setExpanded);
    controller.current = c;
    return () => {
      c.destroy();
      controller.current = null;
    };
  }, [ref]);
  useEffect(() => controller.current?.setAutomatic(automatic), [automatic]);
  return { expanded, toggle: () => controller.current?.toggle() };
}
