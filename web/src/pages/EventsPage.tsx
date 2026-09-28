import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { get, type CamEvent } from "../lib/api";
import { navigate } from "../lib/router";
import { eventLabel, fmtDay, fmtDuration, fmtTime, startOfDay, useNow } from "../lib/time";
import { useCameras } from "./LivePage";
import { Fish } from "../components/Doodles";

export function EventsPage() {
  const cams = useCameras();
  const [cam, setCam] = useState("");
  const [days, setDays] = useState(7);
  const today = startOfDay(useNow()).getTime();
  const from = useMemo(() => new Date(today - (days - 1) * 86_400_000).toISOString(), [today, days]);
  const events = useQuery({
    queryKey: ["events", cam, from],
    queryFn: () => get<CamEvent[]>(`/api/events?from=${from}${cam ? `&camera=${cam}` : ""}`),
    refetchInterval: 30_000,
  });
  const camName = (id: string) => cams.data?.find((c) => c.id === id)?.name ?? id;

  const groups = useMemo(() => {
    const m = new Map<number, CamEvent[]>();
    for (const e of events.data ?? []) {
      const d = startOfDay(new Date(e.start)).getTime();
      m.set(d, [...(m.get(d) ?? []), e]);
    }
    return [...m.entries()].sort((a, b) => b[0] - a[0]);
  }, [events.data]);

  return (
    <>
      <header className="page-head">
        <div className="title">
          <h1>Eventos</h1>
          <span className="sub">Movimentos detectados pela câmera.</span>
        </div>
        {cams.data && cams.data.length > 1 && (
          <select className="input" value={cam} onChange={(e) => setCam(e.target.value)} aria-label="Câmera">
            <option value="">Todas as câmeras</option>
            {cams.data.map((c) => (
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

      {events.isLoading && <p className="muted">Carregando…</p>}
      {events.isError && <p className="notice error">Não foi possível carregar os eventos.</p>}
      {events.data && events.data.length === 0 && (
        <div className="card empty">
          <Fish />
          <strong>Tudo calmo por aqui</strong>
          <p>Nenhum movimento neste período. Quem detecta é a própria câmera: se nada aparecer, ajuste a área e a sensibilidade no painel dela.</p>
        </div>
      )}

      <div className="stack">
      {groups.map(([day, list]) => (
        <section key={day} className="card">
          <h2 style={{ textTransform: "capitalize" }}>{fmtDay(day)}</h2>
          <div>
            {list.map((e) => (
              <div key={e.id} className="list-row">
                <span className="when">{fmtTime(e.start)}</span>
                <span>
                  <span className="tag">{eventLabel[e.type] ?? e.type}</span>
                  <span className="muted">
                    {" "}
                    {e.end ? `por ${fmtDuration(new Date(e.end).getTime() - new Date(e.start).getTime())}` : "em andamento"}
                    {cams.data && cams.data.length > 1 ? `, ${camName(e.camera_id)}` : ""}
                  </span>
                </span>
                <button
                  className="btn small"
                  onClick={() => navigate("recordings", { cam: e.camera_id, t: new Date(new Date(e.start).getTime() - 5000).toISOString() })}
                >
                  Assistir
                </button>
              </div>
            ))}
          </div>
        </section>
      ))}
      </div>
    </>
  );
}
