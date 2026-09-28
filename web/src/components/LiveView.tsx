import { useEffect, useRef, useState } from "react";
import { LivePlayer, canTalk, type PlayerStatus } from "../lib/player";
import { Icon } from "./Icon";

interface Props {
  cameraId: string;
  cameraName: string;
  motion?: boolean;
  allowTalk?: boolean;
  compact?: boolean;
  defaultStream?: "main" | "sub";
}

const transportLabel = { webrtc: "WebRTC", mse: "MSE" } as const;

export function LiveView({ cameraId, cameraName, motion, allowTalk, compact, defaultStream = "main" }: Props) {
  const wrapRef = useRef<HTMLDivElement>(null);
  const videoRef = useRef<HTMLVideoElement>(null);
  const playerRef = useRef<LivePlayer | null>(null);
  const [stream, setStream] = useState<"main" | "sub">(defaultStream);
  const [muted, setMuted] = useState(true);
  const [talk, setTalk] = useState(false);
  const [status, setStatus] = useState<PlayerStatus>({ state: "connecting" });
  const [touch, setTouch] = useState(false);

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

  const fullscreen = () => {
    const el = wrapRef.current;
    if (!el) return;
    if (document.fullscreenElement) void document.exitFullscreen();
    else void el.requestFullscreen?.().catch(() => {});
  };

  const talkAvailable = allowTalk && canTalk();

  return (
    <div
      ref={wrapRef}
      className={`player${touch ? " touch" : ""}`}
      onPointerDown={(e) => e.pointerType === "touch" && setTouch((t) => !t)}
      onDoubleClick={fullscreen}
    >
      <video ref={videoRef} playsInline autoPlay muted aria-label={`Ao vivo: ${cameraName}`} />

      <div className="player-tag">
        {status.state === "playing" ? <span className="dot rec" /> : <span className="dot warn" />}
        <span>{status.state === "playing" ? "Ao vivo" : "Conectando"}</span>
        {status.transport && status.state === "playing" && (
          <span className="faint">{transportLabel[status.transport]}</span>
        )}
      </div>
      {motion && (
        <div className="player-tag motion" role="status">
          <span className="dot bad" /> Movimento
        </div>
      )}

      {status.state !== "playing" && (
        <div className="player-overlay">
          <div>
            {status.state !== "error" && <div className="spinner" />}
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

      <div className="player-bar">
        <button className="btn icon" onClick={() => setMuted((m) => !m)} aria-label={muted ? "Ativar som" : "Silenciar"} title={muted ? "Ativar som" : "Silenciar"}>
          <Icon name={muted ? "mute" : "sound"} />
        </button>
        {allowTalk && (
          <button
            className={`btn${talk ? " on" : ""}`}
            disabled={!talkAvailable}
            onClick={() => setTalk((t) => !t)}
            title={talkAvailable ? "Falar pelo alto-falante da câmera" : "Falar exige HTTPS (disponível com o Tailscale)"}
          >
            <Icon name="mic" /> {talk ? "Falando" : "Falar"}
          </button>
        )}
        <span className="spacer" />
        {!compact && (
          <div className="segmented" role="group" aria-label="Qualidade">
            <button aria-pressed={stream === "main"} onClick={() => setStream("main")}>HD</button>
            <button aria-pressed={stream === "sub"} onClick={() => setStream("sub")}>SD</button>
          </div>
        )}
        <button className="btn icon" onClick={fullscreen} aria-label="Tela cheia" title="Tela cheia">
          <Icon name="fullscreen" />
        </button>
      </div>
    </div>
  );
}
