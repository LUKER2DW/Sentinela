import { useEffect, useRef, useState } from "react";
import { KIND_LABEL, MAX_POINTS, MAX_ZONES, ZONE_COLORS, nextZoneName, svgPoints, toNorm, zoneProblem, type ObjectKind, type Zone } from "../lib/zones";
import { Icon } from "./Icon";

const KINDS = Object.keys(KIND_LABEL) as ObjectKind[];

interface Props {
  camId: string;
  camName: string;
  zones: Zone[];
  saving: boolean;
  onSave: (zones: Zone[]) => void;
  onClose: () => void;
}

/**
 * Draw detection zones over a current picture of the camera. Tap to add
 * points, drag a point to move it. A person (or vehicle/animal) counts when
 * the bottom of its box — the feet — is inside a zone that watches it.
 */
export function ZoneEditor({ camId, camName, zones: initial, saving, onSave, onClose }: Props) {
  const [zones, setZones] = useState<Zone[]>(() => initial.map((z) => ({ ...z, points: z.points.map((p) => [...p] as [number, number]) })));
  const [drawing, setDrawing] = useState<[number, number][] | null>(null);
  const [selected, setSelected] = useState<number | null>(initial.length ? 0 : null);
  const [drag, setDrag] = useState<{ zone: number; point: number } | null>(null);
  const [size, setSize] = useState({ w: 1000, h: 560 });
  const [nonce, setNonce] = useState(() => Date.now());
  const [imgError, setImgError] = useState(false);
  const svgRef = useRef<SVGSVGElement>(null);
  const problem = zoneProblem(zones);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && (drawing ? setDrawing(null) : onClose());
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [drawing, onClose]);

  const pointAt = (e: React.PointerEvent) => toNorm(e.clientX, e.clientY, svgRef.current!.getBoundingClientRect());
  const update = (i: number, z: Partial<Zone>) => setZones((all) => all.map((x, j) => (j === i ? { ...x, ...z } : x)));

  const finish = () => {
    if (!drawing || drawing.length < 3) return;
    setZones((all) => [...all, { name: nextZoneName(all), points: drawing, objects: ["person"] }]);
    setSelected(zones.length);
    setDrawing(null);
  };

  return (
    <div className="dialog-backdrop" role="dialog" aria-modal="true" aria-labelledby="zones-title">
      <div className="dialog zone-dialog">
        <header className="zone-head">
          <h2 id="zones-title">Zonas de {camName}</h2>
          <button className="btn icon ghost" aria-label="Fechar" onClick={onClose}>
            <Icon name="x" />
          </button>
        </header>
        <p className="muted">
          {drawing
            ? "Toque na imagem para marcar os cantos da zona. Com 3 ou mais pontos, toque em Concluir."
            : "Só conta quem pisa dentro de uma zona: os pés da pessoa (ou as rodas do carro) precisam estar dentro dela. Sem zonas, a imagem inteira vigia pessoas."}
        </p>

        <div className="zone-stage">
          {imgError ? (
            <div className="zone-noimg">
              Não foi possível pegar uma imagem da câmera.{" "}
              <button
                className="btn small"
                onClick={() => {
                  setImgError(false);
                  setNonce(Date.now());
                }}
              >
                Tentar de novo
              </button>
            </div>
          ) : (
            <img
              src={`/api/cameras/${encodeURIComponent(camId)}/frame.jpeg?t=${nonce}`}
              alt=""
              onLoad={(e) => setSize({ w: 1000, h: Math.round((1000 * e.currentTarget.naturalHeight) / e.currentTarget.naturalWidth) || 560 })}
              onError={() => setImgError(true)}
            />
          )}
          <svg
            ref={svgRef}
            viewBox={`0 0 ${size.w} ${size.h}`}
            preserveAspectRatio="none"
            className={drawing ? "drawing" : ""}
            onPointerDown={(e) => {
              if (drawing && drawing.length < MAX_POINTS) setDrawing([...drawing, pointAt(e)]);
            }}
            onPointerMove={(e) => {
              if (!drag) return;
              const p = pointAt(e);
              update(drag.zone, { points: zones[drag.zone].points.map((q, k) => (k === drag.point ? p : q)) });
            }}
            onPointerUp={() => setDrag(null)}
            onPointerCancel={() => setDrag(null)}
          >
            {zones.map((z, i) => {
              const color = ZONE_COLORS[i % ZONE_COLORS.length];
              return (
                <g key={i} onPointerDown={(e) => !drawing && (e.stopPropagation(), setSelected(i))}>
                  <polygon points={svgPoints(z.points, size.w, size.h)} fill={color} fillOpacity={selected === i ? 0.32 : 0.18} stroke={color} strokeWidth={3} strokeLinejoin="round" />
                  {selected === i &&
                    !drawing &&
                    z.points.map(([x, y], k) => (
                      <circle
                        key={k}
                        className="zone-handle"
                        cx={x * size.w}
                        cy={y * size.h}
                        r={11}
                        fill="#fff"
                        stroke={color}
                        strokeWidth={4}
                        onPointerDown={(e) => {
                          e.stopPropagation();
                          (e.target as Element).setPointerCapture?.(e.pointerId);
                          setDrag({ zone: i, point: k });
                        }}
                      />
                    ))}
                </g>
              );
            })}
            {drawing && (
              <g>
                <polyline points={svgPoints(drawing, size.w, size.h)} fill="none" stroke="#fff" strokeWidth={3} strokeDasharray="10 8" />
                {drawing.map(([x, y], k) => (
                  <circle key={k} cx={x * size.w} cy={y * size.h} r={8} fill="#fff" />
                ))}
              </g>
            )}
          </svg>
        </div>

        {drawing ? (
          <div className="dialog-actions">
            <button className="btn ghost" onClick={() => setDrawing(null)}>
              Cancelar
            </button>
            <button className="btn" disabled={drawing.length === 0} onClick={() => setDrawing(drawing.slice(0, -1))}>
              Desfazer ponto
            </button>
            <button className="btn primary" disabled={drawing.length < 3} onClick={finish}>
              <Icon name="check" /> Concluir zona
            </button>
          </div>
        ) : (
          <>
            <div className="zone-list">
              {zones.map((z, i) => (
                <div key={i} className={`zone-row${selected === i ? " on" : ""}`} onClick={() => setSelected(i)}>
                  <span className="zone-swatch" style={{ background: ZONE_COLORS[i % ZONE_COLORS.length] }} />
                  <input className="input" aria-label="Nome da zona" value={z.name} maxLength={40} onChange={(e) => update(i, { name: e.target.value })} />
                  <div className="segmented small" role="group" aria-label="O que a zona vigia">
                    {KINDS.map((k) => (
                      <button
                        key={k}
                        aria-pressed={z.objects.includes(k)}
                        onClick={() => update(i, { objects: z.objects.includes(k) ? z.objects.filter((o) => o !== k) : [...z.objects, k] })}
                      >
                        {KIND_LABEL[k]}
                      </button>
                    ))}
                  </div>
                  <button
                    className="btn icon ghost danger"
                    aria-label={`Apagar ${z.name}`}
                    onClick={(e) => {
                      e.stopPropagation();
                      setZones(zones.filter((_, j) => j !== i));
                      setSelected(null);
                    }}
                  >
                    <Icon name="trash" />
                  </button>
                </div>
              ))}
            </div>
            {problem && <p className="notice error">{problem}</p>}
            <div className="dialog-actions">
              <button className="btn" disabled={zones.length >= MAX_ZONES} onClick={() => (setDrawing([]), setSelected(null))}>
                <Icon name="plus" /> Nova zona
              </button>
              <span className="grow" />
              <button className="btn ghost" onClick={onClose}>
                Cancelar
              </button>
              <button className="btn primary" disabled={!!problem || saving} onClick={() => onSave(zones)}>
                {saving ? "Salvando…" : "Salvar zonas"}
              </button>
            </div>
          </>
        )}
      </div>
    </div>
  );
}
