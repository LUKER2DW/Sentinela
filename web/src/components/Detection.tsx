import { useState } from "react";
import { useMutation, useQueries, useQueryClient } from "@tanstack/react-query";
import { api, get, type SystemInfo } from "../lib/api";
import { setOverlay, useOverlay, type OverlayPrefs } from "../lib/overlay";
import { useSession, useToast } from "../lib/session";
import { relative } from "../lib/time";
import { KIND_ICON, KIND_LABEL, MIN_SIZE, SENSITIVITY, type DetectionSettings, type ObjectKind, type Zone } from "../lib/zones";
import { useCameras } from "../pages/LivePage";
import { Icon } from "./Icon";
import { ZoneEditor } from "./ZoneEditor";

const KINDS = Object.keys(KIND_LABEL) as ObjectKind[];

const OVERLAY_OPTIONS: { key: keyof OverlayPrefs; label: string; hint?: string }[] = [
  { key: "boxes", label: "Caixas em volta do que for detectado" },
  { key: "labels", label: "Nome em cada caixa", hint: "Pessoa, Veículo ou Animal." },
  { key: "score", label: "Confiança da detecção", hint: "Quanto o detector tem certeza, em %." },
  { key: "all", label: "Também o que não avisa", hint: "Fora das zonas ou parado, em traço pontilhado." },
  { key: "zones", label: "Contorno das zonas" },
];

/** Object detector status and per-camera settings (ADR-0016). */
export function DetectionCard({ s }: { s: SystemInfo }) {
  const { can } = useSession();
  const toast = useToast();
  const qc = useQueryClient();
  const cams = useCameras();
  const list = cams.data ?? [];
  const settings = useQueries({
    queries: list.map((c) => ({ queryKey: ["detection", c.id], queryFn: () => get<DetectionSettings>(`/api/cameras/${c.id}/detection`) })),
  });
  const [editing, setEditing] = useState<string | null>(null);
  const save = useMutation({
    mutationFn: ({ id, d }: { id: string; d: DetectionSettings }) => api<DetectionSettings>("PUT", `/api/cameras/${id}/detection`, d),
    onSuccess: (d, { id }) => {
      qc.setQueryData(["detection", id], d);
      setEditing(null);
      toast("Detecção atualizada");
    },
    onError: (e: Error) => toast(e.message, true),
  });
  const det = s.detector;
  const st = det?.status;
  const analysing = st ? Object.values(st.cameras).some((c) => c.analysing) : false;
  const op = can("operator");

  return (
    <section className="card det-card">
      <h2>Detecção</h2>
      <p className="muted" style={{ marginBottom: 8 }}>
        {!det?.healthy
          ? "Detector parado: o movimento avisa como antes."
          : analysing
            ? `Analisando agora (${Math.round(st!.inference_ms)} ms por imagem).`
            : `Pronto, esperando movimento${det.last_seen ? `, visto ${relative(det.last_seen)}` : ""}.`}
        {det?.healthy && st?.model && <span className="faint"> {st.model}.</span>}
      </p>
      <p className="faint" style={{ fontSize: "0.85rem", marginBottom: 14 }}>
        Liga só quando a câmera vê movimento e avisa apenas pessoas, veículos ou animais. Árvores, sombras e carros fora das zonas não avisam.
      </p>

      {list.map((c, i) => {
        const d = settings[i]?.data;
        if (!d) return null;
        const set = (patch: Partial<DetectionSettings>) => save.mutate({ id: c.id, d: { ...d, ...patch } });
        const busy = !op || save.isPending;
        return (
          <div key={c.id} className="det-cam">
            {list.length > 1 && <strong>{c.name}</strong>}
            <label className="check-row">
              <input type="checkbox" checked={d.enabled} disabled={busy} onChange={(e) => set({ enabled: e.target.checked })} /> Detectar nesta câmera
            </label>
            {d.enabled && (
              <div className="det-settings">
                <div className="det-field">
                  <span className="det-name">Sensibilidade</span>
                  <div className="segmented small" role="group" aria-label="Sensibilidade">
                    {SENSITIVITY.map((x) => (
                      <button key={x.label} aria-pressed={Math.abs(d.min_score - x.value) < 0.01} disabled={busy} onClick={() => set({ min_score: x.value })}>
                        {x.label}
                      </button>
                    ))}
                  </div>
                </div>

                <div className="det-field">
                  <span className="det-name">Zonas</span>
                  <div className="det-zone-line">
                    <button className="btn small" disabled={!op} onClick={() => setEditing(c.id)}>
                      <Icon name="zone" /> {d.zones.length ? `Editar zonas (${d.zones.length})` : "Desenhar zonas"}
                    </button>
                    {d.zones.length > 0 && <span className="faint small">{d.zones.map((z) => z.name).join(", ")}</span>}
                  </div>
                </div>

                {d.zones.length === 0 && (
                  <div className="det-field">
                    <span className="det-name">Sem zonas, vigiar</span>
                    <div className="segmented small" role="group" aria-label="O que vigiar na imagem toda">
                      {KINDS.map((k) => {
                        const on = d.objects.includes(k);
                        return (
                          <button
                            key={k}
                            aria-pressed={on}
                            disabled={busy || (on && d.objects.length === 1)}
                            onClick={() => set({ objects: on ? d.objects.filter((o) => o !== k) : [...d.objects, k] })}
                          >
                            <Icon name={KIND_ICON[k]} /> {KIND_LABEL[k]}
                          </button>
                        );
                      })}
                    </div>
                  </div>
                )}

                <div className="det-field">
                  <span className="det-name">Tamanho</span>
                  <div className="segmented small" role="group" aria-label="Tamanho mínimo do objeto">
                    {MIN_SIZE.map((x) => (
                      <button key={x.value} aria-pressed={Math.abs(d.min_size - x.value) < 0.001} disabled={busy} onClick={() => set({ min_size: x.value })}>
                        {x.label}
                      </button>
                    ))}
                  </div>
                </div>

                <label className="check-row">
                  <input type="checkbox" checked={d.ignore_parked} disabled={busy} onChange={(e) => set({ ignore_parked: e.target.checked })} />
                  <span>
                    Ignorar o que fica parado
                    <span className="faint small det-hint">Carro estacionado depois de 30 s, animal deitado depois de 1 min.</span>
                  </span>
                </label>
              </div>
            )}
            {editing === c.id && (
              <ZoneEditor
                camId={c.id}
                camName={c.name}
                zones={d.zones}
                watch={d.objects}
                saving={save.isPending}
                onClose={() => setEditing(null)}
                onSave={(zones: Zone[]) => save.mutate({ id: c.id, d: { ...d, zones } })}
              />
            )}
          </div>
        );
      })}

    </section>
  );
}

/** How detections are drawn over the live view on this device. */
export function OverlayCard() {
  const overlay = useOverlay();
  const cams = useCameras();
  const settings = useQueries({
    queries: (cams.data ?? []).map((c) => ({ queryKey: ["detection", c.id], queryFn: () => get<DetectionSettings>(`/api/cameras/${c.id}/detection`) })),
  });
  const noZones = settings.length > 0 && settings.every((q) => q.data && q.data.zones.length === 0);
  return (
    <section className="card det-overlay">
      <h2>Caixas no ao vivo</h2>
      <p className="faint small" style={{ marginBottom: 10 }}>
        Neste aparelho. Enquanto você assiste a uma câmera, o detector analisa a imagem a cada 2 segundos.
      </p>
      {OVERLAY_OPTIONS.map((o) => (
        <label key={o.key} className={`check-row${o.key !== "boxes" && o.key !== "zones" && !overlay.boxes ? " off" : ""}`}>
          <input
            type="checkbox"
            checked={overlay[o.key]}
            disabled={o.key !== "boxes" && o.key !== "zones" && !overlay.boxes}
            onChange={(e) => setOverlay({ [o.key]: e.target.checked })}
          />
          <span>
            {o.label}
            {o.key === "zones" && noZones ? (
              <span className="faint small det-hint">Nenhuma zona desenhada ainda: desenhe em Detecção, Zonas.</span>
            ) : (
              o.hint && <span className="faint small det-hint">{o.hint}</span>
            )}
          </span>
        </label>
      ))}
    </section>
  );
}
