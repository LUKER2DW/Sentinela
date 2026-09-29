import { useEffect, useRef, useState, type KeyboardEvent, type PointerEvent } from "react";
import { LivePlayer, canTalk, type PlayerStatus } from "../lib/player";
import { NO_ZOOM, clampZoom, haptic, saveStream, type Zoom } from "../lib/gestures";
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

  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;
    const p = new LivePlayer(video, { cameraId, stream, talk, onStatus: setStatus });
    playerRef.current = p;
    p.start();
    // Pause the stream while the tab is hidden: saves camera and CPU.
    const onVis = () => {
      if (document.hidden) p.stop();
      else p.start();
    };
    document.addEventListener("visibilitychange", onVis);
    return () => {
      document.removeEventListener("visibilitychange", onVis);
      p.stop();
      playerRef.current = null;
    };
  }, [cameraId, stream, talk]);

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

      <div className="player-tag">
        {status.state === "playing" ? <span className="dot rec" /> : <span className="dot warn" />}
        <span>{status.state === "playing" ? "Ao vivo" : "Conectando"}</span>
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

      {meter && status.state === "playing" && <DelayMeter cameraId={cameraId} onClose={() => setMeter(false)} />}

      {status.state !== "playing" && (
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
            className={`btn talk${holding ? " on" : ""}`}
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
            <Icon name="mic" />{" "}
            {holding ? (
              status.state === "playing" ? "Falando…" : "Conectando…"
            ) : (
              <>
                <span className="hide-sm">Segure para falar</span>
                <span className="show-sm">Falar</span>
              </>
            )}
          </button>
        )}
        <span className="spacer" />
        <button
          className={`btn icon${meter ? " on" : ""}`}
          onClick={() => setMeter((m) => !m)}
          aria-pressed={meter}
          aria-label="Medir atraso"
          title="Medir atraso: compara o relógio da câmera com a hora certa"
        >
          <Icon name="timer" />
        </button>
        {!compact && (
          <div className="segmented" role="group" aria-label="Qualidade">
            <button aria-pressed={stream === "main"} onClick={() => pickStream("main")}>
              HD
            </button>
            <button aria-pressed={stream === "sub"} onClick={() => pickStream("sub")} title="SD gasta menos dados no celular">
              SD
            </button>
          </div>
        )}
        <button className="btn icon" onClick={fullscreen} aria-label="Tela cheia" title="Tela cheia">
          <Icon name="fullscreen" />
        </button>
      </div>
    </div>
  );
}
