import { useState } from "react";
import { DAY_MS, fmtHM } from "../lib/time";
import { Icon } from "./Icon";

// A keyframe of the 2K main stream is ~220 KB; used for the size estimate.
const KEYFRAME_KB = 220;
const FPS = 25;

const PERIODS = [
  { id: "day", label: "Dia inteiro", from: 0, to: 24 },
  { id: "light", label: "6h às 18h", from: 6, to: 18 },
  { id: "night", label: "18h às 24h", from: 18, to: 24 },
] as const;

const STEPS = [
  { s: 300, label: "Rápido" },
  { s: 120, label: "Médio" },
  { s: 60, label: "Detalhado" },
] as const;

export function timelapseRange(dayStart: Date, fromH: number, toH: number, now: number) {
  const from = dayStart.getTime() + fromH * 3600_000;
  const to = Math.min(dayStart.getTime() + Math.min(toH * 3600_000, DAY_MS), now);
  return to > from ? { from: new Date(from), to: new Date(to) } : null;
}

// Keyframe timelapse of one day (copy-only on the server: no transcode).
export function TimelapseCard({ camId, dayStart, now }: { camId: string; dayStart: Date; now: number }) {
  const [period, setPeriod] = useState<(typeof PERIODS)[number]["id"]>("day");
  const [step, setStep] = useState<number>(120);
  const [src, setSrc] = useState("");
  const [error, setError] = useState("");
  const p = PERIODS.find((x) => x.id === period)!;
  const range = timelapseRange(dayStart, p.from, p.to, now);
  const frames = range ? Math.floor((range.to.getTime() - range.from.getTime()) / 1000 / step) : 0;
  const url = range
    ? `/api/cameras/${encodeURIComponent(camId)}/timelapse?from=${range.from.toISOString()}&to=${range.to.toISOString()}&every=${step}&fps=${FPS}`
    : "";

  return (
    <section className="card">
      <h2>Timelapse</h2>
      <p className="muted" style={{ marginBottom: 8 }}>
        O dia acelerado, montado com as imagens-chave gravadas, sem perda de qualidade.
      </p>
      <div className="field">
        <div className="segmented">
          {PERIODS.map((x) => (
            <button key={x.id} aria-pressed={period === x.id} onClick={() => setPeriod(x.id)}>
              {x.label}
            </button>
          ))}
        </div>
      </div>
      <div className="field">
        <div className="segmented">
          {STEPS.map((x) => (
            <button key={x.s} aria-pressed={step === x.s} onClick={() => setStep(x.s)}>
              {x.label}
            </button>
          ))}
        </div>
      </div>
      {range ? (
        <p className="muted">
          {fmtHM(range.from)}–{fmtHM(range.to)}: 1 imagem a cada {step / 60} min, cerca de {Math.max(1, Math.round(frames / FPS))} s de vídeo e{" "}
          {Math.max(1, Math.round((frames * KEYFRAME_KB) / 1024))} MB.
        </p>
      ) : (
        <p className="muted">Este período ainda não começou.</p>
      )}
      {src && (
        <video
          className="timelapse-video"
          src={src}
          controls
          autoPlay
          muted
          playsInline
          onError={() => setError("O navegador não conseguiu tocar o timelapse (H.265). Use Baixar.")}
        />
      )}
      {error && <p className="muted">{error}</p>}
      <div className="dialog-actions">
        <button
          className="btn"
          disabled={!range}
          onClick={() => {
            setError("");
            setSrc(url);
          }}
        >
          <Icon name="play" /> Assistir
        </button>
        {range && (
          <a className="btn primary" href={`${url}&download=1`} download>
            <Icon name="download" /> Baixar
          </a>
        )}
      </div>
    </section>
  );
}
