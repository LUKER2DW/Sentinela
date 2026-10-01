import type { Segment } from "./api";
import { findSegment } from "./segments";

// Timeline preview pictures. The server sends one keyframe per request
// (/api/recordings/{id}?at=&gop=1, copied bytes); the viewer's own decoder
// turns it into a small JPEG kept in memory. One hidden <video> does all the
// decoding, one file at a time, newest request first: phones have few
// hardware decoders, and a <video> per picture kept them busy (pictures
// late, playback slow to start). The decoder is let go as soon as the queue
// is empty so the player gets it.

const MAX = 80; // ~25 KB each
const WIDTH = 480;
const TIMEOUT = 8000;

const frames = new Map<string, string>(); // url → data: URL, oldest first
const failed = new Set<string>();
const listeners = new Set<() => void>();
let next: string | null = null;
let busy = false;
let video: HTMLVideoElement | null = null;

/** The preview request for the picture at ms, or null outside recordings. */
export function previewUrl(segments: Segment[], ms: number): string | null {
  const hit = findSegment(segments, ms);
  if (!hit || new Date(hit.seg.start).getTime() > ms) return null;
  // Rounded to 2 s so nearby positions share one picture.
  return `/api/recordings/${hit.seg.id}?at=${(Math.floor(hit.offset / 2) * 2).toFixed(0)}&gop=1`;
}

export function frameFor(url: string | null): string | undefined {
  return url ? frames.get(url) : undefined;
}

export function frameFailed(url: string | null): boolean {
  return !!url && failed.has(url);
}

export function onFrames(fn: () => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

/** Asks for the picture; a newer ask replaces one still waiting. */
export function wantFrame(url: string) {
  if (frames.has(url) || failed.has(url)) return;
  next = url;
  if (!busy) void pump();
}

async function pump() {
  busy = true;
  while (next) {
    const url = next;
    next = null;
    if (frames.has(url) || failed.has(url)) continue;
    try {
      remember(url, await decode(url));
    } catch {
      failed.add(url);
    }
    listeners.forEach((fn) => fn());
  }
  busy = false;
  release();
}

function remember(url: string, data: string) {
  frames.set(url, data);
  if (frames.size > MAX) frames.delete(frames.keys().next().value!);
}

function release() {
  if (!video) return;
  video.removeAttribute("src");
  video.load();
}

function decode(url: string): Promise<string> {
  const v = (video ??= Object.assign(document.createElement("video"), { muted: true, playsInline: true, preload: "auto" }));
  return new Promise((resolve, reject) => {
    let done = false;
    const finish = (err?: Error) => {
      if (done) return;
      done = true;
      window.clearTimeout(timer);
      v.onloadeddata = v.onerror = null;
      v.pause();
      if (err) return reject(err);
      try {
        resolve(snap(v));
      } catch (e) {
        reject(e as Error);
      }
    };
    const timer = window.setTimeout(() => finish(new Error("timeout")), TIMEOUT);
    v.onerror = () => finish(new Error("decode"));
    v.onloadeddata = () => finish();
    v.src = url;
    v.load();
    // Android WebViews may not fetch beyond metadata for preload alone;
    // muted playback is always allowed and makes it decode the keyframe.
    void v.play().catch(() => {});
  });
}

function snap(v: HTMLVideoElement): string {
  if (!v.videoWidth || !v.videoHeight) throw new Error("no picture");
  const w = Math.min(WIDTH, v.videoWidth);
  const c = document.createElement("canvas");
  c.width = w;
  c.height = Math.round((v.videoHeight / v.videoWidth) * w);
  c.getContext("2d")!.drawImage(v, 0, 0, c.width, c.height);
  return c.toDataURL("image/jpeg", 0.72);
}
