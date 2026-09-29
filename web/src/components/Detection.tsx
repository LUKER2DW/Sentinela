import { useState } from "react";
import { useMutation, useQueries, useQueryClient } from "@tanstack/react-query";
import { api, get, type SystemInfo } from "../lib/api";
import { useSession, useToast } from "../lib/session";
import { relative } from "../lib/time";
import { SENSITIVITY, type DetectionSettings, type Zone } from "../lib/zones";
import { useCameras } from "../pages/LivePage";
import { ZoneEditor } from "./ZoneEditor";

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

  return (
    <section className="card">
      <h2>Detecção de pessoas</h2>
      <p className="muted" style={{ marginBottom: 8 }}>
        {!det?.healthy
          ? "Detector parado: o movimento avisa como antes."
          : analysing
            ? `Analisando movimento agora (${Math.round(st!.inference_ms)} ms por imagem).`
            : `Pronto, esperando movimento${det.last_seen ? ` · ${relative(det.last_seen)}` : ""}.`}
      </p>
      <p className="faint" style={{ fontSize: "0.85rem", marginBottom: 12 }}>
        Liga só quando a câmera vê movimento e avisa apenas pessoas, veículos ou animais dentro das zonas. Árvores, sombras e carros na rua não avisam.
      </p>
      {list.map((c, i) => {
        const d = settings[i]?.data;
        if (!d) return null;
        const set = (patch: Partial<DetectionSettings>) => save.mutate({ id: c.id, d: { ...d, ...patch } });
        return (
          <div key={c.id} className="stack" style={{ gap: 8, marginBottom: 12 }}>
            {list.length > 1 && <strong>{c.name}</strong>}
            <label className="check-row">
              <input type="checkbox" checked={d.enabled} disabled={!can("operator") || save.isPending} onChange={(e) => set({ enabled: e.target.checked })} /> Detectar nesta câmera
            </label>
            {d.enabled && (
              <>
                <div className="segmented" role="group" aria-label="Sensibilidade">
                  {SENSITIVITY.map((x) => (
                    <button key={x.label} aria-pressed={Math.abs(d.min_score - x.value) < 0.01} disabled={!can("operator")} onClick={() => set({ min_score: x.value })}>
                      {x.label}
                    </button>
                  ))}
                </div>
                <button className="btn" disabled={!can("operator")} onClick={() => setEditing(c.id)}>
                  {d.zones.length ? `Zonas (${d.zones.length})` : "Desenhar zonas"}
                </button>
              </>
            )}
            {editing === c.id && (
              <ZoneEditor
                camId={c.id}
                camName={c.name}
                zones={d.zones}
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
