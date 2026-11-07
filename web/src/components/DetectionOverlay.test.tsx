import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { DetectionOverlay } from "./DetectionOverlay";
import { pushSeen } from "../lib/detections";
import { setOverlay } from "../lib/overlay";
import type { DetectionSettings } from "../lib/zones";

function render(settings: DetectionSettings) {
  const qc = new QueryClient();
  qc.setQueryData(["detection", "cam1"], settings);
  return renderToStaticMarkup(
    <QueryClientProvider client={qc}>
      <DetectionOverlay cameraId="cam1" videoW={1600} videoH={900} />
    </QueryClientProvider>,
  );
}

const base: DetectionSettings = { enabled: true, min_score: 0.45, zones: [], objects: ["person"], ignore_parked: true, min_size: 0 };
const gate = { name: "Portão", points: [[0.2, 0.5], [0.8, 0.5], [0.8, 1], [0.2, 1]] as [number, number][], objects: ["person" as const] };

describe("DetectionOverlay", () => {
  pushSeen("cam1", [
    { kind: "person", x: 0.4, y: 0.3, w: 0.1, h: 0.4, score: 0.82, zones: ["Portão"], parked: false, counted: true },
    { kind: "vehicle", x: 0.1, y: 0.6, w: 0.2, h: 0.2, score: 0.9, zones: [], parked: true, counted: false },
  ]);

  it("draws the counted objects with their name and the zone outlines", () => {
    setOverlay({ boxes: true, labels: true, score: true, all: false, zones: true });
    const html = render({ ...base, zones: [gate] });
    expect(html).toContain("det-box k-person");
    expect(html).toContain("Pessoa");
    expect(html).toContain("82%");
    expect(html).not.toContain("k-vehicle"); // does not warn: hidden unless "all"
    expect(html).toMatch(/<polygon points="200.0,500.0 800.0,500.0/);
  });

  it("shows what does not warn when asked, and nothing when all is off", () => {
    setOverlay({ all: true });
    expect(render(base)).toContain("det-box k-vehicle quiet");
    setOverlay({ boxes: false, zones: false });
    expect(render(base)).toBe("");
  });
});
