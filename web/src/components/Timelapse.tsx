import { useEffect, useRef, useState } from "react";
import { get } from "../lib/api";
import { DAY_MS, fmtBytes, fmtHM } from "../lib/time";
import { useCompact } from "../lib/viewport";
import { Icon } from "./Icon";

// A keyframe of the 2K main stream is ~220 KB; used for the size estimate.
const KEYFRAME_KB = 220;
const FPS = 25;
// Files up to this size are loaded whole before playing: an all-keyframe
// video is ~40 Mbit/s, more than Wi-Fi or 4G keep up with while streaming.
const PRELOAD_MAX = 400 << 20;
const PRELOAD_MAX_PHONE = 160 << 20;

const PERIODS = [
  { id: "day", label: "Dia inteiro", from: 0, to: 24 },
  { id: "light", label: "Dia claro", hint: "6h–18h", from: 6, to: 18 },
  { id: "night", label: "Noite", hint: "18h–24h", from: 18, to: 24 },
] as const;

const STEPS = [
  { s: 300, label: "Rápido", hint: "1 imagem a cada 5 min" },
  { s: 120, label: "Médio", hint: "1 imagem a cada 2 min" },
  { s: 60, label: "Detalhado", hint: "1 imagem por minuto" },
] as const;

export function timelapseRange(dayStart: Date, fromH: number, toH: number, now: number) {
  const from = dayStart.getTime() + fromH * 3600_000;
  const to = Math.min(dayStart.getTime() + Math.min(toH * 3600_000, DAY_MS), now);
  return to > from ? { from: new Date(from), to: new Date(to) } : null;
}

interface Job {
  state: "queued" | "running" | "ready" | "failed" | "empty";
  done: number;
  total: number;
  bytes?: number;
}

/** What to show: the query frozen when "Assistir" was pressed. */
interface Pick {
  url: string;
  label: string;
}

// Keyframe timelapse of one day (copy-only on the server: no transcode).
export function TimelapsePanel({ camId, dayStart, now }: { camId: string; dayStart: Date; now: number }) {
  const [period, setPeriod] = useState<(typeof PERIODS)[number]["id"]>("day");
  const [step, setStep] = useState<number>(120);
  const [pick, setPick] = useState<Pick | null>(null);
  const p = PERIODS.find((x) => x.id === period)!;
  const range = timelapseRange(dayStart, p.from, p.to, now);
  const frames = range ? Math.floor((range.to.getTime() - range.from.getTime()) / 1000 / step) : 0;

  const open = () => {
    if (!range) return;
    // Rounded to the minute: asking again soon reuses the prepared file.
    const to = new Date(Math.floor(range.to.getTime() / 60_000) * 60_000);
    const q = `from=${range.from.toISOString()}&to=${to.toISOString()}&every=${step}&fps=${FPS}`;
    setPick({
      url: `/api/cameras/${encodeURIComponent(camId)}/timelapse?${q}`,
      label: `${p.label}, ${fmtHM(range.from)}–${fmtHM(to)}, ${STEPS.find((x) => x.s === step)!.label.toLowerCase()}`,
    });
  };

  return (
    <div className="tl-panel">
      <p className="muted small">O dia acelerado, montado com as imagens-chave gravadas, sem perda de qualidade.</p>
      <fieldset className="choice-list">
        <legend>Período</legend>
        {PERIODS.map((x) => (
          <label key={x.id} className={`choice${period === x.id ? " on" : ""}`}>
            <input type="radio" name="tl-period" checked={period === x.id} onChange={() => setPeriod(x.id)} />
            <span>{x.label}</span>
            {"hint" in x && <span className="muted small">{x.hint}</span>}
          </label>
        ))}
      </fieldset>
      <fieldset className="choice-list">
        <legend>Velocidade</legend>
        {STEPS.map((x) => (
          <label key={x.s} className={`choice${step === x.s ? " on" : ""}`}>
            <input type="radio" name="tl-step" checked={step === x.s} onChange={() => setStep(x.s)} />
            <span>{x.label}</span>
            <span className="muted small">{x.hint}</span>
          </label>
        ))}
      </fieldset>
      <p className="muted small tl-estimate">
        {range
          ? `${fmtHM(range.from)}–${fmtHM(range.to)} · cerca de ${Math.max(1, Math.round(frames / FPS))} s de vídeo · ${Math.max(1, Math.round((frames * KEYFRAME_KB) / 1024))} MB`
          : "Este período ainda não começou."}
      </p>
      <button className="btn primary block" disabled={!range} onClick={open}>
        <Icon name="play" /> Montar e assistir
      </button>
      {pick && <TimelapseDialog pick={pick} onClose={() => setPick(null)} />}
    </div>
  );
}

/** Prepares the file on the server, loads it, then plays it large. */
function TimelapseDialog({ pick, onClose }: { pick: Pick; onClose: () => void }) {
  const compact = useCompact();
  const [job, setJob] = useState<Job | null>(null);
  const [error, setError] = useState("");
  const [loaded, setLoaded] = useState(0);
  const [blobSrc, setSrc] = useState("");
  const videoRef = useRef<HTMLVideoElement>(null);
  const [rate, setRate] = useState(1);

  // 1) the server writes the file (progress by frames).
  useEffect(() => {
    let live = true;
    let timer: number | undefined;
    const poll = () =>
      get<Job>(pick.url.replace("/timelapse?", "/timelapse/prepare?"))
        .then((j) => {
          if (!live) return;
          setJob(j);
          if (j.state === "failed") setError("O servidor não conseguiu montar o timelapse.");
          else if (j.state === "empty") setError("Não há gravação neste período.");
          else if (j.state !== "ready") timer = window.setTimeout(poll, 700);
        })
        .catch((e: Error) => live && setError(e.message));
    poll();
    return () => {
      live = false;
      window.clearTimeout(timer);
    };
  }, [pick.url]);

  // 2) the browser loads it whole (smooth playback), or streams a big one.
  const ready = job?.state === "ready";
  const bytes = job?.bytes ?? 0;
  const direct = ready && bytes > (compact ? PRELOAD_MAX_PHONE : PRELOAD_MAX);
  const src = direct ? pick.url : blobSrc;
  useEffect(() => {
    if (!ready || direct) return;
    const ctrl = new AbortController();
    let url = "";
    (async () => {
      const res = await fetch(pick.url, { credentials: "same-origin", signal: ctrl.signal });
      if (!res.ok || !res.body) throw new Error(`HTTP ${res.status}`);
      const reader = res.body.getReader();
      const parts: Uint8Array[] = [];
      let got = 0;
      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        parts.push(value);
        got += value.length;
        setLoaded(got);
      }
      url = URL.createObjectURL(new Blob(parts as BlobPart[], { type: "video/mp4" }));
      setSrc(url);
    })().catch((e: Error) => e.name !== "AbortError" && setSrc(pick.url));
    return () => {
      ctrl.abort();
      if (url) URL.revokeObjectURL(url);
    };
  }, [ready, direct, pick.url]);

  useEffect(() => {
    if (videoRef.current) videoRef.current.playbackRate = rate;
  }, [rate, src]);

  const building = !ready && !error;
  const pct = building
    ? job && job.total > 0
      ? job.done / job.total
      : 0
    : ready && !src && bytes > 0
      ? loaded / bytes
      : 1;

  return (
    <div className="dialog-backdrop" role="dialog" aria-modal="true" aria-labelledby="tl-title" onClick={(e) => e.target === e.currentTarget && onClose()}>
      <div className="dialog wide tl-dialog">
        <div className="dialog-head">
          <div>
            <h2 id="tl-title">Timelapse</h2>
            <p className="muted small">{pick.label}</p>
          </div>
          <button className="btn ghost icon small" aria-label="Fechar" onClick={onClose}>
            <Icon name="x" />
          </button>
        </div>
        <div className="tl-stage">
          {src ? (
            <video
              ref={videoRef}
              src={src}
              controls
              autoPlay
              muted
              playsInline
              onLoadedMetadata={(e) => (e.currentTarget.playbackRate = rate)}
              onError={() => setError("O navegador não conseguiu tocar o timelapse (H.265). Use Baixar.")}
            />
          ) : (
            <div className="tl-progress">
              {error ? (
                <p>{error}</p>
              ) : (
                <>
                  <div className="spinner" />
                  <p>
                    {!job || job.state === "queued"
                      ? "Na fila do servidor…"
                      : job.state === "running"
                        ? `Montando: ${job.done} de ${job.total} imagens`
                        : `Carregando ${fmtBytes(loaded)} de ${fmtBytes(bytes)}`}
                  </p>
                  <span className="meter tl-meter" aria-hidden="true">
                    <span style={{ width: `${Math.round(pct * 100)}%` }} />
                  </span>
                </>
              )}
            </div>
          )}
        </div>
        <div className="dialog-actions">
          <div className="segmented" role="group" aria-label="Velocidade de reprodução">
            {[0.5, 1, 2].map((r) => (
              <button key={r} aria-pressed={rate === r} onClick={() => setRate(r)}>
                {String(r).replace(".", ",")}×
              </button>
            ))}
          </div>
          <span className="grow" />
          {ready && bytes > 0 && <span className="muted small">{fmtBytes(bytes)}</span>}
          <a className={`btn primary${ready ? "" : " disabled"}`} href={ready ? `${pick.url}&download=1` : undefined} download aria-disabled={!ready}>
            <Icon name="download" /> Baixar
          </a>
        </div>
      </div>
    </div>
  );
}
