// Viewing H.265 in browsers that cannot decode it (ADR-0026). The server
// keeps recording and serving H.265 untouched; only this browser converts,
// for display: @hevcjs/core (MIT, github.com/lid-labs/hevc.js) decodes H.265
// in WebAssembly and re-encodes to H.264 with the browser's own encoder
// (WebCodecs), behind MediaSource. It is loaded only when needed.

/** The MSE mime check for H.265, before any interception. */
const HEVC_PROBE = 'video/mp4; codecs="hvc1.1.6.L153.B0"';
const H264_ENCODE = { codec: "avc1.42E01F", width: 1280, height: 720, bitrate: 2_000_000, framerate: 15 };

/** The browser's own MSE support for a mime, ignoring the converter (which
 *  answers "yes" for H.265 once installed). */
export function nativeSupported(mime: string): boolean {
  const w = window as unknown as { ManagedMediaSource?: typeof MediaSource; MediaSource?: typeof MediaSource };
  const MS = w.ManagedMediaSource ?? w.MediaSource;
  if (!MS) return false;
  try {
    return state.originalSupported && MS === w.MediaSource ? state.originalSupported(mime) : MS.isTypeSupported(mime);
  } catch {
    return false;
  }
}

/** The browser plays H.265 itself (MSE, without hevc.js). */
export function nativeHevc(): boolean {
  const w = window as unknown as { ManagedMediaSource?: typeof MediaSource; MediaSource?: typeof MediaSource };
  const MS = w.ManagedMediaSource ?? w.MediaSource;
  if (!MS) return false;
  try {
    return state.originalSupported ? state.originalSupported(HEVC_PROBE) : MS.isTypeSupported(HEVC_PROBE);
  } catch {
    return false;
  }
}

interface State {
  checked?: Promise<boolean>;
  installed?: Promise<void>;
  originalSupported?: (t: string) => boolean;
  active: number;
}
const state: State = { active: 0 };

/** How many streams this device may convert at once: decoding H.265 in
 *  software is heavy, so the rest of a grid uses the cameras' H.264
 *  substream (or waits). */
export function conversionSlots(): number {
  const cores = navigator.hardwareConcurrency || 2;
  const phone = /Android|iPhone|iPad|Mobile/i.test(navigator.userAgent);
  if (phone || cores <= 4) return 1;
  return cores >= 8 ? 3 : 2;
}

/** Whether this browser can convert at all: secure page (HTTPS or the app),
 *  Workers, WebAssembly and an H.264 encoder in WebCodecs. */
export function canConvert(): Promise<boolean> {
  state.checked ??= (async () => {
    if (!window.isSecureContext || typeof Worker === "undefined" || typeof WebAssembly === "undefined") return false;
    if (typeof MediaSource === "undefined") return false;
    const VE = (window as unknown as { VideoEncoder?: { isConfigSupported(c: object): Promise<{ supported?: boolean }> } }).VideoEncoder;
    if (!VE) return false;
    try {
      return !!(await VE.isConfigSupported(H264_ENCODE)).supported;
    } catch {
      return false;
    }
  })();
  return state.checked;
}

/** Installs the converter once (lazy chunk, worker and WASM from /hevc/). */
function install(): Promise<void> {
  state.installed ??= (async () => {
    const orig = MediaSource.isTypeSupported.bind(MediaSource);
    // The WASM loader is a classic script: loaded once on this page it
    // defines HEVCDecoderModule, which hevc.js uses for work it does on the
    // main thread (the worker loads its own copy).
    if (typeof (globalThis as { HEVCDecoderModule?: unknown }).HEVCDecoderModule !== "function") {
      await new Promise<void>((ok, fail) => {
        const s = document.createElement("script");
        s.src = "/hevc/hevc-decode.js";
        s.onload = () => ok();
        s.onerror = () => fail(new Error("could not load the H.265 converter"));
        document.head.appendChild(s);
      });
    }
    const { installMSEIntercept } = await import("@hevcjs/core");
    state.originalSupported = orig;
    installMSEIntercept({
      workerUrl: "/hevc/transcode-worker.js",
      wasmUrl: "/hevc/hevc-decode.js",
      wasmBinaryUrl: "/hevc/hevc-decode.wasm",
      poolSize: 1,
      logLevel: "warn",
    });
  })();
  return state.installed;
}

/** After a conversion failed (an encoder that would not start, a worker
 *  that died), the converter is reinstalled so the next try gets fresh
 *  workers. Only when no other stream is converting at that moment. */
export async function resetConverter(): Promise<void> {
  if (!state.installed || state.active > 0) return;
  try {
    await state.installed;
    const { uninstallMSEIntercept } = await import("@hevcjs/core");
    uninstallMSEIntercept();
  } catch {
    /* not installed */
  }
  state.installed = undefined;
}

/** A conversion slot for one player; release() when the player stops. */
export interface ConversionSlot {
  release(): void;
}

/** Takes a slot and makes sure the converter is ready; null when the device
 *  cannot convert or every slot is busy. */
export async function takeConversionSlot(): Promise<ConversionSlot | null> {
  if (nativeHevc() || !(await canConvert()) || state.active >= conversionSlots()) return null;
  state.active++;
  let held = true;
  const slot = {
    release() {
      if (held) {
        held = false;
        state.active--;
      }
    },
  };
  try {
    await install();
  } catch {
    slot.release();
    return null;
  }
  return slot;
}
