import { useEffect, useRef, useState, type KeyboardEvent, type PointerEvent } from "react";
import { LivePlayer, canTalk, type PlayerStatus } from "../lib/player";
import { NO_ZOOM, clampZoom, haptic, saveStream, type Zoom } from "../lib/gestures";
import { ClipRecorder, canRecord, grabFrame, paintFrame, saveBlob, stamp } from "../lib/capture";
import { useToast } from "../lib/session";
import { fmtClock } from "../lib/time";
import { Icon } from "./Icon";
import { Boat } from "./Doodles";
import { DelayMeter } from "./DelayMeter";

interface Props {
  cameraId: string;
  cameraName: string;
  motion?: boolean;
  allowTalk?: boolean;
  compact?: boolean;
  defaultStream?: "main" | "sub";
}

const transportLabel = { webrtc: "WebRTC", mse: "MSE" } as const;
export const streamLabel = { main: "Principal", sub: "Substream" } as const;

// A tab hidden (or a window covered) longer than this reopens the stream
// fresh when shown; hidden longer than STOP it closes the stream.
const HIDDEN_RESTART_MS = 3_000;
const HIDDEN_STOP_MS = 30_000;
// A picture that does not move for this long while playing reconnects.
const STALL_MS = 4_000;
// Local recordings stop by themselves after this long (memory on the viewer).
const MAX_CLIP_MS = 15 * 60_000;

// Push-to-talk keeps the audio session this long after the last press, so
// quick replies do not reconnect the video each time.
const TALK_IDLE_MS = 30_000;

type Point = { x: number; y: number };

export function LiveView({ cameraId, cameraName, motion, allowTalk, compact, defaultStream = "main" }: Props) {
  const wrapRef = useRef<HTMLDivElement>(null);
  const videoRef = useRef<HTMLVideoElement>(null);
  const playerRef = useRef<LivePlayer | null>(null);
  const [stream, setStream] = useState<"main" | "sub">(defaultStream);
  const [muted, setMuted] = useState(true);
  const [talk, setTalk] = useState(false);
  const [holding, setHolding] = useState(false);
  const [status, setStatus] = useState<PlayerStatus>({ state: "connecting" });
  const [touch, setTouch] = useState(false);
  const [delay, setDelay] = useState<number | null>(null);
  const [meter, setMeter] = useState(false);
  const [zoom, setZoom] = useState<Zoom>(NO_ZOOM);
  const freezeRef = useRef<HTMLCanvasElement>(null);
  const [frozen, setFrozen] = useState(false);
  const [clip, setClip] = useState<ClipRecorder | null>(null);
  const [clipMs, setClipMs] = useState(0);
  const toast = useToast();

  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;
    // Keep the last picture while the stream reconnects (no black flash).
    const freeze = () => {
      if (freezeRef.current && paintFrame(video, freezeRef.current)) setFrozen(true);
    };
    const p = new LivePlayer(video, { cameraId, stream, talk, onStatus: setStatus, onTeardown: freeze });
    playerRef.current = p;
    p.start();
    // A hidden tab stops decoding video but the stream keeps arriving: on
    // return the browser would replay a backlog (stutter, smeared frames).
    // So a tab hidden for a while reopens the stream fresh on return, behind
    // the last picture, and one hidden longer closes it (camera and CPU).
    let hiddenAt = 0;
    let hiddenTimer: number | undefined;
    let paused = false;
    const onVis = () => {
      window.clearTimeout(hiddenTimer);
      if (document.hidden) {
        hiddenAt = performance.now();
        hiddenTimer = window.setTimeout(() => {
          paused = true;
          p.stop();
        }, HIDDEN_STOP_MS);
      } else if (paused) {
        paused = false;
        p.start();
      } else if (performance.now() - hiddenAt > HIDDEN_RESTART_MS) {
        p.restart();
      }
    };
    document.addEventListener("visibilitychange", onVis);
    // Stall watchdog: a picture frozen for STALL_MS while "playing" (lost
    // packets waiting for a keyframe that never comes) reconnects.
    // Painted frames are counted where the browser tells (a WebRTC clock
    // keeps running while the picture is frozen); else currentTime.
    const rv = video as HTMLVideoElement & { requestVideoFrameCallback?: (cb: () => void) => number; cancelVideoFrameCallback?: (h: number) => void };
    let painted = 0;
    let rvfc = 0;
    const onFrame = () => {
      painted++;
      rvfc = rv.requestVideoFrameCallback!(onFrame);
    };
    const hasRvfc = typeof (video as { requestVideoFrameCallback?: unknown }).requestVideoFrameCallback === "function";
    if (hasRvfc) rvfc = rv.requestVideoFrameCallback!(onFrame);
    let lastT = -1;
    let still = 0;
    const watch = window.setInterval(() => {
      if (document.hidden || p.state !== "playing" || video.paused) {
        still = 0;
        return;
      }
      const t = hasRvfc ? painted : video.currentTime;
      still = t === lastT ? still + 1 : 0;
      lastT = t;
      if (still * 1000 >= STALL_MS) {
        still = 0;
        p.restart();
      }
    }, 1000);
    return () => {
      window.clearTimeout(hiddenTimer);
      window.clearInterval(watch);
      rv.cancelVideoFrameCallback?.(rvfc);
      document.removeEventListener("visibilitychange", onVis);
      p.stop();
      playerRef.current = null;
    };
  }, [cameraId, stream, talk]);

  // The freeze-frame goes away once the new stream paints a picture.
  useEffect(() => {
    const v = videoRef.current;
    if (!v || !frozen) return;
    const clear = () => setFrozen(false);
    const rv = v as HTMLVideoElement & {
      requestVideoFrameCallback?: (cb: () => void) => number;
      cancelVideoFrameCallback?: (h: number) => void;
    };
    if (rv.requestVideoFrameCallback) {
      const h = rv.requestVideoFrameCallback(clear);
      return () => rv.cancelVideoFrameCallback?.(h);
    }
    v.addEventListener("playing", clear, { once: true });
    return () => v.removeEventListener("playing", clear);
  }, [frozen, status.state]);

  // Local recording: a running timer and an automatic stop at the cap.
  const clipRef = useRef<ClipRecorder | null>(null);
  const stopClip = async () => {
    const c = clipRef.current;
    if (!c) return;
    clipRef.current = null;
    setClip(null);
    try {
      const blob = await c.stop();
      await saveBlob(blob, `sentinela-${cameraId}-${stamp(new Date(c.startedAt))}.${c.extension}`);
      toast("Gravação salva no aparelho.");
    } catch (e) {
      if ((e as Error).name !== "AbortError") toast((e as Error).message, true);
    }
  };
  useEffect(() => {
    if (!clip) return;
    const t = window.setInterval(() => {
      const ms = Date.now() - clip.startedAt;
      setClipMs(ms);
      if (ms >= MAX_CLIP_MS) void stopClip();
    }, 500);
    return () => window.clearInterval(t);
  }, [clip]); // eslint-disable-line react-hooks/exhaustive-deps
  // Changing camera or quality ends a local recording (its source goes away).
  useEffect(() => () => void stopClip(), [cameraId, stream]); // eslint-disable-line react-hooks/exhaustive-deps

  const startClip = () => {
    const v = videoRef.current;
    if (!v || status.state !== "playing") return;
    try {
      setClipMs(0);
      const c = new ClipRecorder(v);
      clipRef.current = c;
      setClip(c);
      haptic(20);
    } catch (e) {
      toast(`Não foi possível gravar: ${(e as Error).message}`, true);
    }
  };

  const snapshot = () => {
    const v = videoRef.current;
    if (!v) return;
    haptic();
    grabFrame(v)
      .then((b) => saveBlob(b, `sentinela-${cameraId}-${stamp()}.jpg`))
      .then(() => toast("Imagem salva no aparelho."))
      .catch((e: Error) => e.name !== "AbortError" && toast(e.message, true));
  };

  useEffect(() => {
    if (videoRef.current) videoRef.current.muted = muted;
  }, [muted]);

  // Push-to-talk: the mic only sends while the button is held.
  useEffect(() => {
    playerRef.current?.setMic(holding);
    if (holding || !talk) return;
    const t = window.setTimeout(() => setTalk(false), TALK_IDLE_MS);
    return () => window.clearTimeout(t);
  }, [holding, talk, status.state]);

  // Buffer delay, sampled every 2 s while playing (shown in the tag).
  const playing = status.state === "playing";
  useEffect(() => {
    if (!playing) return;
    let live = true;
    const tick = () =>
      void playerRef.current
        ?.bufferDelay()
        .then((d) => live && setDelay(d))
        .catch(() => {});
    const t = window.setInterval(tick, 2000);
    tick();
    return () => {
      live = false;
      window.clearInterval(t);
      setDelay(null);
    };
  }, [playing]);

  // Leaving fullscreen releases the landscape lock.
  useEffect(() => {
    const onFs = () => {
      if (!document.fullscreenElement) (screen.orientation as ScreenOrientation & { unlock?: () => void })?.unlock?.();
    };
    document.addEventListener("fullscreenchange", onFs);
    return () => document.removeEventListener("fullscreenchange", onFs);
  }, []);

  const fullscreen = () => {
    const el = wrapRef.current;
    if (!el) return;
    if (document.fullscreenElement) {
      void document.exitFullscreen();
      return;
    }
    if (el.requestFullscreen) {
      void el
        .requestFullscreen()
        .then(() => {
          // Phones: turn the picture sideways (only works in fullscreen).
          const o = screen.orientation as ScreenOrientation & { lock?: (o: string) => Promise<void> };
          return o?.lock?.("landscape");
        })
        .catch(() => {});
    } else {
      // iPhone Safari only lets the <video> itself go fullscreen.
      (videoRef.current as HTMLVideoElement & { webkitEnterFullscreen?: () => void })?.webkitEnterFullscreen?.();
    }
  };

  // ---------- touch: tap shows the bar, double-tap fullscreen, pinch zoom ----------
  const pointers = useRef(new Map<number, Point>());
  const pinch = useRef<{ dist: number; scale: number } | null>(null);
  const pan = useRef<Point | null>(null);
  const tap = useRef<{ at: number; x: number; y: number; moved: boolean } | null>(null);
  const lastTap = useRef(0);
  const lastPointer = useRef("mouse");

  const size = () => {
    const r = wrapRef.current?.getBoundingClientRect();
    return { w: r?.width ?? 0, h: r?.height ?? 0 };
  };
  const distance = () => {
    const [a, b] = [...pointers.current.values()];
    return Math.hypot(a.x - b.x, a.y - b.y);
  };
  const fromControls = (e: PointerEvent) => !!(e.target as HTMLElement).closest(".player-bar, button, .delay-meter");

  const onPointerDown = (e: PointerEvent) => {
    lastPointer.current = e.pointerType;
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
    const prev = pointers.current.get(e.pointerId);
    if (!prev) return;
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
      else fullscreen();
    } else {
      lastTap.current = e.timeStamp;
      setTouch((v) => !v);
    }
  };

  const talkAvailable = allowTalk && canTalk();
  const press = (on: boolean) => {
    if (!talkAvailable) return;
    if (on) {
      haptic(20);
      setTalk(true);
    }
    setHolding(on);
  };
  const talkKey = (e: KeyboardEvent, on: boolean) => {
    if (e.key !== " " && e.key !== "Enter") return;
    e.preventDefault();
    if (!e.repeat) press(on);
  };

  const pickStream = (s: "main" | "sub") => {
    setStream(s);
    if (!compact) saveStream(s);
  };

  return (
    <div
      ref={wrapRef}
      className={`player${touch ? " touch" : ""}${zoom.scale > 1 ? " zoomed" : ""}`}
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={onPointerUp}
      onPointerCancel={onPointerUp}
      onDoubleClick={() => lastPointer.current === "mouse" && fullscreen()}
    >
      <video
        ref={videoRef}
        playsInline
        autoPlay
        muted
        aria-label={`Ao vivo: ${cameraName}`}
        style={zoom.scale > 1 ? { transform: `translate(${zoom.x}px, ${zoom.y}px) scale(${zoom.scale})` } : undefined}
      />
      <canvas
        ref={freezeRef}
        className={`player-freeze${frozen ? " on" : ""}`}
        aria-hidden="true"
        style={zoom.scale > 1 ? { transform: `translate(${zoom.x}px, ${zoom.y}px) scale(${zoom.scale})` } : undefined}
      />

      <div className="player-tag">
        {status.state === "playing" && !frozen ? <span className="dot rec" /> : <span className="dot warn" />}
        <span>{status.state === "playing" && !frozen ? "Ao vivo" : frozen ? "Reconectando" : "Conectando"}</span>
        {!compact && <span className="faint">{streamLabel[stream]}</span>}
        {status.transport && status.state === "playing" && (
          <span className="faint" title="Transporte e quanto vídeo o navegador segura antes de mostrar. Quanto menor, mais perto do tempo real.">
            {transportLabel[status.transport]}
            {delay !== null && ` ${delay < 0.1 ? "<0,1" : delay.toFixed(1).replace(".", ",")} s`}
          </span>
        )}
        {zoom.scale > 1 && <span className="faint">{zoom.scale.toFixed(1).replace(".", ",")}×</span>}
      </div>
      {motion && (
        <div className="player-tag motion" role="status">
          <span className="dot bad" /> Movimento
        </div>
      )}
      {clip && (
        <div className="player-tag rec-tag" role="status">
          <span className="dot rec" /> Gravando no aparelho <span className="num">{fmtClock(clipMs)}</span>
        </div>
      )}

      {meter && status.state === "playing" && <DelayMeter cameraId={cameraId} onClose={() => setMeter(false)} />}

      {status.state !== "playing" && !(frozen && status.state !== "error") && (
        <div className="player-overlay">
          <div className="stack-sm" style={{ justifyItems: "center" }}>
            {status.state === "error" ? <Boat /> : <div className="spinner" />}
            <p>
              {status.state === "error"
                ? status.message
                : status.state === "reconnecting"
                  ? `Reconectando… ${status.message ?? ""}`
                  : "Abrindo vídeo…"}
            </p>
          </div>
        </div>
      )}

      <div className={`player-bar${holding ? " always" : ""}`}>
        <button className="btn icon" onClick={() => setMuted((m) => !m)} aria-label={muted ? "Ativar som" : "Silenciar"} title={muted ? "Ativar som" : "Silenciar"}>
          <Icon name={muted ? "mute" : "sound"} />
        </button>
        {allowTalk && (
          <button
            className={`btn icon talk${holding ? " on" : ""}`}
            aria-label={holding ? (status.state === "playing" ? "Falando" : "Conectando") : "Segure para falar"}
            disabled={!talkAvailable}
            aria-pressed={holding}
            onPointerDown={(e) => {
              e.currentTarget.setPointerCapture(e.pointerId);
              press(true);
            }}
            onPointerUp={() => press(false)}
            onPointerCancel={() => press(false)}
            onKeyDown={(e) => talkKey(e, true)}
            onKeyUp={(e) => talkKey(e, false)}
            onContextMenu={(e) => e.preventDefault()}
            title={talkAvailable ? "Segure para falar pelo alto-falante da câmera" : "Falar exige HTTPS e WebRTC (indisponível pela rede de casa em HTTP e pelo Funnel)"}
          >
            <Icon name="mic" />
          </button>
        )}
        <span className="spacer" />
        <button className="btn icon" onClick={snapshot} disabled={status.state !== "playing"} aria-label="Salvar imagem" title="Salvar a imagem atual no aparelho">
          <Icon name="camera" />
        </button>
        {canRecord() && (
          <button
            className={`btn icon rec-btn${clip ? " on" : ""}`}
            onClick={() => (clip ? void stopClip() : startClip())}
            disabled={!clip && status.state !== "playing"}
            aria-pressed={!!clip}
            aria-label={clip ? "Parar e salvar a gravação" : "Gravar no aparelho"}
            title={clip ? "Parar e salvar a gravação no aparelho" : "Gravar o que aparece aqui e salvar no aparelho"}
          >
            <Icon name={clip ? "stop" : "record"} />
          </button>
        )}
        <button
          className={`btn icon hide-sm${meter ? " on" : ""}`}
          onClick={() => setMeter((m) => !m)}
          aria-pressed={meter}
          aria-label="Medir atraso"
          title="Medir atraso: compara o relógio da câmera com a hora certa"
        >
          <Icon name="timer" />
        </button>
        {!compact && (
          // One button: shows the stream playing, a tap switches to the other.
          <button
            className="btn stream-toggle"
            onClick={() => pickStream(stream === "main" ? "sub" : "main")}
            aria-label={stream === "main" ? "HD: tocar para mudar para SD" : "SD: tocar para mudar para HD"}
            title={stream === "main" ? "HD (stream principal, 2K). Toque para SD: abre mais rápido e gasta menos dados" : "SD (substream). Toque para HD: resolução máxima (2K)"}
          >
            {stream === "main" ? "HD" : "SD"}
          </button>
        )}
        <button className="btn icon" onClick={fullscreen} aria-label="Tela cheia" title="Tela cheia">
          <Icon name="fullscreen" />
        </button>
      </div>
    </div>
  );
}
