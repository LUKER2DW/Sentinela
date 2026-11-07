import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { fetchSystem, get, type Camera, type Timeline } from "../lib/api";
import { href, navigate } from "../lib/router";
import { useSession } from "../lib/session";
import { useActiveEvents } from "../lib/liveEvents";
import { DAY_MS, fmtDay, fmtTime, startOfDay, useNow } from "../lib/time";
import { LiveView } from "../components/LiveView";
import { CameraSettings, PtzPanel } from "../components/CameraControls";
import { DayRibbon, RibbonLegend } from "../components/DayRibbon";
import { Boat } from "../components/Doodles";
import { Icon } from "../components/Icon";
import { recordingDays } from "./SystemPage";
import { initialStream } from "../lib/gestures";

import { t, plural, fmtNum } from "../lib/i18n";
export function useCameras() {
  return useQuery({ queryKey: ["cameras"], queryFn: () => get<Camera[]>("/api/cameras"), staleTime: 60_000 });
}

export function useRecorderState(cameraId: string) {
  const sys = useQuery({ queryKey: ["system"], queryFn: fetchSystem, refetchInterval: 15_000 });
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
        <span className="dot rec" />{" "}{t("Recording now")}
      </span>
    );
  return (
    <a className="pill" href={href("system")} title={rec?.last_error || t("Recorder has no signal")}>
      <span className="dot warn" />{" "}{t("Recording stopped")}
    </a>
  );
}

export function LivePage({ params, autoFullscreen = false }: { params: URLSearchParams; autoFullscreen?: boolean }) {
  const cams = useCameras();
  const now = useNow();
  const list = cams.data ?? [];
  const selected = list.find((c) => c.id === params.get("cam")) ?? (list.length === 1 ? list[0] : undefined);

  if (cams.isLoading) return null;
  if (cams.isError) return <p className="notice error">{t("Could not load the cameras.")}</p>;
  if (!list.length)
    return (
      <div className="empty">
        <Boat />
        <strong>{t("No cameras yet")}</strong>
        <p>{t("Add a camera in /etc/sentinela/config.yaml and restart Sentinela.")}</p>
      </div>
    );

  // Grid when asked (or several cameras and none picked); one camera otherwise.
  const view = params.get("view") ?? (list.length > 1 && !params.get("cam") ? "grid" : "single");
  if (view === "grid") return <Grid cams={list} now={now} />;
  return <Single cam={selected ?? list[0]} now={now} autoFullscreen={autoFullscreen} />;
}

/** Uma câmera / Grade switch in the page header. */
function ViewSwitch({ view, cam }: { view: "grid" | "single"; cam?: string }) {
  return (
    <div className="segmented" role="group" aria-label={t("View")}>
      <button aria-pressed={view === "single"} title={t("One camera, with controls")} onClick={() => navigate("live", { view: "single", ...(cam ? { cam } : {}) })}>
        <Icon name="video" /> <span className="hide-sm">{t("Detailed")}</span>
      </button>
      <button aria-pressed={view === "grid"} title={t("All cameras in a grid")} onClick={() => navigate("live", { view: "grid" })}>
        <Icon name="grid" /> <span className="hide-sm">{t("Grid")}</span>
      </button>
    </div>
  );
}

function Grid({ cams, now }: { cams: Camera[]; now: number }) {
  const active = useActiveEvents();
  return (
    <>
      <header className="page-head">
        <div className="title">
          <h1>{t("Live")}</h1>
          <span className="sub">{fmtDay(now)}</span>
        </div>
        <ViewSwitch view="grid" />
        <span className="clock">{fmtTime(now)}</span>
      </header>
      <div className={`grid-cams n${Math.min(cams.length, 4)}`}>
        {cams.map((c) => (
          <section key={c.id}>
            <div className="cam-title">
              <h2>
                <a href={href("live", { view: "single", cam: c.id })} style={{ textDecoration: "none" }}>
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

function Single({ cam, now, autoFullscreen }: { cam: Camera; now: number; autoFullscreen: boolean }) {
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
  // The last HD/SD choice on this device; otherwise SD on phones and on
  // mobile data (saves the data plan), HD elsewhere. One tap switches.
  const [stream] = useState(() => initialStream(window.matchMedia("(max-width: 760px)").matches));
  // Phones: PTZ right under the video, where the thumb is.
  const narrow = useNarrow();

  return (
    <>
      <header className="page-head">
        <div className="title">
          <h1>{cam.name}</h1>
          <span className="sub">{fmtDay(now)}</span>
        </div>
        <RecordingPill cameraId={cam.id} />
        <ViewSwitch view="single" cam={cam.id} />
        <span className="clock">{fmtTime(now)}</span>
      </header>

      <div className="two-col live-cols">
        <div className="stack live-left">
          <LiveView
            cameraId={cam.id}
            cameraName={cam.name}
            allowTalk={can("operator") && cam.talk !== false}
            motion={(active[cam.id] ?? []).length > 0}
            defaultStream={stream}
            autoFullscreen={autoFullscreen}
          />
          {narrow && cam.ptz !== false && cam.control !== false && <PtzPanel cameraId={cam.id} canControl={can("operator")} />}
          <section className="card" aria-label={t("Today")}>
            <div className="card-head">
              <h2>{t("Today")}</h2>
              <span className="muted">
                {motionToday === 0 ? t("No motion") : motionToday === 1 ? t("1 motion event") : plural(motionToday, "{n} motion event", "{n} motion events")}
              </span>
            </div>
            <DayRibbon
              dayStart={dayStart}
              data={timeline.data}
              now={now}
              label={t("Today's timeline. Pick a time to watch the recording.")}
              onPick={(ms) => navigate("recordings", { cam: cam.id, t: new Date(ms).toISOString() })}
            />
            <RibbonLegend />
          </section>
          <TodayStats timeline={timeline.data} motion={motionToday} />
        </div>
        <aside className="stack live-right">
          {!narrow && cam.ptz !== false && cam.control !== false && <PtzPanel cameraId={cam.id} canControl={can("operator")} />}
          {cam.control !== false && <CameraSettings cameraId={cam.id} canControl={can("operator")} isAdmin={can("admin")} ptz={cam.ptz !== false} light={cam.light !== false} />}
        </aside>
      </div>
    </>
  );
}

/** True while the layout is one column (the two-col grid collapses at 1180 px). */
function useNarrow(): boolean {
  const query = "(max-width: 1180px)";
  const [narrow, setNarrow] = useState(() => window.matchMedia(query).matches);
  useEffect(() => {
    const m = window.matchMedia(query);
    const on = () => setNarrow(m.matches);
    m.addEventListener("change", on);
    return () => m.removeEventListener("change", on);
  }, []);
  return narrow;
}

function TodayStats({ timeline, motion }: { timeline?: Timeline; motion: number }) {
  const sys = useQuery({ queryKey: ["system"], queryFn: fetchSystem, refetchInterval: 15_000 });
  const hours = (timeline?.segments ?? []).reduce((a, x) => a + (new Date(x.end).getTime() - new Date(x.start).getTime()), 0) / 3_600_000;
  const days = sys.data ? recordingDays(sys.data) : null;
  return (
    <section className="card stat-row today-stats" aria-label={t("Today's summary")}>
      <div className="stat">
        <b>{fmtNum(hours, 1, 1)} h</b>
        <span>{t("recorded today")}</span>
      </div>
      <div className="stat">
        <b>{motion}</b>
        <span>{motion === 1 ? t("motion today") : t("motion events today")}</span>
      </div>
      <div className="stat">
        <b>{days === null ? "–" : days}</b>
        <span>{days === 1 ? t("day fits on the disk") : t("days fit on the disk")}</span>
      </div>
    </section>
  );
}
