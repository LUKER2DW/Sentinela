import { useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, fetchSystem, get, type CamEvent, type Camera, type Segment, type Timeline } from "../lib/api";
import { navigate } from "../lib/router";
import { useSession, useToast } from "../lib/session";
import { DAY_MS, addDays, eventLabel, fmtBytes, fmtDay, fmtDuration, fmtHM, fmtTime, mergeRanges, sameDay, startOfDay, useNow } from "../lib/time";
import { DayRibbon, RibbonLegend } from "../components/DayRibbon";
import { TimeRuler } from "../components/TimeRuler";
import { Icon } from "../components/Icon";
import { Boat, Fish } from "../components/Doodles";
import { useCameras } from "./LivePage";
import { findSegment } from "../lib/segments";
import { TimelapsePanel } from "../components/Timelapse";
import { TimelinePreview } from "../components/TimelinePreview";
import { usePinchZoom } from "../lib/pinch";
import { grabFrame, saveBlob, stamp } from "../lib/capture";

const SPEEDS = [1, 2, 4, 8, 16];

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
  // Playback position (for the ruler to follow) and the ruler's window.
  // Stamped with the pick it belongs to, so a new pick starts from requested.
  const pick = `${camId}-${requested}`;
  const [play, setPlay] = useState<{ pick: string; ms: number } | null>(null);
  const playPos = play?.pick === pick ? play.ms : null;
  const [view, setView] = useState<[number, number] | null>(null);
  const events = useMemo(() => [...(timeline.data?.events ?? [])].sort((a, b) => a.start.localeCompare(b.start)), [timeline.data]);

  return (
    <>
      <header className="page-head">
        <div className="title">
          <h1>Gravações</h1>
          <span className="sub">Escolha um horário na faixa ou um evento ao lado para assistir.</span>
        </div>
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
          {!sameDay(dayStart, new Date(now)) && (
            <button className="btn small ghost" onClick={() => go(null, new Date(now))}>
              Hoje
            </button>
          )}
        </div>
      </header>

      {cams.data && cams.data.length > 1 && (
        <CameraPicker
          cams={cams.data}
          value={camId}
          // Same moment on the other camera: comparing two views of one event.
          onPick={(id) => navigate("recordings", tParam ? { cam: id, t: tParam } : { cam: id })}
        />
      )}

      <section className="card rec-timeline">
        <DayRibbon
          dayStart={dayStart}
          data={timeline.data}
          cursor={requested}
          now={now}
          onPick={(ms) => go(ms)}
          view={view}
          preview={(ms) => <TimelinePreview segments={timeline.data?.segments ?? []} ms={ms} />}
          label="Linha do tempo do dia. Clique para assistir a partir desse horário."
        />
        <TimeRuler
          dayStart={dayStart}
          data={timeline.data}
          cursor={playPos ?? requested}
          now={now}
          onPick={(ms) => go(ms)}
          preview={(ms) => <TimelinePreview segments={timeline.data?.segments ?? []} ms={ms} />}
          onView={(a, b) => setView((v) => (v && Math.abs(v[0] - a) < 1000 && Math.abs(v[1] - b) < 1000 ? v : [a, b]))}
        />
        <RibbonLegend />
      </section>

      <div className="rec-layout">
        <div className="rec-main">
          {camId && timeline.data ? (
            <Playback
              key={`${camId}-${requested}`}
              camId={camId}
              camName={cam?.name ?? camId}
              segments={timeline.data.segments}
              events={events}
              requested={requested}
              onJump={go}
              onPos={(ms) => setPlay({ pick, ms })}
            />
          ) : (
            <div className="player" />
          )}
        </div>
        <aside className="rec-side">
          <SidePanel timeline={timeline.data} events={events} camId={camId} dayStart={dayStart} now={now} cursor={playPos ?? requested} onPick={(ms) => go(ms)} />
        </aside>
      </div>

    </>
  );
}

/**
 * Which camera's recordings: one chip per camera with whether it is
 * recording now. A row of chips (not a dropdown) so the choice is visible
 * and one tap away on the phone.
 */
function CameraPicker({ cams, value, onPick }: { cams: Camera[]; value?: string; onPick: (id: string) => void }) {
  const sys = useQuery({ queryKey: ["system"], queryFn: fetchSystem, refetchInterval: 30_000 });
  const live = (id: string) => {
    const r = sys.data?.recorders.find((x) => x.camera_id === id);
    return !!r && r.state === "recording" && new Date(sys.data!.now).getTime() - new Date(r.heartbeat).getTime() < 60_000;
  };
  return (
    <div className="cam-picker hscroll" role="radiogroup" aria-label="Câmera">
      {cams.map((c) => {
        const on = c.id === value;
        const rec = live(c.id);
        return (
          <button key={c.id} role="radio" aria-checked={on} className={`cam-chip${on ? " on" : ""}`} onClick={() => !on && onPick(c.id)}>
            <span className="cam-chip-ico" aria-hidden="true">
              <Icon name="video" />
            </span>
            <span className="cam-chip-text">
              <strong>{c.name}</strong>
              <span className="small">
                <span className={`dot ${rec ? "rec" : sys.data ? "bad" : ""}`} /> {sys.data ? (rec ? "Gravando" : "Sem gravar agora") : "…"}
              </span>
            </span>
          </button>
        );
      })}
    </div>
  );
}

type Tab = "events" | "timelapse" | "summary";

function SidePanel({
  timeline,
  events,
  camId,
  dayStart,
  now,
  cursor,
  onPick,
}: {
  timeline?: Timeline;
  events: CamEvent[];
  camId?: string;
  dayStart: Date;
  now: number;
  cursor: number | null;
  onPick: (ms: number) => void;
}) {
  const [tab, setTab] = useState<Tab>("events");
  return (
    <section className="card side-card">
      <div className="side-tabs" role="tablist" aria-label="Painel">
        <button role="tab" aria-selected={tab === "events"} onClick={() => setTab("events")}>
          Eventos <span className="count">{events.length}</span>
        </button>
        <button role="tab" aria-selected={tab === "timelapse"} onClick={() => setTab("timelapse")}>
          Timelapse
        </button>
        <button role="tab" aria-selected={tab === "summary"} onClick={() => setTab("summary")}>
          Resumo
        </button>
      </div>
      <div className="side-body" role="tabpanel">
        {tab === "events" && <DayEvents events={events} cursor={cursor} onPick={onPick} />}
        {tab === "timelapse" && camId && <TimelapsePanel key={`${camId}-${dayStart.getTime()}`} camId={camId} dayStart={dayStart} now={now} />}
        {tab === "summary" && <DaySummary timeline={timeline} dayStart={dayStart} now={now} />}
      </div>
    </section>
  );
}

function DayEvents({ events, cursor, onPick }: { events: CamEvent[]; cursor: number | null; onPick: (ms: number) => void }) {
  const [type, setType] = useState("");
  const types = useMemo(() => [...new Set(events.map((e) => eventLabel[e.type] ?? e.type))], [events]);
  const shown = [...events].reverse().filter((e) => !type || (eventLabel[e.type] ?? e.type) === type);
  if (events.length === 0)
    return (
      <div className="empty compact-empty">
        <Fish />
        <p>Nenhum evento neste dia.</p>
      </div>
    );
  return (
    <>
      {types.length > 1 && (
        <div className="chip-row" role="group" aria-label="Tipo de evento">
          <button className={`chip small${type === "" ? " on" : ""}`} onClick={() => setType("")}>
            Todos
          </button>
          {types.map((t) => (
            <button key={t} className={`chip small${type === t ? " on" : ""}`} onClick={() => setType(t)}>
              {t}
            </button>
          ))}
        </div>
      )}
      <div className="side-scroll">
        {shown.map((e) => {
          const a = new Date(e.start).getTime();
          const b = e.end ? new Date(e.end).getTime() : a;
          const playing = cursor !== null && cursor >= a - 5000 && cursor <= b + 5000;
          return (
            <button key={e.id} className={`event-row${playing ? " on" : ""}`} onClick={() => onPick(a - 5000)}>
              <span className="when">{fmtTime(e.start)}</span>
              <span className={`tag ${e.type}`}>{eventLabel[e.type] ?? e.type}</span>
              {e.end && <span className="muted small">{fmtDuration(b - a)}</span>}
            </button>
          );
        })}
      </div>
    </>
  );
}

function DaySummary({ timeline, dayStart, now }: { timeline?: Timeline; dayStart: Date; now: number }) {
  const segs = timeline?.segments ?? [];
  const t0 = dayStart.getTime();
  const end = Math.min(t0 + DAY_MS, now);
  const covered = mergeRanges(segs.map((s) => [new Date(s.start).getTime(), new Date(s.end).getTime()] as [number, number]));
  const recorded = covered.reduce((a, [x, y]) => a + Math.max(0, Math.min(y, end) - Math.max(x, t0)), 0);
  // Holes over a minute between recorded stretches (camera offline, restarts).
  const gaps = covered.slice(1).filter((r, i) => r[0] - covered[i][1] > 60_000).length;
  const bytes = segs.reduce((a, s) => a + s.bytes, 0);
  const counts = new Map<string, number>();
  for (const e of timeline?.events ?? []) counts.set(eventLabel[e.type] ?? e.type, (counts.get(eventLabel[e.type] ?? e.type) ?? 0) + 1);
  return (
    <dl className="kv summary-kv">
      <dt>Gravado</dt>
      <dd>
        {(recorded / 3600_000).toFixed(1).replace(".", ",")} h de {((end - t0) / 3600_000).toFixed(1).replace(".", ",")} h
      </dd>
      <dt>Falhas</dt>
      <dd>{gaps === 0 ? "nenhuma" : `${gaps} ${gaps === 1 ? "interrupção" : "interrupções"}`}</dd>
      <dt>Arquivos</dt>
      <dd>
        {segs.length} · {fmtBytes(bytes)}
      </dd>
      <dt>Protegidos</dt>
      <dd>{segs.filter((s) => s.protected).length}</dd>
      {[...counts.entries()].map(([k, v]) => (
        <FragmentRow key={k} k={k} v={v} />
      ))}
    </dl>
  );
}

function FragmentRow({ k, v }: { k: string; v: number }) {
  return (
    <>
      <dt>{k}</dt>
      <dd>{v}</dd>
    </>
  );
}

function Playback({
  camId,
  camName,
  segments,
  events,
  requested,
  onJump,
  onPos,
}: {
  camId: string;
  camName: string;
  segments: Segment[];
  events: CamEvent[];
  requested: number | null;
  onJump: (ms: number) => void;
  onPos: (ms: number) => void;
}) {
  const videoRef = useRef<HTMLVideoElement>(null);
  const wrapRef = useRef<HTMLDivElement>(null);
  const [speed, setSpeed] = useState(1);
  const [playing, setPlaying] = useState(false);
  const [muted, setMuted] = useState(true);
  const [pos, setPos] = useState<number | null>(requested);
  const [error, setError] = useState("");
  const [exportRange, setExportRange] = useState<{ from: number; to?: number } | null>(null);
  const [clipFrom, setClipFrom] = useState<number | null>(null);
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
  useEffect(() => {
    if (videoRef.current) videoRef.current.muted = muted;
  }, [muted, current]);

  const fullscreen = () => {
    const el = wrapRef.current;
    if (document.fullscreenElement) void document.exitFullscreen();
    else if (el?.requestFullscreen) void el.requestFullscreen().catch(() => {});
    else (videoRef.current as HTMLVideoElement & { webkitEnterFullscreen?: () => void })?.webkitEnterFullscreen?.();
  };
  // Phones: pinch to zoom into the picture, double-tap for fullscreen/reset.
  const pz = usePinchZoom(wrapRef, fullscreen);
  const lastPointer = useRef("mouse");

  const protect = useMutation({
    mutationFn: (p: boolean) => api("PUT", `/api/recordings/${current!.seg.id}/protected`, { protected: p }),
    onSuccess: (_, p) => {
      toast(p ? "Trecho protegido contra apagamento" : "Proteção removida");
      void qc.invalidateQueries({ queryKey: ["timeline"] });
      if (current) setCurrent({ ...current, seg: { ...current.seg, protected: p } });
    },
    onError: (e: Error) => toast(e.message, true),
  });

  // ±10 s: seek inside the loaded file; cross into the next/previous file via the timeline.
  const skip = (sec: number) => {
    const v = videoRef.current;
    if (!v || pos === null || !current) return;
    const t = v.currentTime + sec;
    // A file opened at an offset holds nothing before it: reload further back.
    if (t >= current.offset && t < (v.duration || 0) - 0.5) v.currentTime = t;
    else onJump(pos + sec * 1000);
  };
  const toggle = () => {
    const v = videoRef.current;
    if (!v) return;
    if (v.paused) void v.play();
    else v.pause();
  };
  const prevEvent = pos !== null ? [...events].reverse().find((e) => new Date(e.start).getTime() < pos - 8000) : undefined;
  const nextEvent = pos !== null ? events.find((e) => new Date(e.start).getTime() > pos + 2000) : undefined;

  // Keyboard: space plays/pauses, arrows skip 10 s, F fullscreen.
  const keys = useRef({ toggle, skip, fullscreen });
  useEffect(() => {
    keys.current = { toggle, skip, fullscreen };
  });
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement;
      if (t.closest("input, select, textarea, [contenteditable], dialog, .dialog") || e.ctrlKey || e.metaKey || e.altKey) return;
      if (e.key === " " && !t.closest("button")) {
        e.preventDefault();
        keys.current.toggle();
      } else if (e.key === "ArrowLeft") keys.current.skip(-10);
      else if (e.key === "ArrowRight") keys.current.skip(10);
      else if (e.key === "f") keys.current.fullscreen();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  const snapshot = () => {
    const v = videoRef.current;
    if (!v || pos === null) return;
    grabFrame(v)
      .then((b) => saveBlob(b, `sentinela-${camId}-${stamp(new Date(pos))}.jpg`))
      .then(() => toast("Imagem salva no aparelho."))
      .catch((e: Error) => e.name !== "AbortError" && toast(e.message, true));
  };

  if (requested === null)
    return (
      <div className="player">
        <div className="player-overlay">
          <Boat />
          <p>Clique na faixa acima ou num evento para assistir a partir de um horário.</p>
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

  const gapNote = requested < segStart - 5000 ? `Sem gravação às ${fmtHM(requested)}; começando às ${fmtHM(segStart)}.` : "";

  return (
    <div className="playback">
      <div
        className={`player${pz.zoomed ? " zoomed" : ""}`}
        ref={wrapRef}
        // A touch double-tap also fires dblclick: only the mouse's counts here.
        onPointerDownCapture={(e) => (lastPointer.current = e.pointerType)}
        onDoubleClick={() => lastPointer.current === "mouse" && fullscreen()}
        {...pz.handlers}
      >
        <video
          key={current.seg.id}
          ref={videoRef}
          style={pz.style}
          // ?at: the server starts the file at the keyframe before the offset
          // (one request instead of one per fragment up to it).
          src={`/api/recordings/${current.seg.id}${current.offset > 2 ? `?at=${current.offset.toFixed(1)}` : ""}`}
          preload="auto"
          playsInline
          autoPlay
          muted
          controls={false}
          aria-label={`Gravação de ${camName}`}
          onLoadedMetadata={(e) => {
            const v = e.currentTarget;
            v.currentTime = current.offset;
            v.playbackRate = speed;
            v.muted = muted;
            void v.play().catch(() => setPlaying(false));
          }}
          onPlay={() => setPlaying(true)}
          onPause={() => setPlaying(false)}
          onTimeUpdate={(e) => {
            const ms = segStart + e.currentTarget.currentTime * 1000;
            setPos(ms);
            onPos(ms);
          }}
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
        <div className="player-tag">
          <span className="num">{pos ? fmtTime(pos) : "--:--:--"}</span>
          {speed !== 1 && <span className="faint">{speed}×</span>}
        </div>
        {clipFrom !== null && (
          <div className="player-tag rec-tag" role="status">
            <Icon name="scissors" /> Trecho desde {fmtTime(clipFrom)}
          </div>
        )}
        <div className="player-bar">
          <button className="btn icon" aria-label={playing ? "Pausar" : "Reproduzir"} title="Reproduzir/pausar (espaço)" onClick={toggle}>
            <Icon name={playing ? "pause" : "play"} />
          </button>
          <button className="btn skip" aria-label="Voltar 10 segundos" title="Voltar 10 s (←)" onClick={() => skip(-10)}>
            −10
          </button>
          <button className="btn skip" aria-label="Avançar 10 segundos" title="Avançar 10 s (→)" onClick={() => skip(10)}>
            +10
          </button>
          {/* Phone: one button that cycles the speeds, so the bar fits the width. */}
          <button
            className="btn speed-cycle"
            aria-label={`Velocidade ${speed}×, tocar para mudar`}
            onClick={() => setSpeed(SPEEDS[(SPEEDS.indexOf(speed) + 1) % SPEEDS.length])}
          >
            {speed}×
          </button>
          <div className="segmented speed-seg" role="group" aria-label="Velocidade">
            {SPEEDS.map((s) => (
              <button key={s} aria-pressed={speed === s} onClick={() => setSpeed(s)}>
                {s}×
              </button>
            ))}
          </div>
          <span className="spacer" />
          <button className="btn icon" onClick={() => setMuted((m) => !m)} aria-label={muted ? "Ativar som" : "Silenciar"} title={muted ? "Ativar som" : "Silenciar"}>
            <Icon name={muted ? "mute" : "sound"} />
          </button>
          <button className="btn icon" onClick={fullscreen} aria-label="Tela cheia" title="Tela cheia (F)">
            <Icon name="fullscreen" />
          </button>
        </div>
      </div>

      <div className="rec-toolbar">
        <div className="rec-toolbar-group">
          <button className="btn small" disabled={!prevEvent} onClick={() => prevEvent && onJump(new Date(prevEvent.start).getTime() - 5000)} title="Evento anterior">
            <Icon name="skipBack" /> <span className="hide-sm">Evento anterior</span>
          </button>
          <button className="btn small" disabled={!nextEvent} onClick={() => nextEvent && onJump(new Date(nextEvent.start).getTime() - 5000)} title="Próximo evento">
            <span className="hide-sm">Próximo evento</span> <Icon name="skipFwd" />
          </button>
        </div>
        {gapNote && <span className="muted small">{gapNote}</span>}
        <span className="grow" />
        <div className="rec-toolbar-group">
          <button className="btn small" onClick={snapshot} title="Salvar a imagem na tela como JPEG">
            <Icon name="camera" /> Imagem
          </button>
          {clipFrom === null ? (
            <button className="btn small" onClick={() => pos && setClipFrom(pos)} title="Marca o começo; toque de novo no fim para salvar o trecho">
              <Icon name="scissors" /> Marcar trecho
            </button>
          ) : (
            <>
              <button
                className="btn small on"
                disabled={!pos || pos <= clipFrom}
                onClick={() => {
                  if (!pos) return;
                  setExportRange({ from: clipFrom, to: pos });
                  setClipFrom(null);
                }}
                title="Termina o trecho aqui e salva no aparelho"
              >
                <Icon name="download" /> Salvar trecho{pos && pos > clipFrom ? ` (${fmtDuration(pos - clipFrom)})` : ""}
              </button>
              <button className="btn small ghost" onClick={() => setClipFrom(null)} aria-label="Cancelar trecho">
                <Icon name="x" />
              </button>
            </>
          )}
          {can("operator") && (
            <button
              className={`btn small${current.seg.protected ? " on" : ""}`}
              onClick={() => protect.mutate(!current.seg.protected)}
              title="Trechos protegidos não são apagados pela limpeza automática"
            >
              <Icon name="shield" /> {current.seg.protected ? "Protegido" : "Proteger"}
            </button>
          )}
          <button className="btn small primary" onClick={() => pos && setExportRange({ from: pos })}>
            <Icon name="download" /> Exportar
          </button>
        </div>
      </div>

      {exportRange && <ExportDialog camId={camId} range={exportRange} onClose={() => setExportRange(null)} />}
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

/** Export: an exact marked stretch, or a few minutes around the playhead. */
function ExportDialog({ camId, range, onClose }: { camId: string; range: { from: number; to?: number }; onClose: () => void }) {
  const toast = useToast();
  const [sharing, setSharing] = useState(false);
  const exact = range.to !== undefined;
  const [minutes, setMinutes] = useState(5);
  const [back, setBack] = useState(1);
  const from = new Date(exact ? range.from : range.from - back * 60_000);
  const to = new Date(exact ? range.to! : from.getTime() + minutes * 60_000);
  const url = `/api/cameras/${encodeURIComponent(camId)}/export?from=${from.toISOString()}&to=${to.toISOString()}`;
  const lengthMin = (to.getTime() - from.getTime()) / 60_000;
  return (
    <div className="dialog-backdrop" role="dialog" aria-modal="true" aria-labelledby="exp-title" onClick={onClose}>
      <div className="dialog" onClick={(e) => e.stopPropagation()}>
        <h2 id="exp-title">{exact ? "Salvar trecho" : "Exportar vídeo"}</h2>
        {!exact && (
          <>
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
          </>
        )}
        <p className="muted">
          De {fmtTime(from)} até {fmtTime(to)} ({fmtDuration(to.getTime() - from.getTime())}). O arquivo MP4 é montado da gravação original, sem perda de
          qualidade.
        </p>
        <div className="dialog-actions">
          <button className="btn ghost" onClick={onClose}>
            Cancelar
          </button>
          {"share" in navigator && lengthMin <= SHARE_MAX_MIN && (
            <button
              className="btn"
              disabled={sharing}
              onClick={() => {
                setSharing(true);
                const name = `sentinela-${camId}-${stamp(from)}.mp4`;
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
