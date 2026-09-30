import { Sketch, type Pt } from "../lib/sketch";

// A faint nautical chart behind the sign-in card: rhumb lines fanning out
// of the compass rose (as on old portolan charts), depth contours around two
// islets, soundings, a dotted route with a small ship, and a few shells.
// 600×900 units, sliced to cover the panel. Drawn once, aria-hidden.

type Paths = Record<string, string>;
let cache: Paths | null = null;

// The compass rose sits at the bottom right of the panel (.auth-compass).
const ROSE: Pt = [520, 820];

function blob(s: Sketch, cx: number, cy: number, r: number, wob: number, seed: number): string {
  const pts: Pt[] = [];
  for (let i = 0; i <= 16; i++) {
    const a = (i / 16) * Math.PI * 2;
    const k = 1 + Math.sin(a * 3 + seed) * wob + Math.cos(a * 2 - seed) * wob * 0.6;
    pts.push([cx + Math.cos(a) * r * k * 1.25, cy + Math.sin(a) * r * k]);
  }
  return s.curve(pts, 1);
}

function build(): Paths {
  const s = new Sketch(313, 0.8);
  const h = new Sketch(314, 0.6);
  const rhumbs = Array.from({ length: 16 }, (_, i) => {
    const a = (i / 16) * Math.PI * 2;
    return s.line(ROSE, [ROSE[0] + Math.cos(a) * 1100, ROSE[1] + Math.sin(a) * 1100], 1);
  }).join("");
  const islet: Pt[] = [
    [70, 150],
    [96, 128],
    [132, 124],
    [158, 142],
    [150, 168],
    [112, 178],
    [80, 172],
  ];
  const route: Pt[] = [
    [40, 700],
    [120, 610],
    [230, 560],
    [300, 450],
    [420, 360],
    [500, 250],
    [560, 120],
  ];
  const routeD = s.curve(route, 1);
  const ship =
    s.poly([
      [-9, 0],
      [9, 0],
      [6, 4],
      [-6, 4],
    ], true, 1) +
    s.line([0, 0], [0, -12], 1) +
    s.poly([
      [1, -11],
      [8, -2],
      [1, -2],
    ], false, 1);
  const shell = (x: number, y: number) =>
    s.curve([
      [x - 12, y + 8],
      [x - 9, y - 6],
      [x, y - 12],
      [x + 9, y - 6],
      [x + 12, y + 8],
      [x, y + 10],
      [x - 12, y + 8],
    ], 1) + [-8, -4, 0, 4, 8].map((dx) => s.line([x, y + 12], [x + dx, y - 8 + Math.abs(dx) * 0.4], 1)).join("");
  return {
    rhumbs,
    grid: [150, 300, 450, 600, 750]
      .map((y) => s.line([-20, y], [620, y], 1))
      .concat([150, 300, 450].map((x) => s.line([x, -20], [x, 920], 1)))
      .join(""),
    islet: s.poly(islet),
    isletHatch: h.hachure(islet, 5, 40),
    contours: [blob(s, 114, 152, 60, 0.12, 1), blob(s, 114, 152, 92, 0.16, 2), blob(s, 460, 520, 34, 0.14, 3), blob(s, 460, 520, 60, 0.18, 4)].join(""),
    rock: s.poly([
      [442, 516],
      [456, 506],
      [474, 510],
      [480, 524],
      [462, 530],
    ]),
    route: routeD,
    ship,
    shells: shell(90, 830) + shell(330, 120),
    marks: [
      [120, 610],
      [300, 450],
      [500, 250],
    ]
      .map(([x, y]) => s.line([x - 5, y - 5], [x + 5, y + 5], 1) + s.line([x + 5, y - 5], [x - 5, y + 5], 1))
      .join(""),
  };
}

const SOUNDINGS: [number, number, string][] = [
  [60, 300, "12"],
  [210, 240, "18"],
  [380, 180, "25"],
  [250, 700, "9"],
  [350, 780, "7"],
  [540, 420, "31"],
  [170, 470, "14"],
  [420, 640, "22"],
  [30, 520, "6"],
];

export function ChartBackdrop({ className = "" }: { className?: string }) {
  cache ??= build();
  const p = cache;
  return (
    <svg className={`chart ${className}`} viewBox="0 0 600 900" preserveAspectRatio="xMidYMid slice" aria-hidden="true">
      <path d={p.grid} className="chart-grid" />
      <path d={p.rhumbs} className="chart-rhumb" />
      <path d={p.contours} className="chart-contour" />
      <path d={p.isletHatch} className="chart-hatch" />
      <path d={p.islet} className="chart-ink" />
      <path d={p.rock} className="chart-ink" />
      <path d={p.route} className="chart-route" />
      <path d={p.marks} className="chart-ink" />
      <g transform="translate(230 560) rotate(-28)">
        <path d={p.ship} className="chart-ink" />
      </g>
      <path d={p.shells} className="chart-ink" />
      <g className="chart-soundings">
        {SOUNDINGS.map(([x, y, t]) => (
          <text key={`${x}-${y}`} x={x} y={y}>
            {t}
          </text>
        ))}
      </g>
    </svg>
  );
}
