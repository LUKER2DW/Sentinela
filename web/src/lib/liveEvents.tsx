import { createContext, useContext, useEffect, useState, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { pushSeen } from "./detections";
import type { SeenObject } from "./zones";

interface LiveEvent {
  camera_id: string;
  type: string;
  active: boolean;
  at: string;
}

/** camera id → set of active event types */
type Active = Record<string, string[]>;

const Ctx = createContext<Active>({});

/** Subscribes once to the server's SSE stream and exposes active events. */
export function LiveEventsProvider({ children }: { children: ReactNode }) {
  const [active, setActive] = useState<Active>({});
  const qc = useQueryClient();

  useEffect(() => {
    const es = new EventSource("/api/events/stream");
    es.addEventListener("camera", (msg) => {
      const e = JSON.parse((msg as MessageEvent).data) as LiveEvent;
      setActive((prev) => {
        const cur = new Set(prev[e.camera_id] ?? []);
        if (e.active) cur.add(e.type);
        else cur.delete(e.type);
        return { ...prev, [e.camera_id]: [...cur] };
      });
      void qc.invalidateQueries({ queryKey: ["timeline"] });
      void qc.invalidateQueries({ queryKey: ["events"] });
    });
    // The detector's boxes for the live overlay (every analysed keyframe).
    es.addEventListener("objects", (msg) => {
      const e = JSON.parse((msg as MessageEvent).data) as { camera: string; objects: SeenObject[] };
      pushSeen(e.camera, e.objects ?? []);
    });
    return () => es.close();
  }, [qc]);

  return <Ctx.Provider value={active}>{children}</Ctx.Provider>;
}

export const useActiveEvents = () => useContext(Ctx);
