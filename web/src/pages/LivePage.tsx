import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { get, type Camera, type SystemInfo, type Timeline } from "../lib/api";
import { href, navigate } from "../lib/router";
import { useSession } from "../lib/session";
import { useActiveEvents } from "../lib/liveEvents";
import { DAY_MS, fmtDay, fmtTime, startOfDay, useNow } from "../lib/time";
import { LiveView } from "../components/LiveView";
import { CameraSettings, PtzPanel } from "../components/CameraControls";
import { DayRibbon, RibbonLegend } from "../components/DayRibbon";
import { Boat } from "../components/Doodles";

export function useCameras() {
  return useQuery({ queryKey: ["cameras"], queryFn: () => get<Camera[]>("/api/cameras"), staleTime: 60_000 });
}

export function useRecorderState(cameraId: string) {
  const sys = useQuery({ queryKey: ["system"], queryFn: () => get<SystemInfo>("/api/system"), refetchInterval: 15_000 });
  const rec = sys.data?.recorders.find((r) => r.camera_id === cameraId);
  // Staleness is judged with the server's clock, not the phone's.
  const fresh = rec && sys.data && new Date(sys.data.now).getTime() - new Date(rec.heartbeat).getTime() < 60_000;
  return { rec, recording: !!fresh && rec?.state === "recording", loaded: sys.isSuccess };
}

function RecordingPill({ cameraId }: { cameraId: string }) {
  const { rec, recording, loaded } = useRecorderState(cameraId);
  if (!loaded) return null;
  if (recording)
    return (
      <span className="pill">
        <span className="dot rec" /> Gravando
      </span>
    );
  return (
    <a className="pill" href={href("system")} title={rec?.last_error || "Gravador sem sinal"}>
      <span className="dot warn" /> Gravação parada
    </a>
  );
}

export function LivePage({ params }: { params: URLSearchParams }) {
  const cams = useCameras();
  const now = useNow();
  const list = cams.data ?? [];
  const selected = list.find((c) => c.id === params.get("cam")) ?? (list.length === 1 ? list[0] : undefined);

  if (cams.isLoading) return null;
  if (cams.isError) return <p className="notice error">Não foi possível carregar as câmeras.</p>;
  if (!list.length)
    return (
      <div className="empty">
        <Boat />
        <strong>Nenhuma câmera ainda</strong>
        <p>Adicione uma câmera em /etc/sentinela/config.yaml e reinicie o Sentinela.</p>
      </div>
    );

  if (!selected) return <Grid cams={list} now={now} />;
  return <Single cam={selected} now={now} multi={list.length > 1} />;
}

function Grid({ cams, now }: { cams: Camera[]; now: number }) {
  const active = useActiveEvents();
  return (
    <>
      <header className="page-head">
        <div className="title">
          <h1>Ao vivo</h1>
          <span className="sub">{fmtDay(now)}</span>
        </div>
        <span className="clock">{fmtTime(now)}</span>
      </header>
      <div className="grid-cams">
        {cams.map((c) => (
          <section key={c.id}>
            <div className="cam-title">
              <h2>
                <a href={href("live", { cam: c.id })} style={{ textDecoration: "none" }}>
                  {c.name}
                </a>
              </h2>
              <RecordingPill cameraId={c.id} />
            </div>
            <LiveView cameraId={c.id} cameraName={c.name} compact defaultStream="sub" motion={(active[c.id] ?? []).length > 0} />
          </section>
        ))}
      </div>
    </>
  );
}

function Single({ cam, now, multi }: { cam: Camera; now: number; multi: boolean }) {
  const { can } = useSession();
  const active = useActiveEvents();
  const dayStart = startOfDay(now);
  const from = dayStart.toISOString();
  const to = new Date(dayStart.getTime() + DAY_MS).toISOString();
  const timeline = useQuery({
    queryKey: ["timeline", cam.id, from],
    queryFn: () => get<Timeline>(`/api/cameras/${cam.id}/timeline?from=${from}&to=${to}`),
    refetchInterval: 60_000,
  });
  const motionToday = timeline.data?.events.filter((e) => e.type === "motion").length ?? 0;
  // Phones start on the substream (SD) to save mobile data; HD is one tap away.
  const [phone] = useState(() => window.matchMedia("(max-width: 760px)").matches);

  return (
    <>
      <header className="page-head">
        <div className="title">
          {multi && (
            <a className="sub" href={href("live")}>
              Todas as câmeras
            </a>
          )}
          <h1>{cam.name}</h1>
          <span className="sub">{fmtDay(now)}</span>
        </div>
        <RecordingPill cameraId={cam.id} />
        <span className="clock">{fmtTime(now)}</span>
      </header>

      <div className="two-col">
        <div className="stack">
          <LiveView
            cameraId={cam.id}
            cameraName={cam.name}
            allowTalk={can("operator")}
            motion={(active[cam.id] ?? []).length > 0}
            defaultStream={phone ? "sub" : "main"}
          />
          <section className="card" aria-label="Hoje">
            <div className="card-head">
              <h2>Hoje</h2>
              <span className="muted">
                {motionToday === 0 ? "Nenhum movimento" : motionToday === 1 ? "1 movimento" : `${motionToday} movimentos`}
              </span>
            </div>
            <DayRibbon
              dayStart={dayStart}
              data={timeline.data}
              now={now}
              label="Linha do tempo de hoje. Escolha um horário para assistir a gravação."
              onPick={(ms) => navigate("recordings", { cam: cam.id, t: new Date(ms).toISOString() })}
            />
            <RibbonLegend />
          </section>
        </div>
        <aside className="stack">
          <PtzPanel cameraId={cam.id} canControl={can("operator")} />
          <CameraSettings cameraId={cam.id} canControl={can("operator")} isAdmin={can("admin")} />
        </aside>
      </div>
    </>
  );
}
