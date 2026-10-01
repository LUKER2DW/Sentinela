import { useCallback, useEffect, useRef, useState, type PointerEvent as RPointerEvent } from "react";
import { api } from "../lib/api";
import { useSeen } from "../lib/detections";
import {
  KIND_ICON,
  KIND_LABEL,
  KIND_ONE,
  MAX_POINTS,
  MAX_ZONES,
  ZONE_COLORS,
  midpoint,
  movePoints,
  nextZoneName,
  rectPoints,
  selfIntersects,
  snapPt,
  svgPoints,
  toNorm,
  zoneHits,
  zoneProblem,
  type ObjectKind,
  type Pt,
  type Zone,
} from "../lib/zones";
import { Icon } from "./Icon";

const KINDS = Object.keys(KIND_LABEL) as ObjectKind[];
type Tool = "select" | "polygon" | "rect";

interface Props {
  camId: string;
  camName: string;
  zones: Zone[];
  /** Kinds watched over the whole picture when there are no zones. */
  watch: ObjectKind[];
  saving: boolean;
  onSave: (zones: Zone[]) => void;
  onClose: () => void;
}

type Drag =
  | { kind: "vertex"; zone: number; point: number; moved: boolean }
  | { kind: "zone"; zone: number; from: Pt; orig: Pt[]; moved: boolean }
  | { kind: "rect"; from: Pt; to: Pt };

/** Zones with undo/redo. `commit` records a step; `replace` changes the
 * current step (the moves of one drag are a single step). */
function useHistory(initial: Zone[]) {
  const [h, setH] = useState({ past: [] as Zone[][], now: initial, future: [] as Zone[][] });
  const commit = useCallback((f: (z: Zone[]) => Zone[]) => setH((s) => ({ past: [...s.past, s.now].slice(-60), now: f(s.now), future: [] })), []);
  const replace = useCallback((f: (z: Zone[]) => Zone[]) => setH((s) => ({ ...s, now: f(s.now) })), []);
  const undo = useCallback(
    () => setH((s) => (s.past.length ? { past: s.past.slice(0, -1), now: s.past[s.past.length - 1], future: [s.now, ...s.future] } : s)),
    [],
  );
  const redo = useCallback(() => setH((s) => (s.future.length ? { past: [...s.past, s.now], now: s.future[0], future: s.future.slice(1) } : s)), []);
  return { zones: h.now, commit, replace, undo, redo, canUndo: h.past.length > 0, canRedo: h.future.length > 0, dirty: h.past.length > 0 };
}

const HINT: Record<Tool, string> = {
  select: "Arraste uma zona para movê-la e os pontos para ajustar. Puxe o ponto pequeno no meio de um lado para criar um canto; toque duas vezes num canto para tirá-lo.",
  polygon: "Toque nos cantos da zona, um depois do outro. Toque no primeiro ponto (ou Enter) para fechar.",
  rect: "Arraste na imagem para desenhar um retângulo.",
};

/**
 * Draw the detection zones over a current picture of the camera. A person
 * (or vehicle, animal) counts when the bottom of its box, where it stands,
 * is inside a zone that watches it. While open, the detector analyses the
 * camera so its boxes show whether each object would count.
 */
export function ZoneEditor({ camId, camName, zones: initial, watch, saving, onSave, onClose }: Props) {
  const hist = useHistory(initial.map((z) => ({ ...z, points: z.points.map((p) => [...p] as Pt) })));
  const { zones, commit, replace } = hist;
  const [tool, setTool] = useState<Tool>(initial.length ? "select" : "polygon");
  const [selected, setSelected] = useState<number | null>(initial.length ? 0 : null);
  const [vertex, setVertex] = useState<number | null>(null);
  const [draft, setDraft] = useState<Pt[]>([]);
  const [hover, setHover] = useState<Pt | null>(null);
  const [drag, setDrag] = useState<Drag | null>(null);
  const [live, setLive] = useState(true);
  const [nonce, setNonce] = useState(() => Date.now());
  const [ratio, setRatio] = useState(448 / 800);
  const [imgError, setImgError] = useState(false);
  const [stageW, setStageW] = useState(0);
  const svgRef = useRef<SVGSVGElement>(null);
  const seen = useSeen(camId);
  const problem = zoneProblem(zones);
  const twisted = zones.find((z) => selfIntersects(z.points));

  const W = 1000;
  const H = Math.round(W * ratio);
  // Handles are sized in screen pixels: k turns pixels into viewBox units.
  const k = stageW ? W / stageW : 1;

  useEffect(() => {
    const el = svgRef.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setStageW(el.getBoundingClientRect().width));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  // While "detections" is on: keep the detector analysing this camera and
  // refresh the picture so the boxes match it.
  useEffect(() => {
    if (!live) return;
    const probe = () => void api("POST", `/api/cameras/${encodeURIComponent(camId)}/detection/probe`).catch(() => {});
    probe();
    const p = window.setInterval(probe, 15_000);
    const f = window.setInterval(() => setNonce(Date.now()), 6_000);
    return () => {
      window.clearInterval(p);
      window.clearInterval(f);
    };
  }, [live, camId]);

  const close = useCallback(() => {
    if (!hist.dirty || window.confirm("Sair sem salvar as zonas?")) onClose();
  }, [hist.dirty, onClose]);

  const finishDraft = useCallback(
    (pts: Pt[]) => {
      if (pts.length < 3 || zones.length >= MAX_ZONES) return;
      commit((all) => [...all, { name: nextZoneName(all), points: pts, objects: ["person"] }]);
      setSelected(zones.length);
      setVertex(null);
      setDraft([]);
      setTool("select");
    },
    [commit, zones.length],
  );

  const removeVertex = useCallback(
    (zi: number, pi: number) => {
      if (zones[zi].points.length <= 3) return;
      commit((all) => all.map((z, j) => (j === zi ? { ...z, points: z.points.filter((_, n) => n !== pi) } : z)));
      setVertex(null);
    },
    [commit, zones],
  );

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const typing = (e.target as HTMLElement)?.tagName === "INPUT";
      const mod = e.ctrlKey || e.metaKey;
      if (mod && e.key.toLowerCase() === "z" && !typing) {
        e.preventDefault();
        if (e.shiftKey) hist.redo();
        else hist.undo();
        return;
      }
      if (mod && e.key.toLowerCase() === "y" && !typing) {
        e.preventDefault();
        hist.redo();
        return;
      }
      if (e.key === "Escape") {
        if (draft.length) setDraft([]);
        else if (tool !== "select") setTool("select");
        else close();
        return;
      }
      if (typing) return;
      if (e.key === "Enter" && tool === "polygon") finishDraft(draft);
      if (e.key === "Backspace" && tool === "polygon" && draft.length) setDraft(draft.slice(0, -1));
      if ((e.key === "Delete" || e.key === "Backspace") && tool === "select" && selected !== null && vertex !== null) removeVertex(selected, vertex);
      const nudge = { ArrowLeft: [-1, 0], ArrowRight: [1, 0], ArrowUp: [0, -1], ArrowDown: [0, 1] }[e.key];
      if (nudge && tool === "select" && selected !== null) {
        e.preventDefault();
        const step = e.shiftKey ? 0.02 : 0.004;
        commit((all) => all.map((z, j) => (j === selected ? { ...z, points: movePoints(z.points, nudge[0] * step, nudge[1] * step) } : z)));
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [draft, tool, selected, vertex, close, finishDraft, removeVertex, commit, hist]);

  const at = (e: RPointerEvent) => toNorm(e.clientX, e.clientY, svgRef.current!.getBoundingClientRect()) as Pt;
  const capture = (e: RPointerEvent) => svgRef.current?.setPointerCapture?.(e.pointerId);
  const update = (i: number, z: Partial<Zone>) => commit((all) => all.map((x, j) => (j === i ? { ...x, ...z } : x)));

  const onStageDown = (e: RPointerEvent) => {
    const p = snapPt(at(e));
    if (tool === "polygon") {
      const first = draft[0];
      if (first && draft.length >= 3 && Math.hypot((p[0] - first[0]) * W, (p[1] - first[1]) * H) < 16 * k) return finishDraft(draft);
      if (draft.length < MAX_POINTS) setDraft([...draft, p]);
    } else if (tool === "rect") {
      capture(e);
      setDrag({ kind: "rect", from: p, to: p });
    } else {
      setSelected(null);
      setVertex(null);
    }
  };

  const onMove = (e: RPointerEvent) => {
    const p = at(e);
    if (tool === "polygon") setHover(snapPt(p));
    if (!drag) return;
    if (drag.kind === "rect") return setDrag({ ...drag, to: snapPt(p) });
    if (drag.kind === "vertex") {
      const f = (all: Zone[]) => all.map((z, j) => (j === drag.zone ? { ...z, points: z.points.map((q, n) => (n === drag.point ? snapPt(p) : q)) } : z));
      if (drag.moved) replace(f);
      else {
        commit(f);
        setDrag({ ...drag, moved: true });
      }
      return;
    }
    const moved = movePoints(drag.orig, p[0] - drag.from[0], p[1] - drag.from[1]);
    const f = (all: Zone[]) => all.map((z, j) => (j === drag.zone ? { ...z, points: moved } : z));
    if (drag.moved) replace(f);
    else if (Math.hypot((p[0] - drag.from[0]) * W, (p[1] - drag.from[1]) * H) > 3 * k) {
      commit(f);
      setDrag({ ...drag, moved: true });
    }
  };

  const onUp = () => {
    if (drag?.kind === "rect") {
      const pts = rectPoints(drag.from, drag.to);
      if (Math.abs(drag.to[0] - drag.from[0]) > 0.02 && Math.abs(drag.to[1] - drag.from[1]) > 0.02) finishDraft(pts);
    }
    setDrag(null);
  };

  const rectDraft = drag?.kind === "rect" ? rectPoints(drag.from, drag.to) : null;
  const objects = live ? (seen?.objects ?? []) : [];

  return (
    <div className="dialog-backdrop" role="dialog" aria-modal="true" aria-labelledby="zones-title">
      <div className="dialog zone-dialog">
        <header className="zone-head">
          <h2 id="zones-title">Zonas de {camName}</h2>
          <button className="btn icon ghost" aria-label="Fechar" onClick={close}>
            <Icon name="x" />
          </button>
        </header>

        <div className="zone-tools" role="toolbar" aria-label="Ferramentas">
          <div className="segmented small" role="group" aria-label="Ferramenta">
            <button aria-pressed={tool === "select"} onClick={() => (setTool("select"), setDraft([]))}>
              <Icon name="pointer" /> Mover
            </button>
            <button aria-pressed={tool === "polygon"} disabled={zones.length >= MAX_ZONES} onClick={() => (setTool("polygon"), setSelected(null))}>
              <Icon name="polygon" /> Polígono
            </button>
            <button aria-pressed={tool === "rect"} disabled={zones.length >= MAX_ZONES} onClick={() => (setTool("rect"), setDraft([]), setSelected(null))}>
              <Icon name="square" /> Retângulo
            </button>
          </div>
          <div className="segmented small" role="group" aria-label="Histórico">
            <button disabled={!hist.canUndo} onClick={hist.undo} aria-label="Desfazer" title="Desfazer (Ctrl+Z)">
              <Icon name="undo" />
            </button>
            <button disabled={!hist.canRedo} onClick={hist.redo} aria-label="Refazer" title="Refazer (Ctrl+Shift+Z)">
              <Icon name="redo" />
            </button>
          </div>
          <span className="grow" />
          <label className="check-row zone-live">
            <input type="checkbox" checked={live} onChange={(e) => setLive(e.target.checked)} /> Ver detecções
          </label>
          <button className="btn ghost icon small" aria-label="Nova imagem da câmera" title="Nova imagem" onClick={() => (setImgError(false), setNonce(Date.now()))}>
            <Icon name="refresh" />
          </button>
        </div>

        <div className={`zone-stage tool-${tool}`} style={{ aspectRatio: `${W} / ${H}` }}>
          {imgError ? (
            <div className="zone-noimg">
              Não foi possível pegar uma imagem da câmera.
              <button className="btn small" onClick={() => (setImgError(false), setNonce(Date.now()))}>
                Tentar de novo
              </button>
            </div>
          ) : (
            <img
              src={`/api/cameras/${encodeURIComponent(camId)}/frame.jpeg?t=${nonce}`}
              alt=""
              draggable={false}
              onLoad={(e) => e.currentTarget.naturalWidth && setRatio(e.currentTarget.naturalHeight / e.currentTarget.naturalWidth)}
              onError={() => setImgError(true)}
            />
          )}
          <svg
            ref={svgRef}
            viewBox={`0 0 ${W} ${H}`}
            preserveAspectRatio="none"
            onPointerDown={onStageDown}
            onPointerMove={onMove}
            onPointerUp={onUp}
            onPointerCancel={onUp}
            onPointerLeave={() => setHover(null)}
          >
            {zones.map((z, i) => {
              const color = ZONE_COLORS[i % ZONE_COLORS.length];
              const sel = selected === i && tool === "select";
              return (
                <g key={i} className={`zone-shape${sel ? " on" : ""}`} style={{ color }}>
                  <polygon
                    points={svgPoints(z.points, W, H)}
                    onPointerDown={(e) => {
                      if (tool !== "select") return;
                      e.stopPropagation();
                      capture(e);
                      setSelected(i);
                      setVertex(null);
                      setDrag({ kind: "zone", zone: i, from: at(e), orig: z.points, moved: false });
                    }}
                  />
                  <text x={z.points[0][0] * W + 8 * k} y={z.points[0][1] * H + 20 * k} fontSize={13 * k} className="zone-name">
                    {z.name}
                  </text>
                  {sel &&
                    z.points.map((_, n) => {
                      if (z.points.length >= MAX_POINTS) return null;
                      const [mx, my] = midpoint(z.points, n);
                      return (
                        <circle
                          key={`m${n}`}
                          className="zone-mid"
                          cx={mx * W}
                          cy={my * H}
                          r={6 * k}
                          onPointerDown={(e) => {
                            e.stopPropagation();
                            capture(e);
                            commit((all) => all.map((x, j) => (j === i ? { ...x, points: [...x.points.slice(0, n + 1), [mx, my] as Pt, ...x.points.slice(n + 1)] } : x)));
                            setVertex(n + 1);
                            setDrag({ kind: "vertex", zone: i, point: n + 1, moved: true });
                          }}
                        />
                      );
                    })}
                  {sel &&
                    z.points.map(([x, y], n) => (
                      <g
                        key={n}
                        className={`zone-handle${vertex === n ? " on" : ""}`}
                        onPointerDown={(e) => {
                          e.stopPropagation();
                          capture(e);
                          setVertex(n);
                          setDrag({ kind: "vertex", zone: i, point: n, moved: false });
                        }}
                        onDoubleClick={() => removeVertex(i, n)}
                      >
                        <circle cx={x * W} cy={y * H} r={20 * k} fill="transparent" />
                        <circle cx={x * W} cy={y * H} r={7.5 * k} className="dot" />
                      </g>
                    ))}
                </g>
              );
            })}

            {objects.map((o, n) => {
              const hits = zoneHits(o, zones, watch);
              const counts = hits.length > 0 && !o.parked;
              return (
                <g key={n} className={`zone-obj k-${o.kind}${counts ? " counts" : ""}`}>
                  <rect x={o.x * W} y={o.y * H} width={o.w * W} height={o.h * H} rx={4 * k} />
                  <circle cx={(o.x + o.w / 2) * W} cy={Math.min(o.y + o.h, 0.999) * H} r={5 * k} />
                  <text x={o.x * W} y={o.y * H - 5 * k} fontSize={12 * k}>
                    {KIND_ONE[o.kind]}: {counts ? `conta${hits[0] !== "*" ? ` em ${hits.join(", ")}` : ""}` : o.parked && hits.length ? "parado, não conta" : "não conta"}
                  </text>
                </g>
              );
            })}

            {tool === "polygon" && draft.length > 0 && (
              <g className="zone-draft">
                <polyline points={svgPoints(hover ? [...draft, hover] : draft, W, H)} />
                {draft.map(([x, y], n) => (
                  <circle key={n} cx={x * W} cy={y * H} r={(n === 0 && draft.length >= 3 ? 9 : 5.5) * k} className={n === 0 && draft.length >= 3 ? "close" : ""} />
                ))}
              </g>
            )}
            {rectDraft && (
              <g className="zone-draft">
                <polygon points={svgPoints(rectDraft, W, H)} />
              </g>
            )}
          </svg>
        </div>

        <p className="zone-hint faint small">{HINT[tool]}</p>

        {tool === "polygon" && (
          <div className="dialog-actions">
            <button className="btn ghost" disabled={!draft.length} onClick={() => setDraft([])}>
              Recomeçar
            </button>
            <button className="btn" disabled={!draft.length} onClick={() => setDraft(draft.slice(0, -1))}>
              Desfazer ponto
            </button>
            <button className="btn primary" disabled={draft.length < 3} onClick={() => finishDraft(draft)}>
              <Icon name="check" /> Fechar zona
            </button>
          </div>
        )}

        <div className="zone-list">
          {zones.length === 0 && <p className="muted">Nenhuma zona ainda. Sem zonas, a imagem inteira vigia {watch.map((w) => KIND_LABEL[w].toLowerCase()).join(" e ")}.</p>}
          {zones.map((z, i) => (
            <div key={i} className={`zone-row${selected === i ? " on" : ""}`} onClick={() => (setSelected(i), setTool("select"))}>
              <span className="zone-swatch" style={{ background: ZONE_COLORS[i % ZONE_COLORS.length] }} />
              <input className="input" aria-label="Nome da zona" value={z.name} maxLength={40} onChange={(e) => update(i, { name: e.target.value })} />
              <div className="segmented small" role="group" aria-label={`O que ${z.name} vigia`}>
                {KINDS.map((kd) => (
                  <button
                    key={kd}
                    aria-pressed={z.objects.includes(kd)}
                    title={KIND_LABEL[kd]}
                    onClick={(e) => {
                      e.stopPropagation();
                      update(i, { objects: z.objects.includes(kd) ? z.objects.filter((o) => o !== kd) : [...z.objects, kd] });
                    }}
                  >
                    <Icon name={KIND_ICON[kd]} /> <span className="hide-sm">{KIND_LABEL[kd]}</span>
                  </button>
                ))}
              </div>
              <button
                className="btn icon ghost danger"
                aria-label={`Apagar ${z.name}`}
                onClick={(e) => {
                  e.stopPropagation();
                  commit((all) => all.filter((_, j) => j !== i));
                  setSelected(null);
                  setVertex(null);
                }}
              >
                <Icon name="trash" />
              </button>
            </div>
          ))}
        </div>

        {problem ? (
          <p className="notice error">{problem}</p>
        ) : twisted ? (
          <p className="notice">Os lados da zona "{twisted.name}" se cruzam: arraste um canto para desfazer o laço.</p>
        ) : null}
        <div className="dialog-actions">
          <span className="faint small">
            {zones.length} de {MAX_ZONES} zonas
          </span>
          <span className="grow" />
          <button className="btn ghost" onClick={close}>
            Cancelar
          </button>
          <button className="btn primary" disabled={!!problem || saving} onClick={() => onSave(zones)}>
            {saving ? "Salvando…" : "Salvar zonas"}
          </button>
        </div>
      </div>
    </div>
  );
}
