import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, get, type ReviewItem, type ReviewList } from "../lib/api";
import { navigate } from "../lib/router";
import { useToast } from "../lib/session";
import { eventLabel, fmtDay, fmtDuration, fmtHM, startOfDay, useNow } from "../lib/time";
import { useCameras } from "./LivePage";
import { Fish } from "../components/Doodles";
import { Icon } from "../components/Icon";

type Severity = "" | "alert" | "detection";

const SEVERITY: { value: Severity; label: string }[] = [
  { value: "", label: "Tudo" },
  { value: "alert", label: "Alertas" },
  { value: "detection", label: "Detecções" },
];

/** Review items (ADR-0015 snapshots, internal/review grouping). */
export function EventsPage() {
  const cams = useCameras();
  const qc = useQueryClient();
  const toast = useToast();
  const [cam, setCam] = useState("");
  const [days, setDays] = useState(1);
  const [severity, setSeverity] = useState<Severity>("");
  const [onlyUnseen, setOnlyUnseen] = useState(false);
  const today = startOfDay(useNow()).getTime();
  const from = useMemo(() => new Date(today - (days - 1) * 86_400_000).toISOString(), [today, days]);
  const range = `from=${from}${cam ? `&camera=${cam}` : ""}`;
  const q = useQuery({
    queryKey: ["review", range, severity, onlyUnseen],
    queryFn: () => get<ReviewList>(`/api/review?${range}${severity ? `&severity=${severity}` : ""}${onlyUnseen ? "&unseen=1" : ""}`),
    refetchInterval: 30_000,
  });
  const camName = (id: string) => cams.data?.find((c) => c.id === id)?.name ?? id;
  const multi = (cams.data?.length ?? 0) > 1;

  const seen = useMutation({
    mutationFn: (body: { ids?: number[]; all?: boolean }) => api("POST", `/api/review/seen?${range}`, body),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["review"] }),
    onError: (e: Error) => toast(e.message, true),
  });

  const groups = useMemo(() => {
    const m = new Map<number, ReviewItem[]>();
    for (const it of q.data?.items ?? []) {
      const d = startOfDay(new Date(it.start)).getTime();
      m.set(d, [...(m.get(d) ?? []), it]);
    }
    return [...m.entries()].sort((a, b) => b[0] - a[0]);
  }, [q.data]);

  const open = (it: ReviewItem) => {
    if (!it.seen) seen.mutate({ ids: [it.id] });
    navigate("recordings", { cam: it.camera_id, t: new Date(new Date(it.start).getTime() - 5000).toISOString() });
  };

  const unseen = q.data?.unseen ?? 0;

  return (
    <>
      <header className="page-head">
        <div className="title">
          <h1>Eventos</h1>
          <span className="sub">{unseen ? `${unseen} ${unseen === 1 ? "novo" : "novos"} para ver` : "Tudo visto."}</span>
        </div>
        {multi && (
          <select className="input" value={cam} onChange={(e) => setCam(e.target.value)} aria-label="Câmera">
            <option value="">Todas as câmeras</option>
            {cams.data!.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </select>
        )}
        <div className="segmented" role="group" aria-label="Período">
          {[1, 7, 30].map((d) => (
            <button key={d} aria-pressed={days === d} onClick={() => setDays(d)}>
              {d === 1 ? "Hoje" : `${d} dias`}
            </button>
          ))}
        </div>
      </header>

      <div className="review-bar">
        <div className="segmented" role="group" aria-label="Tipo">
          {SEVERITY.map((s) => (
            <button key={s.value} aria-pressed={severity === s.value} onClick={() => setSeverity(s.value)}>
              {s.label}
            </button>
          ))}
        </div>
        <button className={`btn small${onlyUnseen ? " on" : ""}`} aria-pressed={onlyUnseen} onClick={() => setOnlyUnseen((v) => !v)}>
          <Icon name="eye" /> Não vistos
        </button>
        <span className="grow" />
        {unseen > 0 && (
          <button className="btn small" disabled={seen.isPending} onClick={() => seen.mutate({ all: true })}>
            <Icon name="check" /> Marcar tudo como visto
          </button>
        )}
      </div>

      {q.isLoading && <p className="muted">Carregando…</p>}
      {q.isError && <p className="notice error">Não foi possível carregar os eventos.</p>}
      {q.data && q.data.items.length === 0 && (
        <div className="card empty">
          <Fish />
          <strong>{onlyUnseen || severity ? "Nada com esse filtro" : "Tudo calmo por aqui"}</strong>
          <p>
            Quem detecta é a própria câmera: se nada aparecer, ajuste a área e a sensibilidade no painel dela. Movimentos a menos de 30 s
            um do outro viram um só evento.
          </p>
        </div>
      )}

      <div className="stack">
        {groups.map(([day, list]) => (
          <section key={day}>
            <h2 className="review-day">{fmtDay(day)}</h2>
            <ul className="review-grid">
              {list.map((it) => (
                <li key={it.id}>
                  <button className={`review-item ${it.severity}${it.seen ? " seen" : ""}`} onClick={() => open(it)}>
                    <span className="review-shot">
                      {it.snapshot_event ? (
                        <img src={`/api/events/${it.snapshot_event}/snapshot`} alt="" loading="lazy" decoding="async" />
                      ) : (
                        <Fish />
                      )}
                      {!it.seen && <span className="review-new">Novo</span>}
                    </span>
                    <span className="review-text">
                      <span className="review-when">
                        {fmtHM(it.start)}
                        <span className="muted small">
                          {it.end ? ` · ${fmtDuration(new Date(it.end).getTime() - new Date(it.start).getTime())}` : " · agora"}
                        </span>
                      </span>
                      <span className="review-tags">
                        {it.severity === "alert" && <span className="tag alert">Alerta</span>}
                        {it.types.map((t) => (
                          <span key={t} className="tag">
                            {eventLabel[t] ?? t}
                          </span>
                        ))}
                        {it.events > 1 && <span className="muted small">{it.events} disparos</span>}
                      </span>
                      {multi && <span className="muted small">{camName(it.camera_id)}</span>}
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          </section>
        ))}
      </div>
    </>
  );
}
