import { useRef, useState, type CSSProperties, type PointerEvent, type RefObject } from "react";
import { NO_ZOOM, clampZoom, haptic, type Zoom } from "./gestures";

type Point = { x: number; y: number };

/**
 * Pinch zoom with pan and double-tap reset for recordings and photos (the
 * live view has its own, with tap-to-show-controls). The page itself does
 * not zoom on phones (index.html viewport), only these.
 */
export function usePinchZoom(wrapRef: RefObject<HTMLElement | null>, onDoubleTap?: () => void) {
  const [zoom, setZoom] = useState<Zoom>(NO_ZOOM);
  const pointers = useRef(new Map<number, Point>());
  const pinch = useRef<{ dist: number; scale: number } | null>(null);
  const pan = useRef<Point | null>(null);
  const tap = useRef<{ at: number; x: number; y: number; moved: boolean } | null>(null);
  const lastTap = useRef(0);

  const size = () => {
    const r = wrapRef.current?.getBoundingClientRect();
    return { w: r?.width ?? 0, h: r?.height ?? 0 };
  };
  const distance = () => {
    const [a, b] = [...pointers.current.values()];
    return Math.hypot(a.x - b.x, a.y - b.y);
  };
  const fromControls = (e: PointerEvent) => !!(e.target as HTMLElement).closest(".player-bar, button, a, input");

  const onPointerDown = (e: PointerEvent) => {
    if (e.pointerType === "mouse" || fromControls(e)) return;
    pointers.current.set(e.pointerId, { x: e.clientX, y: e.clientY });
    if (pointers.current.size === 2) {
      pinch.current = { dist: distance(), scale: zoom.scale };
      tap.current = null;
    } else if (pointers.current.size === 1) {
      tap.current = { at: e.timeStamp, x: e.clientX, y: e.clientY, moved: false };
      pan.current = zoom.scale > 1 ? { x: e.clientX, y: e.clientY } : null;
    }
  };
  const onPointerMove = (e: PointerEvent) => {
    if (!pointers.current.has(e.pointerId)) return;
    pointers.current.set(e.pointerId, { x: e.clientX, y: e.clientY });
    if (tap.current && Math.hypot(e.clientX - tap.current.x, e.clientY - tap.current.y) > 10) tap.current.moved = true;
    const { w, h } = size();
    if (pinch.current && pointers.current.size === 2) {
      const scale = (pinch.current.scale * distance()) / pinch.current.dist;
      setZoom((z) => clampZoom({ ...z, scale }, w, h));
    } else if (pan.current) {
      const dx = e.clientX - pan.current.x;
      const dy = e.clientY - pan.current.y;
      pan.current = { x: e.clientX, y: e.clientY };
      setZoom((z) => clampZoom({ ...z, x: z.x + dx, y: z.y + dy }, w, h));
    }
  };
  const onPointerUp = (e: PointerEvent) => {
    if (!pointers.current.delete(e.pointerId)) return;
    if (pointers.current.size < 2) pinch.current = null;
    if (pointers.current.size === 0) pan.current = null;
    const t = tap.current;
    tap.current = null;
    if (!t || t.moved || e.timeStamp - t.at > 300 || e.type === "pointercancel") return;
    if (e.timeStamp - lastTap.current < 320) {
      lastTap.current = 0;
      haptic();
      if (zoom.scale > 1) setZoom(NO_ZOOM);
      else onDoubleTap?.();
    } else {
      lastTap.current = e.timeStamp;
    }
  };

  const style: CSSProperties | undefined =
    zoom.scale > 1 ? { transform: `translate(${zoom.x}px, ${zoom.y}px) scale(${zoom.scale})` } : undefined;
  return {
    zoom,
    zoomed: zoom.scale > 1,
    style,
    reset: () => setZoom(NO_ZOOM),
    handlers: { onPointerDown, onPointerMove, onPointerUp, onPointerCancel: onPointerUp },
  };
}
