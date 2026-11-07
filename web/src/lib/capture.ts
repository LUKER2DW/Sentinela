
import { t } from "./i18n";// Client-side captures from a playing <video>: a still image and a local
// recording. Both run in the viewer's browser (its own decoder and encoder);
// the server never decodes video (AGENTS.md §2.1).

/** File-name friendly local time: 2026-09-29_18h12m05. */
export function stamp(d = new Date()): string {
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}_${p(d.getHours())}h${p(d.getMinutes())}m${p(d.getSeconds())}`;
}

/** Saves a blob on the device: the share sheet in the app, a download elsewhere. */
export async function saveBlob(blob: Blob, name: string): Promise<void> {
  const nav = navigator as Navigator & { canShare?: (d: ShareData) => boolean };
  // The Android WebView cannot download blob: URLs; its share sheet can save them.
  if (/SentinelaApp/.test(navigator.userAgent) && nav.share && nav.canShare) {
    const file = new File([blob], name, { type: blob.type });
    if (nav.canShare({ files: [file] })) {
      await nav.share({ files: [file], title: name });
      return;
    }
  }
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  document.body.append(a);
  a.click();
  a.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 30_000);
}

/** The frame on screen as a JPEG at the stream's full resolution. */
export async function grabFrame(video: HTMLVideoElement): Promise<Blob> {
  const w = video.videoWidth;
  const h = video.videoHeight;
  if (!w || !h) throw new Error(t("The video has not shown any picture yet."));
  const canvas = document.createElement("canvas");
  canvas.width = w;
  canvas.height = h;
  canvas.getContext("2d")!.drawImage(video, 0, 0, w, h);
  return new Promise((resolve, reject) =>
    canvas.toBlob((b) => (b ? resolve(b) : reject(new Error(t("Could not create the picture.")))), "image/jpeg", 0.92),
  );
}

/** Draws the current frame into a canvas (a freeze-frame while reconnecting). */
export function paintFrame(video: HTMLVideoElement, canvas: HTMLCanvasElement): boolean {
  const w = video.videoWidth;
  const h = video.videoHeight;
  if (!w || !h || video.readyState < 2) return false;
  canvas.width = w;
  canvas.height = h;
  try {
    canvas.getContext("2d")!.drawImage(video, 0, 0, w, h);
    return true;
  } catch {
    return false;
  }
}

// Preferred first: MP4 (H.264) plays everywhere; WebM is the fallback.
const TYPES = ["video/mp4;codecs=avc1.640028,mp4a.40.2", "video/mp4;codecs=avc1", "video/mp4", "video/webm;codecs=vp9,opus", "video/webm;codecs=vp8,opus", "video/webm"];

export function recorderType(isSupported: (t: string) => boolean = (t) => typeof MediaRecorder !== "undefined" && MediaRecorder.isTypeSupported(t)): string | null {
  return TYPES.find((t) => isSupported(t)) ?? null;
}

type Capturable = HTMLVideoElement & { captureStream?: () => MediaStream; mozCaptureStream?: () => MediaStream };

export function canRecord(video?: HTMLVideoElement | null): boolean {
  if (typeof MediaRecorder === "undefined" || !recorderType()) return false;
  const v = (video ?? HTMLVideoElement.prototype) as Capturable;
  return typeof v.captureStream === "function" || typeof v.mozCaptureStream === "function";
}

/** Records what the <video> shows until stop(); resolves with the file. */
export class ClipRecorder {
  private rec: MediaRecorder;
  private chunks: Blob[] = [];
  private done: Promise<Blob>;
  readonly type: string;
  readonly startedAt = Date.now();

  constructor(video: HTMLVideoElement) {
    const v = video as Capturable;
    const stream = v.captureStream ? v.captureStream() : v.mozCaptureStream!();
    this.type = recorderType()!;
    // ~8 Mbit/s keeps a 2K picture sharp without huge files (≈ 60 MB/min).
    this.rec = new MediaRecorder(stream, { mimeType: this.type, videoBitsPerSecond: 8_000_000 });
    this.rec.ondataavailable = (e) => e.data.size && this.chunks.push(e.data);
    this.done = new Promise((resolve, reject) => {
      this.rec.onstop = () => resolve(new Blob(this.chunks, { type: this.type.split(";")[0] }));
      this.rec.onerror = () => reject(new Error(t("The local recording failed.")));
    });
    this.rec.start(1000);
  }

  get extension(): string {
    return this.type.startsWith("video/mp4") ? "mp4" : "webm";
  }

  stop(): Promise<Blob> {
    if (this.rec.state !== "inactive") this.rec.stop();
    return this.done;
  }
}
