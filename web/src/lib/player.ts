// Live player speaking go2rtc's WebSocket protocol (MIT, github.com/AlexxIT/go2rtc;
// protocol adapted, see NOTICE). Order: WebRTC if the browser can decode H.265
// over WebRTC, otherwise MSE (fragmented MP4 over the same socket).
// The socket URL comes from a single-use ticket issued by the Sentinela API.

import { api } from "./api";
import { getZone } from "./access";

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

// Per-device memory of a failed WebRTC path (no HEVC decoder, blocked UDP):
// skip straight to MSE for a week instead of waiting 6 s every time.
const TRANSPORT_KEY = "sentinela.webrtcFailedAt";
const TRANSPORT_TTL = 7 * 24 * 3_600_000;

export function webrtcRecentlyFailed(now = Date.now()): boolean {
  try {
    const at = Number(localStorage.getItem(TRANSPORT_KEY) ?? 0);
    return at > 0 && now - at < TRANSPORT_TTL;
  } catch {
    return false;
  }
}

function rememberWebrtc(ok: boolean) {
  try {
    if (ok) localStorage.removeItem(TRANSPORT_KEY);
    else localStorage.setItem(TRANSPORT_KEY, String(Date.now()));
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
  private queue: ArrayBuffer[] = [];
  private mic?: MediaStream;
  private stopped = false;
  private retry = 0;
  private retryTimer?: number;
  private fallbackTimer?: number;
  private objectUrl?: string;
  private transport?: Transport;
  private webrtcFailed = false;
  private micOn = false;

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

  stop() {
    this.stopped = true;
    window.clearTimeout(this.retryTimer);
    this.teardown();
    this.emit({ state: "stopped" });
  }

  private emit(s: PlayerStatus) {
    this.opts.onStatus?.(s);
  }

  private pickTransport(): Transport {
    if (this.opts.talk) return "webrtc";
    if (this.opts.prefer) return this.opts.prefer;
    // Tailscale Funnel carries TCP only: no path for WebRTC media (ADR-0019).
    if (getZone() === "funnel") return "mse";
    if (!this.webrtcFailed && !webrtcRecentlyFailed() && webrtcSupportsH265()) return "webrtc";
    return "mse";
  }

  private async connect() {
    this.teardown();
    this.emit({ state: this.retry ? "reconnecting" : "connecting" });
    let url: string;
    try {
      const t = await api<{ url: string }>(
        "POST",
        `/api/cameras/${encodeURIComponent(this.opts.cameraId)}/stream-ticket?stream=${this.opts.stream}`,
      );
      url = t.url;
    } catch (e) {
      this.scheduleRetry((e as Error).message);
      return;
    }
    if (this.stopped) return;
    const proto = location.protocol === "https:" ? "wss:" : "ws:";
    const ws = new WebSocket(`${proto}//${location.host}${url}`);
    ws.binaryType = "arraybuffer";
    this.ws = ws;
    const transport = this.pickTransport();
    this.transport = transport;
    ws.onopen = () => {
      if (transport === "webrtc") void this.startWebRTC();
      else this.startMSE();
    };
    ws.onmessage = (ev) => {
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
      if (this.ws === ws && !this.stopped) this.scheduleRetry("conexão encerrada");
    };
  }

  private send(msg: { type: string; value: string }) {
    if (this.ws?.readyState === WebSocket.OPEN) this.ws.send(JSON.stringify(msg));
  }

  private async onMessage(msg: { type: string; value: string }) {
    switch (msg.type) {
      case "webrtc/answer":
        await this.pc?.setRemoteDescription({ type: "answer", sdp: msg.value });
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
        this.mic = await navigator.mediaDevices.getUserMedia({
          audio: { echoCancellation: true, noiseSuppression: true, autoGainControl: true },
        });
        this.setMic(this.micOn);
        pc.addTransceiver(this.mic.getAudioTracks()[0], { direction: "sendrecv" });
      } catch {
        this.emit({ state: "error", message: "Microfone bloqueado pelo navegador" });
        pc.addTransceiver("audio", { direction: "recvonly" });
      }
    } else {
      pc.addTransceiver("audio", { direction: "recvonly" });
    }
    const stream = new MediaStream();
    pc.ontrack = (ev) => {
      // Smallest jitter buffer the browser allows: live view favours latency
      // over smoothness (LAN / tailnet, little jitter).
      const r = ev.receiver as unknown as { jitterBufferTarget?: number | null; playoutDelayHint?: number };
      try {
        if (r.jitterBufferTarget !== undefined) r.jitterBufferTarget = 0;
        else r.playoutDelayHint = 0;
      } catch {
        /* not supported: default buffer */
      }
      stream.addTrack(ev.track);
      if (this.video.srcObject !== stream) this.video.srcObject = stream;
    };
    pc.onicecandidate = (ev) => {
      this.send({ type: "webrtc/candidate", value: ev.candidate ? ev.candidate.candidate : "" });
    };
    pc.onconnectionstatechange = () => {
      if (pc !== this.pc) return;
      if (pc.connectionState === "connected") {
        window.clearTimeout(this.fallbackTimer);
        this.retry = 0;
        void this.video.play().catch(() => {});
        rememberWebrtc(true);
        this.emit({ state: "playing", transport: "webrtc" });
      } else if (pc.connectionState === "failed") {
        if (!this.opts.talk) this.fallbackToMSE();
        else this.scheduleRetry("WebRTC falhou");
      }
    };
    const offer = await pc.createOffer();
    await pc.setLocalDescription(offer);
    this.send({ type: "webrtc/offer", value: pc.localDescription!.sdp });
    // If no media path within 6 s (firewall, codec), use MSE.
    this.fallbackTimer = window.setTimeout(() => {
      if (pc === this.pc && pc.connectionState !== "connected" && !this.opts.talk) this.fallbackToMSE();
    }, 6000);
  }

  private fallbackToMSE() {
    this.webrtcFailed = true;
    rememberWebrtc(false);
    this.retry = 0;
    void this.connect();
  }

  // ---------- MSE ----------

  private startMSE() {
    const MS = mediaSourceClass();
    if (!MS) {
      this.emit({ state: "error", message: "Este navegador não reproduz vídeo ao vivo (sem MSE)" });
      return;
    }
    const codecs = supportedMseCodecs((t) => MS.isTypeSupported(t));
    if (!codecs.includes("hvc1")) {
      this.emit({
        state: "error",
        message: "Este navegador não decodifica H.265. Use Chrome, Edge ou Safari atualizados.",
      });
      this.stopped = true;
      this.ws?.close();
      return;
    }
    this.send({ type: "mse", value: codecs });
  }

  private openSourceBuffer(mime: string) {
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
        try {
          const sb = ms.addSourceBuffer(mime);
          sb.mode = "segments";
          sb.addEventListener("updateend", () => this.pump());
          this.sb = sb;
          this.pump();
          void this.video.play().catch(() => {});
          this.retry = 0;
          this.emit({ state: "playing", transport: "mse" });
        } catch {
          this.scheduleRetry("codec não suportado");
        }
      },
      { once: true },
    );
  }

  private appendMSE(buf: ArrayBuffer) {
    this.queue.push(buf);
    if (this.queue.length > 200) this.queue.splice(0, this.queue.length - 50);
    this.pump();
  }

  private pump() {
    const sb = this.sb;
    if (!sb || sb.updating || !this.queue.length) return;
    try {
      const buffered = sb.buffered;
      if (buffered.length) {
        const end = buffered.end(buffered.length - 1);
        const start = buffered.start(0);
        // Stay at the live edge: jump when far behind, otherwise play a
        // little faster until caught up (no visible skip).
        const lag = end - this.video.currentTime;
        if (lag > 1.5) this.video.currentTime = end - 0.15;
        else this.video.playbackRate = lag > 0.5 ? 1.08 : 1;
        // Keep ~10 s of history so memory stays flat.
        if (end - start > 20 && !sb.updating) {
          sb.remove(start, end - 10);
          return;
        }
      }
      sb.appendBuffer(this.queue.shift()!);
    } catch {
      // QuotaExceeded or detached buffer: drop queue and continue.
      this.queue = [];
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
    this.teardown();
    this.retry++;
    const delay = Math.min(30_000, 1000 * 2 ** Math.min(this.retry, 5));
    this.emit({ state: "reconnecting", message });
    this.retryTimer = window.setTimeout(() => void this.connect(), delay);
  }

  private teardown() {
    window.clearTimeout(this.fallbackTimer);
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
    if (this.ms && this.ms.readyState === "open") {
      try {
        this.ms.endOfStream();
      } catch {
        /* already closed */
      }
    }
    this.ms = undefined;
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
