import { useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, get, type Segment, type Timeline } from "../lib/api";
import { navigate } from "../lib/router";
import { useSession, useToast } from "../lib/session";
import { DAY_MS, addDays, eventLabel, fmtDay, fmtHM, fmtTime, sameDay, startOfDay, useNow } from "../lib/time";
import { DayRibbon, RibbonLegend } from "../components/DayRibbon";
import { Icon } from "../components/Icon";
import { Boat, Fish } from "../components/Doodles";
import { useCameras } from "./LivePage";
import { findSegment } from "../lib/segments";

const SPEEDS = [1, 2, 4, 8];

function toDateInput(d: Date) {
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}

export function RecordingsPage({ params }: { params: URLSearchParams }) {
  const cams = useCameras();
  const camId = params.get("cam") ?? cams.data?.[0]?.id;
  const tParam = params.get("t");
  const requested = tParam ? new Date(tParam).getTime() : null;
  const now = useNow();
  const dayStart = startOfDay(requested ?? now);
  const from = dayStart.toISOString();
  const to = new Date(dayStart.getTime() + DAY_MS).toISOString();

  const timeline = useQuery({
    queryKey: ["timeline", camId, from],
    queryFn: () => get<Timeline>(`/api/cameras/${camId}/timeline?from=${from}&to=${to}`),
    enabled: !!camId,
    refetchInterval: sameDay(dayStart, new Date(now)) ? 60_000 : false,
  });

  const go = (ms: number | null, day?: Date) => {
    const p: Record<string, string> = { cam: camId ?? "" };
    if (ms !== null) p.t = new Date(ms).toISOString();
    else if (day) p.t = new Date(day.getTime() + 12 * 3600_000).toISOString();
    navigate("recordings", p);
  };

  const cam = cams.data?.find((c) => c.id === camId);

  return (
    <>
      <header className="page-head">
        <div className="title">
          <h1>Gravações</h1>
          <span className="sub">Escolha um horário na faixa para assistir.</span>
        </div>
        {cams.data && cams.data.length > 1 && (
          <select className="input" value={camId} onChange={(e) => navigate("recordings", { cam: e.target.value })} aria-label="Câmera">
            {cams.data.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </select>
        )}
        <div className="day-nav">
          <button className="btn icon" aria-label="Dia anterior" onClick={() => go(null, addDays(dayStart, -1))}>
            <Icon name="chevronLeft" />
          </button>
          <span className="day-title">{fmtDay(dayStart)}</span>
          <button className="btn icon" aria-label="Próximo dia" disabled={sameDay(dayStart, new Date(now))} onClick={() => go(null, addDays(dayStart, 1))}>
            <Icon name="chevronRight" />
          </button>
          <input
            type="date"
            className="input"
            aria-label="Escolher dia"
            max={toDateInput(new Date(now))}
            value={toDateInput(dayStart)}
            onChange={(e) => {
              const [y, m, d] = e.target.value.split("-").map(Number);
              if (y) go(null, new Date(y, m - 1, d));
            }}
          />
        </div>
      </header>

      <section className="card" style={{ marginBottom: 24 }}>
        <DayRibbon
          dayStart={dayStart}
          data={timeline.data}
          cursor={requested}
          now={now}
          onPick={(ms) => go(ms)}
          label="Linha do tempo do dia. Clique para assistir a partir desse horário."
        />
        <RibbonLegend />
      </section>

      <div className="two-col">
        {camId && timeline.data ? (
          <Playback key={`${camId}-${requested}`} camId={camId} camName={cam?.name ?? camId} segments={timeline.data.segments} requested={requested} onJump={go} />
        ) : (
          <div className="player" />
        )}
        <aside className="stack">
          <DayEvents timeline={timeline.data} onPick={(ms) => go(ms)} />
        </aside>
      </div>
    </>
  );
}

function Playback({
  camId,
  camName,
  segments,
  requested,
  onJump,
}: {
  camId: string;
  camName: string;
  segments: Segment[];
  requested: number | null;
  onJump: (ms: number) => void;
}) {
  const videoRef = useRef<HTMLVideoElement>(null);
  const wrapRef = useRef<HTMLDivElement>(null);
  const [speed, setSpeed] = useState(1);
  const [playing, setPlaying] = useState(false);
  const [pos, setPos] = useState<number | null>(requested);
  const [error, setError] = useState("");
  const [exportOpen, setExportOpen] = useState(false);
  const { can } = useSession();
  const toast = useToast();
  const qc = useQueryClient();

  // Remounted (key) on every pick; afterwards it auto-advances segment by segment.
  const [current, setCurrent] = useState(() => (requested !== null ? findSegment(segments, requested) : null));

  const segStart = current ? new Date(current.seg.start).getTime() : 0;
  const nextSeg = useMemo(() => {
    if (!current) return null;
    const end = new Date(current.seg.end).getTime();
    return segments.find((s) => new Date(s.start).getTime() >= end - 3000 && s.id !== current.seg.id) ?? null;
  }, [current, segments]);

  useEffect(() => {
    if (videoRef.current) videoRef.current.playbackRate = speed;
  }, [speed, current]);

  const protect = useMutation({
    mutationFn: (p: boolean) => api("PUT", `/api/recordings/${current!.seg.id}/protected`, { protected: p }),
    onSuccess: (_, p) => {
      toast(p ? "Trecho protegido contra apagamento" : "Proteção removida");
      void qc.invalidateQueries({ queryKey: ["timeline"] });
      if (current) setCurrent({ ...current, seg: { ...current.seg, protected: p } });
    },
    onError: (e: Error) => toast(e.message, true),
  });

  if (requested === null)
    return (
      <div className="player">
        <div className="player-overlay">
          <Boat />
          <p>Clique na faixa acima para assistir a partir de um horário.</p>
        </div>
      </div>
    );
  if (!current)
    return (
      <div className="player">
        <div className="player-overlay">
          <Boat />
          <p>Não há gravação a partir de {fmtHM(requested)} neste dia.</p>
        </div>
      </div>
    );

  // ±10 s: seek inside the loaded file; cross into the next/previous file via the timeline.
  const skip = (sec: number) => {
    const v = videoRef.current;
    if (!v || pos === null) return;
    const t = v.currentTime + sec;
    if (t >= 0 && t < (v.duration || 0) - 0.5) v.currentTime = t;
    else onJump(pos + sec * 1000);
  };
  const fullscreen = () => {
    const el = wrapRef.current;
    if (document.fullscreenElement) void document.exitFullscreen();
    else if (el?.requestFullscreen) void el.requestFullscreen().catch(() => {});
    else (videoRef.current as HTMLVideoElement & { webkitEnterFullscreen?: () => void })?.webkitEnterFullscreen?.();
  };

  const gapNote = requested < segStart - 5000 ? `Sem gravação às ${fmtHM(requested)}; começando às ${fmtHM(segStart)}.` : "";

  return (
    <div>
      <div className="player" ref={wrapRef} onDoubleClick={fullscreen}>
        <video
          key={current.seg.id}
          ref={videoRef}
          src={`/api/recordings/${current.seg.id}`}
          playsInline
          autoPlay
          controls={false}
          aria-label={`Gravação de ${camName}`}
          onLoadedMetadata={(e) => {
            const v = e.currentTarget;
            v.currentTime = current.offset;
            v.playbackRate = speed;
            void v.play().catch(() => setPlaying(false));
          }}
          onPlay={() => setPlaying(true)}
          onPause={() => setPlaying(false)}
          onTimeUpdate={(e) => setPos(segStart + e.currentTarget.currentTime * 1000)}
          onEnded={() => {
            if (nextSeg) setCurrent({ seg: nextSeg, offset: 0 });
            else setPlaying(false);
          }}
          onError={() =>
            setError(
              "O navegador não conseguiu tocar este vídeo (H.265). Use Chrome/Edge com aceleração de hardware, Safari, ou exporte o trecho.",
            )
          }
        />
        {error && (
          <div className="player-overlay">
            <p>{error}</p>
          </div>
        )}
        <div className="player-bar always">
          <button
            className="btn icon"
            aria-label={playing ? "Pausar" : "Reproduzir"}
            onClick={() => {
              const v = videoRef.current;
              if (!v) return;
              if (v.paused) void v.play();
              else v.pause();
            }}
          >
            <Icon name={playing ? "pause" : "play"} />
          </button>
          <div className="segmented" role="group" aria-label="Velocidade">
            {SPEEDS.map((s) => (
              <button key={s} aria-pressed={speed === s} onClick={() => setSpeed(s)} className={s > 4 ? "hide-sm" : undefined}>
                {s}×
              </button>
            ))}
          </div>
          <span className="spacer" />
          <button className="btn skip" aria-label="Voltar 10 segundos" title="Voltar 10 s" onClick={() => skip(-10)}>
            −10
          </button>
          <button className="btn skip" aria-label="Avançar 10 segundos" title="Avançar 10 s" onClick={() => skip(10)}>
            +10
          </button>
          <button className="btn icon" onClick={fullscreen} aria-label="Tela cheia" title="Tela cheia">
            <Icon name="fullscreen" />
          </button>
        </div>
      </div>

      <div className="under-player">
        <span className="playhead">{pos ? fmtTime(pos) : "--:--:--"}</span>
        {gapNote && <span className="muted">{gapNote}</span>}
        <span className="grow" />
        {can("operator") && (
          <button className={`btn${current.seg.protected ? " on" : ""}`} onClick={() => protect.mutate(!current.seg.protected)} title="Trechos protegidos não são apagados pela limpeza automática">
            <Icon name="shield" /> {current.seg.protected ? "Protegido" : "Proteger trecho"}
          </button>
        )}
        <button className="btn primary" onClick={() => setExportOpen(true)}>
          <Icon name="download" /> Exportar
        </button>
      </div>

      {exportOpen && pos && <ExportDialog camId={camId} start={pos} onClose={() => setExportOpen(false)} />}
    </div>
  );
}

// Clips shared through the phone's share sheet are fetched into memory first:
// keep them short (5 min of main stream is ~70 MB).
const SHARE_MAX_MIN = 5;

async function shareClip(url: string, name: string): Promise<boolean> {
  const nav = navigator as Navigator & { canShare?: (d: ShareData) => boolean };
  if (!nav.share || !nav.canShare) return false;
  const res = await fetch(url, { credentials: "same-origin" });
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  const file = new File([await res.blob()], name, { type: "video/mp4" });
  if (!nav.canShare({ files: [file] })) return false;
  await nav.share({ files: [file], title: name });
  return true;
}

function ExportDialog({ camId, start, onClose }: { camId: string; start: number; onClose: () => void }) {
  const toast = useToast();
  const [sharing, setSharing] = useState(false);
  const [minutes, setMinutes] = useState(5);
  const [back, setBack] = useState(1);
  const from = new Date(start - back * 60_000);
  const to = new Date(from.getTime() + minutes * 60_000);
  const url = `/api/cameras/${encodeURIComponent(camId)}/export?from=${from.toISOString()}&to=${to.toISOString()}`;
  return (
    <div className="dialog-backdrop" role="dialog" aria-modal="true" aria-labelledby="exp-title" onClick={onClose}>
      <div className="dialog" onClick={(e) => e.stopPropagation()}>
        <h2 id="exp-title">Exportar vídeo</h2>
        <div className="field">
          <label>Começar</label>
          <div className="segmented">
            {[0, 1, 5].map((b) => (
              <button key={b} aria-pressed={back === b} onClick={() => setBack(b)}>
                {b === 0 ? "No horário atual" : `${b} min antes`}
              </button>
            ))}
          </div>
        </div>
        <div className="field">
          <label>Duração</label>
          <div className="segmented">
            {[1, 5, 10, 30].map((m) => (
              <button key={m} aria-pressed={minutes === m} onClick={() => setMinutes(m)}>
                {m} min
              </button>
            ))}
          </div>
        </div>
        <p className="muted">
          De {fmtTime(from)} até {fmtTime(to)}. O arquivo MP4 é montado sem perda de qualidade.
        </p>
        <div className="dialog-actions">
          <button className="btn ghost" onClick={onClose}>
            Cancelar
          </button>
          {"share" in navigator && minutes <= SHARE_MAX_MIN && (
            <button
              className="btn"
              disabled={sharing}
              onClick={() => {
                setSharing(true);
                const name = `sentinela-${camId}-${toDateInput(from)}-${fmtHM(from).replace(":", "h")}.mp4`;
                shareClip(url, name)
                  .then((ok) => (ok ? onClose() : toast("Este aparelho não compartilha vídeos; use Baixar.", true)))
                  .catch((e: Error) => e.name !== "AbortError" && toast(`Compartilhar: ${e.message}`, true))
                  .finally(() => setSharing(false));
              }}
            >
              <Icon name="share" /> {sharing ? "Preparando…" : "Compartilhar"}
            </button>
          )}
          <a className="btn primary" href={url} download onClick={() => window.setTimeout(onClose, 300)}>
            <Icon name="download" /> Baixar MP4
          </a>
        </div>
      </div>
    </div>
  );
}

function DayEvents({ timeline, onPick }: { timeline?: Timeline; onPick: (ms: number) => void }) {
  const events = [...(timeline?.events ?? [])].sort((a, b) => b.start.localeCompare(a.start));
  const hours = useMemo(() => {
    const total = (timeline?.segments ?? []).reduce((acc, s) => acc + (new Date(s.end).getTime() - new Date(s.start).getTime()), 0);
    return total / 3600_000;
  }, [timeline]);
  return (
    <section className="card">
      <h2>Neste dia</h2>
      <p className="muted" style={{ marginBottom: 8 }}>
        {hours.toFixed(1).replace(".", ",")} h gravadas
        {events.length ? `, ${events.length} ${events.length === 1 ? "evento" : "eventos"}` : ""}
      </p>
      {events.length === 0 ? (
        <div className="empty" style={{ padding: "16px 0 0" }}>
          <Fish />
          <p>Nenhum movimento neste dia.</p>
        </div>
      ) : (
        <div className="scroll">
          {events.map((e) => (
            <button key={e.id} className="list-row" onClick={() => onPick(new Date(e.start).getTime() - 5000)}>
              <span className="when">{fmtHM(e.start)}</span>
              <span className="tag">{eventLabel[e.type] ?? e.type}</span>
            </button>
          ))}
        </div>
      )}
    </section>
  );
}
