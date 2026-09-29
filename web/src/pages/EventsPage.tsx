import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, get, type ReviewItem, type ReviewList } from "../lib/api";
import { navigate } from "../lib/router";
import { useToast } from "../lib/session";
import { eventLabel, fmtDay, fmtDuration, fmtHM, fmtTime, startOfDay, useNow } from "../lib/time";
import { useCameras } from "./LivePage";
import { Fish } from "../components/Doodles";
import { Icon } from "../components/Icon";
import { ModeCard } from "../components/Notifications";

type Severity = "" | "alert" | "detection";

const SEVERITY: { value: Severity; label: string }[] = [
  { value: "", label: "Tudo" },
  { value: "alert", label: "Alertas" },
  { value: "detection", label: "Detecções" },
];

const typeLabel = (t: string) => eventLabel[t] ?? t;

/** Review items (ADR-0015 snapshots, internal/review grouping). */
export function EventsPage() {
  const cams = useCameras();
  const qc = useQueryClient();
  const toast = useToast();
  const [cam, setCam] = useState("");
  const [days, setDays] = useState(1);
  const [severity, setSeverity] = useState<Severity>("");
  const [onlyUnseen, setOnlyUnseen] = useState(false);
  const [kind, setKind] = useState("");
  const [open, setOpen] = useState<number | null>(null);
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

  const kinds = useMemo(() => [...new Set((q.data?.items ?? []).flatMap((it) => it.types))], [q.data]);
  const items = useMemo(() => (q.data?.items ?? []).filter((it) => !kind || it.types.includes(kind)), [q.data, kind]);
  const groups = useMemo(() => {
    const m = new Map<number, ReviewItem[]>();
    for (const it of items) {
      const d = startOfDay(new Date(it.start)).getTime();
      m.set(d, [...(m.get(d) ?? []), it]);
    }
    return [...m.entries()].sort((a, b) => b[0] - a[0]);
  }, [items]);

  const show = (it: ReviewItem) => {
    if (!it.seen) seen.mutate({ ids: [it.id] });
    setOpen(it.id);
  };
  const unseen = q.data?.unseen ?? 0;
  const counts = useMemo(() => {
    const all = q.data?.items ?? [];
    return { alerts: all.filter((i) => i.severity === "alert").length, total: all.length };
  }, [q.data]);
  const current = open !== null ? items.find((i) => i.id === open) : undefined;

  return (
    <>
      <header className="page-head">
        <div className="title">
          <h1>Eventos</h1>
          <span className="sub">
            {counts.total === 0
              ? "Nada no período."
              : `${counts.total} ${counts.total === 1 ? "evento" : "eventos"}, ${counts.alerts} ${counts.alerts === 1 ? "alerta" : "alertas"}${unseen ? ` · ${unseen} ${unseen === 1 ? "novo" : "novos"}` : " · tudo visto"}`}
          </span>
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

      <ModeCard />

      <div className="review-bar">
        <div className="segmented" role="group" aria-label="Gravidade">
          {SEVERITY.map((s) => (
            <button key={s.value} aria-pressed={severity === s.value} onClick={() => setSeverity(s.value)}>
              {s.label}
            </button>
          ))}
        </div>
        {kinds.length > 1 && (
          <div className="chip-row" role="group" aria-label="Tipo">
            <button className={`chip small${kind === "" ? " on" : ""}`} onClick={() => setKind("")}>
              Todos os tipos
            </button>
            {kinds.map((k) => (
              <button key={k} className={`chip small${kind === k ? " on" : ""}`} onClick={() => setKind(kind === k ? "" : k)}>
                <span className={`type-dot ${k}`} /> {typeLabel(k)}
              </button>
            ))}
          </div>
        )}
        <button className={`btn small${onlyUnseen ? " on" : ""}`} aria-pressed={onlyUnseen} onClick={() => setOnlyUnseen((v) => !v)}>
          <Icon name="eye" /> Só não vistos
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
      {q.data && items.length === 0 && (
        <div className="card empty">
          <Fish />
          <strong>{onlyUnseen || severity || kind ? "Nada com esse filtro" : "Tudo calmo por aqui"}</strong>
          <p>
            A câmera avisa movimento e o detector marca pessoas, veículos e animais. Movimentos a menos de 30 s um do outro viram um só evento.
          </p>
        </div>
      )}

      <div className="stack">
        {groups.map(([day, list]) => (
          <section key={day}>
            <h2 className="review-day">
              {fmtDay(day)} <span className="muted small">· {list.length}</span>
            </h2>
            <ul className="review-grid">
              {list.map((it) => (
                <li key={it.id}>
                  <button className={`review-item ${it.severity}${it.seen ? " seen" : ""}`} onClick={() => show(it)}>
                    <span className="review-shot">
                      {it.snapshot_event ? <img src={`/api/events/${it.snapshot_event}/snapshot`} alt="" loading="lazy" decoding="async" /> : <Fish />}
                      {!it.seen && <span className="review-new">Novo</span>}
                      {it.severity === "alert" && <span className="review-alert">Alerta</span>}
                    </span>
                    <span className="review-text">
                      <span className="review-when">
                        {fmtHM(it.start)}
                        <span className="muted small">{it.end ? ` · ${fmtDuration(new Date(it.end).getTime() - new Date(it.start).getTime())}` : " · agora"}</span>
                      </span>
                      <span className="review-tags">
                        {it.types.map((t) => (
                          <span key={t} className="tag">
                            <span className={`type-dot ${t}`} />
                            {typeLabel(t)}
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

      {current && (
        <EventDialog
          item={current}
          camName={camName(current.camera_id)}
          items={items}
          onGo={(it) => show(it)}
          onClose={() => setOpen(null)}
        />
      )}
    </>
  );
}

/** One event large: the snapshot, what it was, and a jump to the recording. */
function EventDialog({ item, camName, items, onGo, onClose }: { item: ReviewItem; camName: string; items: ReviewItem[]; onGo: (it: ReviewItem) => void; onClose: () => void }) {
  const i = items.findIndex((x) => x.id === item.id);
  // The list is newest first: "anterior" is the older one.
  const older = i >= 0 && i < items.length - 1 ? items[i + 1] : null;
  const newer = i > 0 ? items[i - 1] : null;
  const watch = () => navigate("recordings", { cam: item.camera_id, t: new Date(new Date(item.start).getTime() - 5000).toISOString() }, { push: true });
  useEffect(() => {
    const k = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
      if (e.key === "ArrowLeft" && older) onGo(older);
      if (e.key === "ArrowRight" && newer) onGo(newer);
      if (e.key === "Enter") watch();
    };
    window.addEventListener("keydown", k);
    return () => window.removeEventListener("keydown", k);
  });
  return (
    <div className="dialog-backdrop" role="dialog" aria-modal="true" aria-labelledby="ev-title" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="dialog wide event-dialog">
        <div className="dialog-head">
          <div>
            <h2 id="ev-title">
              {item.types.map(typeLabel).join(", ")} · {fmtTime(item.start)}
            </h2>
            <p className="muted small">
              {camName} · {fmtDay(item.start)}
              {item.end ? ` · durou ${fmtDuration(new Date(item.end).getTime() - new Date(item.start).getTime())}` : " · acontecendo agora"}
              {item.events > 1 ? ` · ${item.events} disparos agrupados` : ""}
            </p>
          </div>
          <button className="btn ghost icon small" aria-label="Fechar" onClick={onClose}>
            <Icon name="x" />
          </button>
        </div>
        <div className="event-shot">
          {item.snapshot_event ? (
            <img src={`/api/events/${item.snapshot_event}/snapshot`} alt={`Imagem do evento das ${fmtTime(item.start)}`} />
          ) : (
            <div className="event-noshot">
              <Fish />
              <p>Sem imagem para este evento.</p>
            </div>
          )}
          {older && (
            <button className="viewer-nav prev" aria-label="Evento anterior" title="Anterior (←)" onClick={() => onGo(older)}>
              <Icon name="chevronLeft" />
            </button>
          )}
          {newer && (
            <button className="viewer-nav next" aria-label="Próximo evento" title="Próximo (→)" onClick={() => onGo(newer)}>
              <Icon name="chevronRight" />
            </button>
          )}
        </div>
        <div className="dialog-actions">
          {item.severity === "alert" ? <span className="tag alert">Alerta</span> : <span className="muted small">Detecção</span>}
          <span className="grow" />
          {item.snapshot_event && (
            <a className="btn" href={`/api/events/${item.snapshot_event}/snapshot`} download={`evento-${item.id}.jpg`}>
              <Icon name="download" /> Imagem
            </a>
          )}
          <button className="btn primary" onClick={watch}>
            <Icon name="play" /> Assistir a gravação
          </button>
        </div>
      </div>
    </div>
  );
}
