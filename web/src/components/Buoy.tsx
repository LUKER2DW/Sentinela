import { Sketch, type Pt } from "../lib/sketch";

// A service's status as a buoy in the harbour, drawn by the same hand as
// the lighthouse (lib/sketch). Its colour is the state (ok, busy, bad,
// idle); only a buoy being deployed bobs, and not under reduced motion.

let paths: Record<string, string> | null = null;

function buoyPaths() {
  const k = new Sketch(57, 0.55);
  const body: Pt[] = [
    [14.5, 33],
    [33.5, 33],
    [29.5, 15.5],
    [18.5, 15.5],
  ];
  const sea: Pt[] = [];
  for (let x = 3, i = 0; x <= 45; x += 6, i++) sea.push([x, 37 + (i % 2 ? 1.4 : -1.4)]);
  return {
    body: k.poly(body),
    band: new Sketch(12, 0.4).hachure(
      [
        [16.4, 26],
        [31.6, 26],
        [30.5, 21],
        [17.5, 21],
      ],
      1.9,
      -50,
    ),
    mast: k.line([24, 15.5], [24, 8.5]),
    lamp: k.ellipse(24, 6.6, 2.4, 2.4, 1),
    sea: k.curve(sea, 1),
  };
}

export type BuoyTone = "ok" | "busy" | "bad" | "idle";

export function Buoy({ tone, label }: { tone: BuoyTone; label?: string }) {
  paths ??= buoyPaths();
  const s = { fill: "none", stroke: "currentColor", strokeLinecap: "round" as const, strokeLinejoin: "round" as const };
  return (
    <svg className={`buoy ${tone}`} viewBox="0 0 48 44" role={label ? "img" : undefined} aria-label={label} aria-hidden={label ? undefined : true}>
      <g className="buoy-float">
        <path d={paths.band} {...s} strokeWidth={1.1} opacity={0.75} />
        <path d={paths.body} {...s} strokeWidth={1.7} />
        <path d={paths.mast} {...s} strokeWidth={1.5} />
        <path className="buoy-lamp" d={paths.lamp} {...s} strokeWidth={1.4} />
      </g>
      <path className="buoy-sea" d={paths.sea} {...s} strokeWidth={1.3} />
    </svg>
  );
}
