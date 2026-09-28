import { useCallback, useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, get, type ControlState, type DayNight } from "../lib/api";
import { useToast } from "../lib/session";
import { dayNightLabel } from "../lib/time";
import { Icon } from "./Icon";

type Op = "move" | "zoom" | "focus";

/** Press-and-hold: start, repeat every 400 ms (server deadman is 1 s), stop on release. */
function useHold(cameraId: string, speed: number, onError: (m: string) => void) {
  const timer = useRef<number | undefined>(undefined);
  const active = useRef<string | null>(null);
  const [pressed, setPressed] = useState<string | null>(null);

  const send = useCallback(
    (body: object) => api("POST", `/api/cameras/${cameraId}/ptz`, body),
    [cameraId],
  );

  const stop = useCallback(() => {
    window.clearInterval(timer.current);
    if (active.current) {
      active.current = null;
      setPressed(null);
      send({ action: "stop" }).catch(() => {});
    }
  }, [send]);

  const start = useCallback(
    (op: Op, dir: string) => {
      stop();
      const key = `${op}:${dir}`;
      active.current = key;
      setPressed(key);
      const body = { action: "start", op, dir, speed };
      send(body).catch((e: Error) => {
        onError(e.message);
        stop();
      });
      timer.current = window.setInterval(() => {
        if (active.current === key) send(body).catch(() => {});
      }, 400);
    },
    [send, speed, stop, onError],
  );

  useEffect(() => {
    window.addEventListener("blur", stop);
    return () => {
      window.removeEventListener("blur", stop);
      stop();
    };
  }, [stop]);

  const bind = (op: Op, dir: string) => ({
    onPointerDown: (e: React.PointerEvent) => {
      e.preventDefault();
      (e.target as Element).setPointerCapture?.(e.pointerId);
      start(op, dir);
    },
    onPointerUp: stop,
    onPointerCancel: stop,
    onLostPointerCapture: stop,
    onKeyDown: (e: React.KeyboardEvent) => {
      if ((e.key === "Enter" || e.key === " ") && !e.repeat) start(op, dir);
    },
    onKeyUp: (e: React.KeyboardEvent) => {
      if (e.key === "Enter" || e.key === " ") stop();
    },
    "data-active": pressed === `${op}:${dir}` ? "true" : "false",
  });
  return bind;
}

const DIRS: { dir: string; label: string; angle: number }[] = [
  { dir: "up", label: "Cima", angle: -90 },
  { dir: "up_right", label: "Cima e direita", angle: -45 },
  { dir: "right", label: "Direita", angle: 0 },
  { dir: "down_right", label: "Baixo e direita", angle: 45 },
  { dir: "down", label: "Baixo", angle: 90 },
  { dir: "down_left", label: "Baixo e esquerda", angle: 135 },
  { dir: "left", label: "Esquerda", angle: 180 },
  { dir: "up_left", label: "Cima e esquerda", angle: -135 },
];

export function PtzPanel({ cameraId, canControl }: { cameraId: string; canControl: boolean }) {
  const toast = useToast();
  const [speed, setSpeed] = useState(4);
  const bind = useHold(cameraId, speed, (m) => toast(`PTZ: ${m}`, true));

  return (
    <section className="panel ptz" aria-label="Controle de movimento">
      <div
        className="dial"
        role="group"
        aria-label="Direções"
      >
        {DIRS.map(({ dir, label, angle }) => {
          const rad = (angle * Math.PI) / 180;
          return (
            <button
              key={dir}
              style={{ left: `${50 + 36 * Math.cos(rad)}%`, top: `${50 + 36 * Math.sin(rad)}%` }}
              aria-label={label}
              title={label}
              disabled={!canControl}
              {...bind("move", dir)}
            >
              <Icon name={dir} />
            </button>
          );
        })}
        <div className="hub num" aria-hidden="true">
          {speed}
        </div>
      </div>

      <label className="speed">
        <span className="muted">Velocidade</span>
        <input type="range" min={1} max={8} value={speed} onChange={(e) => setSpeed(Number(e.target.value))} disabled={!canControl} />
      </label>

      <div className="ptz-lens">
        <div className="ptz-row">
          <span className="label">Zoom</span>
          <button className="btn icon small" aria-label="Afastar" title="Afastar" disabled={!canControl} {...bind("zoom", "out")}>
            <Icon name="minus" />
          </button>
          <button className="btn icon small" aria-label="Aproximar" title="Aproximar" disabled={!canControl} {...bind("zoom", "in")}>
            <Icon name="plus" />
          </button>
        </div>
        <div className="ptz-row">
          <span className="label">Foco</span>
          <button className="btn icon small" aria-label="Foco perto" title="Foco perto" disabled={!canControl} {...bind("focus", "near")}>
            <Icon name="minus" />
          </button>
          <button className="btn icon small" aria-label="Foco longe" title="Foco longe" disabled={!canControl} {...bind("focus", "far")}>
            <Icon name="plus" />
          </button>
        </div>
      </div>
      {!canControl && <p className="muted" style={{ fontSize: "0.9rem" }}>Seu perfil só permite assistir.</p>}
    </section>
  );
}

export function CameraSettings({ cameraId, canControl, isAdmin }: { cameraId: string; canControl: boolean; isAdmin: boolean }) {
  const toast = useToast();
  const qc = useQueryClient();
  const key = ["control", cameraId];
  const state = useQuery({
    queryKey: key,
    queryFn: () => get<ControlState>(`/api/cameras/${cameraId}/control`),
    staleTime: 15_000,
    retry: 1,
  });
  const [confirmSave, setConfirmSave] = useState<number | null>(null);

  const light = useMutation({
    mutationFn: (on: boolean) => api("PUT", `/api/cameras/${cameraId}/light`, { on }),
    onSuccess: (_, on) => {
      toast(on ? "Luz branca ligada" : "Luz branca desligada");
      void qc.invalidateQueries({ queryKey: key });
    },
    onError: (e: Error) => toast(`Luz: ${e.message}`, true),
  });
  const dayNight = useMutation({
    mutationFn: (mode: DayNight) => api("PUT", `/api/cameras/${cameraId}/daynight`, { mode }),
    onSuccess: (_, mode) => {
      toast(`Modo de imagem: ${dayNightLabel[mode]}`);
      void qc.invalidateQueries({ queryKey: key });
    },
    onError: (e: Error) => toast(`Modo de imagem: ${e.message}`, true),
  });
  const preset = useMutation({
    mutationFn: ({ id, action }: { id: number; action: "goto" | "set" | "clear" }) =>
      api("POST", `/api/cameras/${cameraId}/presets/${id}`, { action }),
    onSuccess: (_, v) => {
      if (v.action === "set") toast(`Posição ${v.id} salva`);
      if (v.action !== "goto") void qc.invalidateQueries({ queryKey: key });
    },
    onError: (e: Error) => toast(`Posição: ${e.message}`, true),
  });

  const st = state.data;
  const busy = light.isPending || dayNight.isPending;
  const presets = st?.presets ?? [];
  const nextFree = (() => {
    const used = new Set(presets.map((p) => p.id));
    for (let i = 1; i <= 255; i++) if (!used.has(i)) return i;
    return null;
  })();

  return (
    <>
      <section className="panel" aria-label="Posições salvas">
        <h2>Posições salvas</h2>
        {state.isLoading && <p className="muted">Lendo a câmera…</p>}
        {state.isError && <p className="notice error">Câmera não respondeu. {(state.error as Error).message}</p>}
        {st && presets.length === 0 && <p className="muted">Nenhuma posição salva ainda.</p>}
        <div className="presets">
          {presets.map((p) => (
            <button key={p.id} className="btn small" disabled={!canControl || preset.isPending} onClick={() => preset.mutate({ id: p.id, action: "goto" })} title={p.name}>
              {p.id}
            </button>
          ))}
        </div>
        {canControl && isAdmin && nextFree !== null && (
          <button className="btn ghost small" style={{ marginTop: 10 }} onClick={() => setConfirmSave(nextFree)}>
            <Icon name="plus" /> Salvar posição atual
          </button>
        )}
      </section>

      <section className="panel" aria-label="Imagem e luz">
        <h2>Imagem</h2>
        <div className="toggle-row">
          <span>
            <Icon name="light" /> Luz branca
          </span>
          <button
            className="switch"
            role="switch"
            aria-checked={!!st?.white_light}
            aria-label="Luz branca"
            disabled={!canControl || !st || busy}
            onClick={() => st && light.mutate(!st.white_light)}
          />
        </div>
        <div className="field" style={{ marginTop: 8 }}>
          <label htmlFor={`dn-${cameraId}`}>Dia e noite</label>
          <select
            id={`dn-${cameraId}`}
            className="input"
            value={st?.day_night || ""}
            disabled={!canControl || !st || busy}
            onChange={(e) => dayNight.mutate(e.target.value as DayNight)}
          >
            {!st?.day_night && <option value="">—</option>}
            {Object.entries(dayNightLabel).map(([v, l]) => (
              <option key={v} value={v}>
                {l}
              </option>
            ))}
          </select>
        </div>
        {st && (
          <p className="faint" style={{ fontSize: "0.82rem", marginTop: 10 }}>
            {st.model}
          </p>
        )}
      </section>

      {confirmSave !== null && (
        <div className="dialog-backdrop" role="dialog" aria-modal="true" aria-labelledby="save-title">
          <div className="dialog">
            <h2 id="save-title">Salvar posição {confirmSave}?</h2>
            <p className="muted">A posição atual da câmera fica gravada nela como a posição {confirmSave}.</p>
            <div className="dialog-actions">
              <button className="btn ghost" onClick={() => setConfirmSave(null)}>
                Cancelar
              </button>
              <button
                className="btn primary"
                onClick={() => {
                  preset.mutate({ id: confirmSave, action: "set" });
                  setConfirmSave(null);
                }}
              >
                Salvar posição
              </button>
            </div>
          </div>
        </div>
      )}
    </>
  );
}
