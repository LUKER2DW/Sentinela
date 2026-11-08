// Recordings in H.265 for browsers that cannot decode it (ADR-0026). The file
// is the server's own fragmented MP4, unchanged; this browser reads it box by
// box and feeds MediaSource fragment by fragment, where the converter
// (hevcFallback.ts) turns each GOP into H.264 before it is shown.

import { takeConversionSlot } from "./hevcFallback";

/** Top-level MP4 boxes read so far: complete ones are returned, the rest
 *  waits for more bytes. */
export function splitBoxes(buf: Uint8Array): { boxes: { type: string; bytes: Uint8Array }[]; rest: Uint8Array } {
  const boxes: { type: string; bytes: Uint8Array }[] = [];
  let off = 0;
  const view = new DataView(buf.buffer, buf.byteOffset, buf.byteLength);
  while (buf.length - off >= 8) {
    let size = view.getUint32(off);
    const type = String.fromCharCode(buf[off + 4], buf[off + 5], buf[off + 6], buf[off + 7]);
    let header = 8;
    if (size === 1) {
      if (buf.length - off < 16) break;
      size = Number(view.getBigUint64(off + 8));
      header = 16;
    } else if (size === 0) {
      break; // "to the end of the file": not used by these recordings
    }
    if (size < header || buf.length - off < size) break;
    boxes.push({ type, bytes: buf.subarray(off, off + size) });
    off += size;
  }
  return { boxes, rest: buf.subarray(off) };
}

const has = (b: Uint8Array, fourcc: string) => {
  const c = [...fourcc].map((x) => x.charCodeAt(0));
  for (let i = 0; i + 4 <= b.length; i++) if (b[i] === c[0] && b[i + 1] === c[1] && b[i + 2] === c[2] && b[i + 3] === c[3]) return true;
  return false;
};

/** The MSE mime of an init segment (ftyp + moov), "" when it is not H.265. */
export function mimeOf(init: Uint8Array): string {
  if (!has(init, "hvc1") && !has(init, "hev1")) return "";
  const codecs = ["hvc1.1.6.L153.B0"];
  if (has(init, "mp4a")) codecs.push("mp4a.40.2");
  else if (has(init, "Opus")) codecs.push("opus");
  else if (has(init, "fLaC")) codecs.push("flac");
  return `video/mp4; codecs="${codecs.join(",")}"`;
}

/** How far ahead of the playhead to convert: enough to play smoothly,
 *  little enough not to spend the device on what may never be watched. */
const AHEAD_S = 20;

/**
 * Plays url (a recording, fragmented MP4) in video through the converter.
 * Resolves once started; the returned function stops everything. Throws when
 * this device cannot convert now (no slot, not HTTPS, no H.264 encoder) or
 * the file is not H.265.
 */
export async function playConverted(video: HTMLVideoElement, url: string, durationS?: number): Promise<() => void> {
  const slot = await takeConversionSlot();
  if (!slot) throw new Error("conversion unavailable");
  const abort = new AbortController();
  const ms = new MediaSource();
  const objectUrl = URL.createObjectURL(ms);
  let stopped = false;
  const stop = () => {
    if (stopped) return;
    stopped = true;
    abort.abort();
    slot.release();
    if (ms.readyState === "open") {
      try {
        ms.endOfStream();
      } catch {
        /* closing */
      }
    }
    URL.revokeObjectURL(objectUrl);
  };
  video.src = objectUrl;
  try {
    await new Promise<void>((ok) => ms.addEventListener("sourceopen", () => ok(), { once: true }));
    if (durationS && durationS > 0) ms.duration = durationS;
    const resp = await fetch(url, { signal: abort.signal, credentials: "same-origin" });
    if (!resp.ok || !resp.body) throw new Error(`HTTP ${resp.status}`);
    const reader = resp.body.getReader();
    let pending: Uint8Array = new Uint8Array(0);
    let sb: SourceBuffer | null = null;
    let init: Uint8Array[] = [];
    let frag: Uint8Array[] = [];

    const append = (parts: Uint8Array[]) =>
      new Promise<void>((ok, fail) => {
        const total = parts.reduce((n, p) => n + p.length, 0);
        const data = new Uint8Array(total);
        let o = 0;
        for (const p of parts) {
          data.set(p, o);
          o += p.length;
        }
        const s = sb!;
        const done = () => {
          s.removeEventListener("updateend", done);
          s.removeEventListener("error", err);
          ok();
        };
        const err = () => {
          s.removeEventListener("updateend", done);
          s.removeEventListener("error", err);
          fail(new Error("conversion failed"));
        };
        s.addEventListener("updateend", done);
        s.addEventListener("error", err);
        s.appendBuffer(data);
      });
    // Waits while enough is converted ahead of the playhead.
    const backpressure = async () => {
      while (!stopped && sb && sb.buffered.length && sb.buffered.end(sb.buffered.length - 1) - video.currentTime > AHEAD_S) {
        await new Promise((r) => setTimeout(r, 500));
      }
    };

    const pump = async () => {
      for (;;) {
        const { value, done } = await reader.read();
        if (stopped) return;
        if (value) {
          const joined = new Uint8Array(pending.length + value.length);
          joined.set(pending);
          joined.set(value, pending.length);
          const { boxes, rest } = splitBoxes(joined);
          pending = rest;
          for (const b of boxes) {
            if (!sb) {
              init.push(b.bytes);
              if (b.type !== "moov") continue;
              const all = new Uint8Array(init.reduce((n, p) => n + p.length, 0));
              let o = 0;
              for (const p of init) {
                all.set(p, o);
                o += p.length;
              }
              const mime = mimeOf(all);
              if (!mime) throw new Error("not H.265");
              sb = ms.addSourceBuffer(mime);
              sb.mode = "segments";
              await append(init);
              init = [];
              continue;
            }
            // One fragment = moof + mdat (a GOP: frag_keyframe).
            frag.push(b.bytes);
            if (b.type === "mdat") {
              await backpressure();
              if (stopped) return;
              await append(frag);
              frag = [];
            }
          }
        }
        if (done) break;
      }
      if (frag.length && sb) await append(frag);
      if (!stopped && ms.readyState === "open") ms.endOfStream();
    };
    void pump().catch(() => {
      if (!stopped) video.dispatchEvent(new Event("error"));
      stop();
    });
  } catch (e) {
    stop();
    throw e;
  }
  return stop;
}
