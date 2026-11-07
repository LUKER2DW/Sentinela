// Live player speaking go2rtc's WebSocket protocol (MIT, github.com/AlexxIT/go2rtc;
// protocol adapted, see NOTICE). Order: WebRTC if the browser can decode H.265
// over WebRTC, otherwise MSE (fragmented MP4 over the same socket).
// The socket URL comes from a single-use ticket issued by the Sentinela API.

import { api } from "./api";
import { getZone } from "./access";

import { t } from "./i18n";
export type Transport = "webrtc" | "mse";
export type PlayerState = "connecting" | "playing" | "reconnecting" | "error" | "stopped";

export interface PlayerStatus {
  state: PlayerState;
  transport?: Transport;
  message?: string;
}

export interface PlayerOptions {
  cameraId: string;
  stream: "main" | "sub";
  /** Force a transport (debug / user choice). */
  prefer?: Transport;
  /** Send the local microphone (two-way audio). Needs a secure context. */
  talk?: boolean;
  onStatus?: (s: PlayerStatus) => void;
  /** Called just before the picture is dropped (reconnect, stop): the view
   * keeps the last frame on screen instead of a black flash. */
  onTeardown?: () => void;
}

const MSE_CODECS = [
  "avc1.640029",
  "avc1.64002A",
  "avc1.640033",
  "hvc1.1.6.L153.B0",
  "mp4a.40.2",
  "mp4a.40.5",
  "flac",
  "opus",
];

type MSClass = typeof MediaSource;

function mediaSourceClass(): MSClass | null {
  const w = window as unknown as { ManagedMediaSource?: MSClass; MediaSource?: MSClass };
  return w.ManagedMediaSource ?? w.MediaSource ?? null;
}

/** Codecs this browser can play through MSE, in go2rtc's comma format. */
export function supportedMseCodecs(isSupported: (t: string) => boolean): string {
  return MSE_CODECS.filter((c) => isSupported(`video/mp4; codecs="${c}"`)).join(",");
}

/** True if the browser advertises H.265 for WebRTC receive. */
export function webrtcSupportsH265(): boolean {
  if (typeof RTCRtpReceiver === "undefined" || !RTCRtpReceiver.getCapabilities) return false;
  const caps = RTCRtpReceiver.getCapabilities("video");
  return !!caps?.codecs.some((c) => c.mimeType.toLowerCase() === "video/h265");
}

// A temporary network failure must not disable the faster path for a week,
// or follow the viewer from Funnel to LAN. Old unscoped failures are ignored.
const transportKey = () => `sentinela.webrtcFailedAt.${getZone()}`;
const TRANSPORT_TTL = 10 * 60_000;
const MAX_QUEUE_MS = 2_000;
const MAX_QUEUE_BYTES = 8 * 1024 * 1024;

// Live-edge guard for WebRTC. The camera's Wi-Fi delivers every 150 KB
// keyframe 0.35–0.7 s late (measured 2026-11-06); Chrome's adaptive jitter
// buffer answers by growing without bound (0.3 s → 2 s in under a minute,
// and jitterBufferTarget cannot cap it). Above this delay for
// LAG_SAMPLES checks in a row, the view moves to MSE, whose catch-up keeps
// the delay bounded.
export const WEBRTC_MAX_DELAY = 1.0;
const LAG_CHECK_MS = 2_000;
const LAG_SAMPLES = 3;

// MSE live edge: seek when further behind than MSE_SEEK_LAG, keep about
// MSE_CUSHION buffered (at the very edge every late fragment is a stall).
export const MSE_SEEK_LAG = 1.2;
const MSE_CUSHION = 0.3;

/** Playback rate that drains an MSE backlog of `lag` seconds. */
export function catchUpRate(lag: number): number {
  return lag > 0.7 ? 1.12 : lag > 0.45 ? 1.05 : 1;
}

/** True when the last `n` delay samples all exceed `limit` (null = unknown). */
export function persistentlyLate(samples: (number | null)[], limit = WEBRTC_MAX_DELAY, n = LAG_SAMPLES): boolean {
  if (samples.length < n) return false;
  return samples.slice(-n).every((s) => s !== null && s > limit);
}

export function webrtcRecentlyFailed(now = Date.now()): boolean {
  try {
    const at = Number(localStorage.getItem(transportKey()) ?? 0);
    return at > 0 && now >= at && now - at < TRANSPORT_TTL;
  } catch {
    return false;
  }
}

function rememberWebrtc(ok: boolean) {
  try {
    if (ok) localStorage.removeItem(transportKey());
    else localStorage.setItem(transportKey(), String(Date.now()));
  } catch {
    /* private mode: probe again next time */
  }
}

export function canTalk(): boolean {
  // Talk rides WebRTC, which Tailscale Funnel cannot carry (ADR-0019).
  return window.isSecureContext && !!navigator.mediaDevices?.getUserMedia && getZone() !== "funnel";
}

export class LivePlayer {
  private video: HTMLVideoElement;
  private opts: PlayerOptions;
  private ws?: WebSocket;
  private pc?: RTCPeerConnection;
  private ms?: MediaSource;
  private sb?: SourceBuffer;
  private queue: { data: ArrayBuffer; at: number }[] = [];
  private queueBytes = 0;
  private mic?: MediaStream;
  private stopped = false;
  private retry = 0;
  private retryTimer?: number;
  private fallbackTimer?: number;
  private objectUrl?: string;
  private transport?: Transport;
  private webrtcFailedAt = 0;
  private lagTimer?: number;
  private lagSamples: (number | null)[] = [];
  private micOn = false;
  // Bumped by every connect/teardown: a connect still waiting for its ticket
  // when a newer one starts gives up instead of opening a second socket.
  private gen = 0;

  constructor(video: HTMLVideoElement, opts: PlayerOptions) {
    this.video = video;
    this.opts = opts;
  }

  get currentTransport() {
    return this.transport;
  }

  get peer() {
    return this.pc;
  }

  /** Push-to-talk: the mic track only carries sound while this is on. */
  setMic(on: boolean) {
    this.micOn = on;
    this.mic?.getAudioTracks().forEach((t) => (t.enabled = on));
  }

  start() {
    this.stopped = false;
    void this.connect();
  }

  /** Current state (the last one emitted). */
  get state(): PlayerState {
    return this.lastState;
  }
  private lastState: PlayerState = "connecting";

  /** Reopens the stream from a fresh keyframe (backlog after a hidden tab, stalls). */
  restart() {
    if (this.stopped) return;
    window.clearTimeout(this.retryTimer);
    this.retry = 0;
    void this.connect();
  }

  stop() {
    this.stopped = true;
    window.clearTimeout(this.retryTimer);
    this.teardown();
    this.emit({ state: "stopped" });
  }

  private emit(s: PlayerStatus) {
    this.lastState = s.state;
    this.opts.onStatus?.(s);
  }

  private pickTransport(): Transport {
    if (this.opts.talk) return "webrtc";
    if (this.opts.prefer) return this.opts.prefer;
    // Tailscale Funnel carries TCP only: no path for WebRTC media (ADR-0019).
    if (getZone() === "funnel") return "mse";
    const sinceFailure = Date.now() - this.webrtcFailedAt;
    const coolingDown = this.webrtcFailedAt > 0 && sinceFailure >= 0 && sinceFailure < TRANSPORT_TTL;
    if (!coolingDown && !webrtcRecentlyFailed() && webrtcSupportsH265()) return "webrtc";
    return "mse";
  }

  private async connect() {
    this.teardown();
    const gen = this.gen;
    this.emit({ state: this.retry ? "reconnecting" : "connecting" });
    let url: string;
    try {
      const t = await api<{ url: string }>(
        "POST",
        `/api/cameras/${encodeURIComponent(this.opts.cameraId)}/stream-ticket?stream=${this.opts.stream}`,
      );
      url = t.url;
    } catch (e) {
      if (gen === this.gen) this.scheduleRetry((e as Error).message);
      return;
    }
    if (this.stopped || gen !== this.gen) return;
    const proto = location.protocol === "https:" ? "wss:" : "ws:";
    const ws = new WebSocket(`${proto}//${location.host}${url}`);
    ws.binaryType = "arraybuffer";
    this.ws = ws;
    const transport = this.pickTransport();
    this.transport = transport;
    ws.onopen = () => {
      if (this.ws !== ws) return;
      if (transport === "webrtc") void this.startWebRTC();
      else this.startMSE();
    };
    ws.onmessage = (ev) => {
      // A closing socket still delivers what was in flight: its fragments
      // belong to the old stream and would corrupt the new SourceBuffer.
      if (this.ws !== ws) return;
      if (typeof ev.data !== "string") {
        this.appendMSE(ev.data as ArrayBuffer);
        return;
      }
      let msg: { type: string; value: string };
      try {
        msg = JSON.parse(ev.data);
      } catch {
        return;
      }
      void this.onMessage(msg);
    };
    ws.onclose = () => {
      if (this.ws === ws && !this.stopped) this.scheduleRetry(t("connection closed"));
    };
  }

  private send(msg: { type: string; value: string }) {
    if (this.ws?.readyState === WebSocket.OPEN) this.ws.send(JSON.stringify(msg));
  }

  private async onMessage(msg: { type: string; value: string }) {
    switch (msg.type) {
      case "webrtc/answer":
        {
          const pc = this.pc;
          try {
            await pc?.setRemoteDescription({ type: "answer", sdp: msg.value });
          } catch {
            if (pc && pc === this.pc) this.scheduleRetry(t("video negotiation failed"));
          }
        }
        break;
      case "webrtc/candidate":
        if (msg.value) await this.pc?.addIceCandidate({ candidate: msg.value, sdpMid: "0" }).catch(() => {});
        break;
      case "mse":
        this.openSourceBuffer(msg.value);
        break;
      case "error":
        if (this.transport === "webrtc" && !this.opts.talk) {
          this.fallbackToMSE();
        } else {
          this.scheduleRetry(msg.value);
        }
        break;
    }
  }

  // ---------- WebRTC ----------

  private async startWebRTC() {
    // No external STUN: LAN / tailnet host candidates are enough and private.
    const pc = new RTCPeerConnection({ bundlePolicy: "max-bundle", iceServers: [] });
    this.pc = pc;
    pc.addTransceiver("video", { direction: "recvonly" });
    if (this.opts.talk && canTalk()) {
      try {
        const mic = await navigator.mediaDevices.getUserMedia({
          audio: { echoCancellation: true, noiseSuppression: true, autoGainControl: true },
        });
        if (pc !== this.pc) {
          mic.getTracks().forEach((t) => t.stop());
          return;
        }
        this.mic = mic;
        this.setMic(this.micOn);
        pc.addTransceiver(this.mic.getAudioTracks()[0], { direction: "sendrecv" });
      } catch {
        if (pc !== this.pc) return;
        this.emit({ state: "error", message: t("Microphone blocked by the browser") });
        pc.addTransceiver("audio", { direction: "recvonly" });
      }
    } else {
      pc.addTransceiver("audio", { direction: "recvonly" });
    }
    const stream = new MediaStream();
    pc.ontrack = (ev) => {
      if (pc !== this.pc) return;
      // A small jitter buffer (150 ms): with none, Wi-Fi jitter made the 2K
      // picture stutter and smear; this costs a fraction of the delay.
      const r = ev.receiver as unknown as { jitterBufferTarget?: number | null; playoutDelayHint?: number };
      try {
        if (r.jitterBufferTarget !== undefined) r.jitterBufferTarget = 150;
        else r.playoutDelayHint = 0.15;
      } catch {
        /* not supported: default buffer */
      }
      stream.addTrack(ev.track);
      if (this.video.srcObject !== stream) this.video.srcObject = stream;
    };
    pc.onicecandidate = (ev) => {
      if (pc !== this.pc) return;
      this.send({ type: "webrtc/candidate", value: ev.candidate ? ev.candidate.candidate : "" });
    };
    pc.onconnectionstatechange = () => {
      if (pc !== this.pc) return;
      if (pc.connectionState === "connected") {
        window.clearTimeout(this.fallbackTimer);
        this.retry = 0;
        void this.video.play().catch(() => {});
        rememberWebrtc(true);
        this.watchWebrtcLag(pc);
        this.emit({ state: "playing", transport: "webrtc" });
      } else if (pc.connectionState === "failed") {
        if (!this.opts.talk) this.fallbackToMSE();
        else this.scheduleRetry("WebRTC failed");
      }
    };
    try {
      const offer = await pc.createOffer();
      if (pc !== this.pc) return;
      await pc.setLocalDescription(offer);
      if (pc !== this.pc) return;
    } catch {
      if (pc === this.pc) {
        if (!this.opts.talk) this.fallbackToMSE();
        else this.scheduleRetry(t("video negotiation failed"));
      }
      return;
    }
    this.send({ type: "webrtc/offer", value: pc.localDescription!.sdp });
    // If no media path within 6 s (firewall, codec), use MSE.
    this.fallbackTimer = window.setTimeout(() => {
      if (pc === this.pc && pc.connectionState !== "connected" && !this.opts.talk) this.fallbackToMSE();
    }, 6000);
  }

  /** Moves a WebRTC view whose jitter buffer keeps growing to MSE (not while talking: talk needs WebRTC). */
  private watchWebrtcLag(pc: RTCPeerConnection) {
    window.clearTimeout(this.lagTimer);
    this.lagSamples = [];
    if (this.opts.talk || this.opts.prefer === "webrtc") return;
    const check = () => {
      this.lagTimer = window.setTimeout(() => {
        if (pc !== this.pc) return;
        void this.bufferDelay().then((d) => {
          if (pc !== this.pc) return;
          this.lagSamples = [...this.lagSamples.slice(-(LAG_SAMPLES - 1)), d];
          if (persistentlyLate(this.lagSamples)) this.fallbackToMSE();
          else check();
        });
      }, LAG_CHECK_MS);
    };
    check();
  }

  private fallbackToMSE() {
    this.webrtcFailedAt = Date.now();
    rememberWebrtc(false);
    this.retry = 0;
    void this.connect();
  }

  // ---------- MSE ----------

  private startMSE() {
    const MS = mediaSourceClass();
    if (!MS) {
      this.emit({ state: "error", message: t("This browser cannot play live video (no MSE)") });
      return;
    }
    const codecs = supportedMseCodecs((t) => MS.isTypeSupported(t));
    if (!codecs.includes("hvc1")) {
      this.emit({
        state: "error",
        message: t("This browser cannot decode H.265. Use an up-to-date Chrome, Edge or Safari."),
      });
      this.stopped = true;
      this.ws?.close();
      return;
    }
    this.send({ type: "mse", value: codecs });
  }

  private openSourceBuffer(mime: string) {
    if (this.ms) return;
    const MS = mediaSourceClass()!;
    const ms = new MS();
    this.ms = ms;
    const v = this.video as HTMLVideoElement & { disableRemotePlayback: boolean };
    v.disableRemotePlayback = true; // required by ManagedMediaSource (Safari)
    this.video.srcObject = null;
    this.objectUrl = URL.createObjectURL(ms);
    this.video.src = this.objectUrl;
    ms.addEventListener(
      "sourceopen",
      () => {
        if (ms !== this.ms) return;
        try {
          const sb = ms.addSourceBuffer(mime);
          sb.mode = "segments";
          sb.addEventListener("updateend", () => {
            if (sb === this.sb) this.pump();
          });
          sb.addEventListener("error", () => {
            if (sb === this.sb) this.scheduleRetry(t("invalid video fragment"));
          });
          this.sb = sb;
          this.pump();
          void this.video.play().catch(() => {});
          this.retry = 0;
          this.emit({ state: "playing", transport: "mse" });
        } catch {
          this.scheduleRetry(t("codec not supported"));
        }
      },
      { once: true },
    );
  }

  private appendMSE(buf: ArrayBuffer) {
    if (this.transport !== "mse") return;
    this.queue.push({ data: buf, at: performance.now() });
    this.queueBytes += buf.byteLength;
    // Dropping fragments mid-GOP smears every frame up to the next keyframe:
    // a backlog this long reopens the stream from a fresh keyframe instead.
    if (this.queueBytes > MAX_QUEUE_BYTES || performance.now() - this.queue[0].at > MAX_QUEUE_MS) {
      this.restart();
      return;
    }
    this.pump();
  }

  private pump() {
    const sb = this.sb;
    if (!sb || sb.updating) return;
    try {
      const buffered = sb.buffered;
      if (buffered.length) {
        const end = buffered.end(buffered.length - 1);
        const start = buffered.start(0);
        const latestStart = buffered.start(buffered.length - 1);
        // Stay near the live edge: play a little faster while behind and
        // jump only when far behind. A small cushion stays buffered: at the
        // very edge every late fragment is a visible stall.
        const lag = end - this.video.currentTime;
        if (lag > MSE_SEEK_LAG || this.video.currentTime < latestStart) {
          // Seek within the intact buffered GOP; never throw away reference frames.
          this.video.currentTime = Math.max(latestStart, end - MSE_CUSHION);
          this.video.playbackRate = 1;
        } else this.video.playbackRate = catchUpRate(lag);
        // Keep ~10 s of history so memory stays flat.
        if (end - start > 20 && !sb.updating) {
          sb.remove(start, end - 10);
          return;
        }
      }
      const next = this.queue.shift();
      if (next) {
        if (performance.now() - next.at > MAX_QUEUE_MS) {
          this.restart();
          return;
        }
        this.queueBytes -= next.data.byteLength;
        sb.appendBuffer(next.data);
      }
    } catch {
      // QuotaExceeded or a detached buffer: appending the rest would skip
      // frames the next ones depend on. Start over from a keyframe.
      this.restart();
    }
  }

  /**
   * Seconds of video the browser holds before showing it: the MSE buffer
   * ahead of the playhead, or the WebRTC jitter buffer. Camera encoding and
   * network add a little on top; this is the part the player controls.
   */
  async bufferDelay(): Promise<number | null> {
    if (this.transport === "mse") {
      const b = this.sb?.buffered;
      if (!b || !b.length) return null;
      return Math.max(0, b.end(b.length - 1) - this.video.currentTime);
    }
    const pc = this.pc;
    if (!pc) return null;
    const stats = await pc.getStats();
    if (pc !== this.pc) return null;
    let out: number | null = null;
    stats.forEach((st) => {
      const s = st as { type: string; kind?: string; jitterBufferDelay?: number; jitterBufferEmittedCount?: number };
      if (s.type === "inbound-rtp" && s.kind === "video" && s.jitterBufferEmittedCount) {
        const d = (s.jitterBufferDelay ?? 0) / s.jitterBufferEmittedCount;
        const prev = this.jb;
        this.jb = { delay: s.jitterBufferDelay ?? 0, count: s.jitterBufferEmittedCount };
        // Delta since the last sample: current buffering, not the lifetime average.
        out = prev && this.jb.count > prev.count ? (this.jb.delay - prev.delay) / (this.jb.count - prev.count) : d;
      }
    });
    return out;
  }
  private jb?: { delay: number; count: number };

  // ---------- lifecycle ----------

  private scheduleRetry(message: string) {
    if (this.stopped) return;
    window.clearTimeout(this.retryTimer);
    this.teardown();
    this.retry++;
    const delay = Math.min(30_000, 1000 * 2 ** Math.min(this.retry, 5));
    this.emit({ state: "reconnecting", message });
    this.retryTimer = window.setTimeout(() => void this.connect(), delay);
  }

  private teardown() {
    this.gen++;
    window.clearTimeout(this.fallbackTimer);
    window.clearTimeout(this.lagTimer);
    this.lagSamples = [];
    if (this.ws || this.pc || this.ms) this.opts.onTeardown?.();
    const ws = this.ws;
    this.ws = undefined;
    if (ws) {
      ws.onclose = null;
      ws.close();
    }
    this.pc?.close();
    this.pc = undefined;
    this.mic?.getTracks().forEach((t) => t.stop());
    this.mic = undefined;
    this.sb = undefined;
    this.queue = [];
    this.queueBytes = 0;
    const ms = this.ms;
    this.ms = undefined;
    if (ms && ms.readyState === "open") {
      try {
        ms.endOfStream();
      } catch {
        /* already closed */
      }
    }
    if (this.objectUrl) {
      URL.revokeObjectURL(this.objectUrl);
      this.objectUrl = undefined;
      this.video.removeAttribute("src");
      this.video.load();
    }
    this.video.srcObject = null;
    this.video.playbackRate = 1;
    this.jb = undefined;
  }
}
