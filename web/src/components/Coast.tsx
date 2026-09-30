import { Sketch, type Pt } from "../lib/sketch";
import { LighthouseMark } from "./Lighthouse";

// A wide, low strip of coast for page banners (Minha conta): sky with a few
// stars or the sun, clouds, far islets, the lighthouse on a headland at the
// right, a sailboat, gulls and three sketched swells. 1200×170 units,
// anchored to the bottom so any banner height shows the sea.

type Paths = Record<string, string>;
let cache: Paths | null = null;
const H = 118; // horizon

function swell(s: Sketch, y: number, amp: number, step: number, passes = 1): string {
  const pts: Pt[] = [];
  for (let x = -20, i = 0; x <= 1220; x += step, i++) pts.push([x, y + (i % 2 ? amp : -amp)]);
  return s.curve(pts, passes);
}

function cloud(s: Sketch, cx: number, cy: number, w: number): string {
  const k = w / 100;
  const top: Pt[] = [
    [-50, 0],
    [-44, -14],
    [-26, -20],
    [-10, -34],
    [12, -28],
    [26, -32],
    [44, -14],
    [50, 0],
  ];
  return s.curve(top.map(([x, y]) => [cx + x * k, cy + y * k] as Pt), 1) + s.line([cx + 50 * k, cy], [cx - 50 * k, cy], 1);
}

function build(): Paths {
  const s = new Sketch(909, 0.8);
  const h = new Sketch(910, 0.6);
  const head: Pt[] = [
    [960, H],
    [990, 104],
    [1030, 96],
    [1080, 98],
    [1130, 90],
    [1180, 100],
    [1220, 104],
    [1220, H],
  ];
  const islet: Pt[] = [
    [610, H],
    [636, 110],
    [664, 104],
    [690, 110],
    [712, H],
  ];
  return {
    horizon: s.line([-20, H], [1220, H], 1),
    head: s.poly(head, false),
    headHatch: h.hachure(head, 5, 50),
    islet: s.poly(islet, false, 1),
    isletHatch: h.hachure(islet, 5, -50),
    clouds: [cloud(s, 380, 44, 90), cloud(s, 820, 30, 70), cloud(s, 560, 70, 50)].join(""),
    gulls: [
      [470, 60, 1],
      [494, 50, 0.8],
      [900, 64, 0.9],
    ]
      .map(([x, y, k]) =>
        s.curve([
          [x - 9 * k, y + 2 * k],
          [x - 4 * k, y - 3 * k],
          [x, y + 1.5 * k],
          [x + 4 * k, y - 3.5 * k],
          [x + 9 * k, y + 2 * k],
        ], 1),
      )
      .join(""),
    boat:
      s.poly([
        [-16, 0],
        [16, 0],
        [11, 6],
        [-11, 6],
      ]) +
      s.line([0, 0], [0, -30]) +
      s.poly([
        [2, -28],
        [15, -4],
        [2, -4],
      ], false) +
      s.poly([
        [-2, -23],
        [-12, -4],
        [-2, -4],
      ], false, 1),
    swells: [swell(s, 130, 2, 26), swell(s, 146, 3, 34), swell(s, 162, 3.6, 42, 2)].join("|"),
    glitter: [200, 250, 300, 350].map((x, i) => s.line([x, 126 + (i % 2) * 8], [x + 14, 126 + (i % 2) * 8], 1)).join(""),
    stars: [
      [120, 26],
      [210, 60],
      [300, 18],
      [640, 22],
      [720, 58],
      [980, 30],
    ]
      .map(([x, y]) => s.line([x - 3, y], [x + 3, y], 1) + s.line([x, y - 3], [x, y + 3], 1))
      .join(""),
    sun: s.ellipse(160, 46, 16, 16),
  };
}

export function CoastBanner({ className = "" }: { className?: string }) {
  cache ??= build();
  const p = cache;
  return (
    <svg className={`coast ${className}`} viewBox="0 0 1200 170" preserveAspectRatio="xMaxYMax slice" aria-hidden="true">
      <path d={`M-20 ${H}H1220V180H-20Z`} className="coast-water" />
      <path d={p.stars} className="coast-stars" />
      <path d={p.sun} className="coast-sun" />
      <path d={p.clouds} className="coast-cloud" />
      <path d={p.gulls} className="coast-gull" />
      <path d={p.horizon} className="coast-ink faint" />
      <path d={p.isletHatch} className="coast-hatch" />
      <path d={p.islet} className="coast-ink" />
      <path d={`M${[960, H]}L990 104L1030 96L1080 98L1130 90L1180 100L1220 104V${H}Z`} className="coast-land" />
      <path d={p.headHatch} className="coast-hatch" />
      <path d={p.head} className="coast-ink" />
      <LighthouseMark at={[1044, 34, 70]} className="coast-lh" />
      <g transform="translate(760 132)">
        <g className="coast-boat">
          <path d={p.boat} className="coast-ink" />
        </g>
      </g>
      <path d={p.glitter} className="coast-glitter" />
      {p.swells.split("|").map((d, i) => (
        <path key={i} d={d} className={`coast-ink coast-swell s${i}`} />
      ))}
    </svg>
  );
}
