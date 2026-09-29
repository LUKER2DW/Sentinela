import { useId } from "react";
import { Sketch, type Pt } from "../lib/sketch";

// The Sentinela lighthouse, hand-drawn (lib/sketch). Two drawings share one
// design: a detailed scene for the sign-in page and a small brand mark. In
// both the lamp turns: the beam is a cone scaled by cos(angle) around the lamp,
// so it sweeps right, flashes toward the viewer, sweeps left and dims behind
// the tower. Geometry is generated once at module load (fixed seeds).

type Paths = Record<string, string>;

// ---------------------------------------------------------------- scene ----

const L: Pt = [200, 118]; // lamp centre in the scene

function towerX(y: number): [number, number] {
  const k = (y - 156) * (16 / 194);
  return [178 - k, 222 + k];
}

function band(y1: number, y2: number): Pt[] {
  const [a1, b1] = towerX(y1);
  const [a2, b2] = towerX(y2);
  return [
    [a1, y1],
    [b1, y1],
    [b2, y2],
    [a2, y2],
  ];
}

function wave(y: number, amp: number, from: number, to: number, step: number): Pt[] {
  const pts: Pt[] = [];
  for (let x = from, i = 0; x <= to; x += step, i++) pts.push([x, y + (i % 2 ? amp : -amp)]);
  return pts;
}

const TOWER: Pt[] = [
  [178, 156],
  [222, 156],
  [238, 350],
  [162, 350],
];
const BANDS = [band(184, 212), band(244, 272), band(304, 330)];
const ROCKS: Pt[] = [
  [108, 376],
  [118, 364],
  [134, 355],
  [152, 349],
  [200, 352],
  [252, 350],
  [298, 352],
  [313, 360],
  [330, 376],
];
const HOUSE: Pt[] = [
  [246, 324],
  [292, 324],
  [292, 352],
  [246, 352],
];
const ROOF: Pt[] = [
  [240, 326],
  [268, 303],
  [298, 326],
];
const DOME: Pt[] = [
  [182, 92],
  [187, 83],
  [194, 77],
  [200, 75],
  [206, 77],
  [213, 83],
  [218, 92],
];

function buildScene(): Paths {
  const s = new Sketch(7, 1);
  const hs = new Sketch(11, 0.8);
  const posts: string[] = [];
  for (let x = 166; x <= 235; x += 8.6) posts.push(s.line([x, 129], [x, 146], 1));
  const doorArch: Pt[] = [
    [191, 350],
    [191, 333],
    [194, 327],
    [200, 325],
    [206, 327],
    [209, 333],
    [209, 350],
  ];
  const win = (y: number): Pt[] => [
    [194.5, y + 13],
    [194.5, y + 5],
    [196.5, y + 1.5],
    [200, y],
    [203.5, y + 1.5],
    [205.5, y + 5],
    [205.5, y + 13],
    [194.5, y + 13],
  ];
  return {
    // fills (clean shapes; the sketch strokes sit slightly off them, like ink over wash)
    towerFill: "M178 156H222L238 350H162Z",
    bandsFill: BANDS.map((b) => `M${b.map((p) => p.join(" ")).join("L")}Z`).join(""),
    houseFill: "M246 324H292V352H246ZM240 326L268 303L298 326Z",
    doorFill: "M191 350V333Q193 325 200 325Q207 325 209 333V350Z",
    glassFill: "M184 98H216V146H184Z",
    roofFill: "M178 98H222L220 92H180ZM182 92Q186 80 200 75Q214 80 218 92Z",
    platformFill: "M160 146H240L236 156H164Z",
    rockFill: `M${ROCKS.map((p) => p.join(" ")).join("L")}Z`,

    // ink
    tower: s.poly(TOWER),
    bands: BANDS.map((b) => s.line(b[0], b[1]) + s.line(b[3], b[2])).join(""),
    bandHatch: BANDS.map((b) => hs.hachure(b, 3.4, -38)).join(""),
    windows: s.curve(win(222)) + s.curve(win(282)) + s.line([192, 236.5], [208, 236.5], 1) + s.line([192, 296.5], [208, 296.5], 1),
    windowsFill: "M194.5 235V227Q196 222 200 222Q204 222 205.5 227V235ZM194.5 295V287Q196 282 200 282Q204 282 205.5 287V295Z",
    door: s.curve(doorArch) + s.line([184, 353], [216, 353]) + s.line([180, 357.5], [220, 357.5], 1),
    platform: s.poly([
      [160, 146],
      [240, 146],
      [236, 156],
      [164, 156],
    ]),
    corbels: s.line([168, 156], [178, 165], 1) + s.line([232, 156], [222, 165], 1) + s.line([188, 156], [190, 163], 1) + s.line([212, 156], [210, 163], 1),
    rail: s.line([163, 129], [237, 129]) + posts.join(""),
    lantern: s.poly([
      [184, 98],
      [216, 98],
      [216, 146],
      [184, 146],
    ]) + s.line([194.7, 98], [194.7, 146], 1) + s.line([205.3, 98], [205.3, 146], 1),
    roof: s.poly([
      [178, 98],
      [222, 98],
      [220, 92],
      [180, 92],
    ]) + s.curve(DOME),
    roofHatch: hs.hachure(
      [
        [184, 92],
        [190, 81],
        [200, 76],
        [210, 81],
        [216, 92],
      ],
      2.6,
      40,
    ),
    finial: s.ellipse(200, 70.5, 4.2, 4.2) + s.line([200, 66], [200, 48]) + s.poly(
      [
        [200, 49.5],
        [213, 52.5],
        [200, 55.5],
      ],
      false,
      1,
    ),
    house: s.poly(HOUSE) + s.poly(ROOF, false) + s.poly(
      [
        [279, 315],
        [279, 300],
        [287, 300],
        [287, 320],
      ],
      false,
    ),
    houseBits:
      s.poly(
        [
          [253, 331],
          [266, 331],
          [266, 342],
          [253, 342],
        ],
        true,
        1,
      ) +
      s.line([259.5, 331], [259.5, 342], 1) +
      s.line([253, 336.5], [266, 336.5], 1) +
      s.poly(
        [
          [274, 352],
          [274, 335],
          [284, 335],
          [284, 352],
        ],
        false,
      ),
    smoke: new Sketch(3, 0.6).curve(
      [
        [283, 297],
        [279, 289],
        [286, 281],
        [281, 272],
        [288, 262],
      ],
      1,
    ),
    rocks: s.poly(ROCKS, false),
    rockHatch: hs.hachure(ROCKS, 5, 52),
    pebbles: s.poly([
      [84, 377],
      [92, 369],
      [102, 370],
      [108, 377],
    ], false) + s.poly([
      [330, 377],
      [338, 368],
      [349, 371],
      [354, 378],
    ], false),
    foam: s.curve([
      [98, 381],
      [106, 377],
      [114, 381],
    ], 1) + s.curve([
      [336, 382],
      [344, 378],
      [352, 382],
    ], 1),

  };
}

let scene: Paths | null = null;

/** Lamp centre of the lighthouse figure, in figure units (for beam and halo). */
export const LAMP = L;

/**
 * The lighthouse on its rock with the keeper's house, in its own 400×420
 * units. The sign-in panorama places, scales and lights it (beam and halo are
 * drawn by the caller, behind and over the lantern respectively).
 */
export function LighthouseFigure({ halo }: { halo: React.ReactNode }) {
  scene ??= buildScene();
  const p = scene;
  return (
    <g className="lh-figure">
      <path d={p.smoke} className="lh-smoke" />
      <path d={p.houseFill} className="f-paper" />
      <path d={p.house} className="ink" />
      <path d={p.houseBits} className="ink thin" />

      <path d={p.towerFill} className="f-paper" />
      <path d={p.bandsFill} className="f-band" />
      <path d={p.bandHatch} className="hatch-band" />
      <path d={p.tower} className="ink" />
      <path d={p.bands} className="ink thin" />
      <path d={p.windowsFill} className="f-glass" />
      <path d={p.windows} className="ink thin" />
      <path d={p.doorFill} className="f-paper" />
      <path d={p.door} className="ink" />

      <path d={p.platformFill} className="f-paper" />
      <path d={p.corbels} className="ink thin" />
      <path d={p.glassFill} className="f-glass" />
      {halo}
      <circle cx={L[0]} cy={L[1]} r="6.5" className="f-lamp" />
      <path d={p.lantern} className="ink thin" />
      <path d={p.platform} className="ink" />
      <path d={p.rail} className="ink thin" />
      <path d={p.roofFill} className="f-roof" />
      <path d={p.roofHatch} className="hatch-roof" />
      <path d={p.roof} className="ink" />
      <path d={p.finial} className="ink" />

      <path d={p.rockFill} className="f-rock" />
      <path d={p.rockHatch} className="hatch-rock" />
      <path d={p.rocks} className="ink" />
      <path d={p.pebbles} className="ink thin" />
      <path d={p.foam} className="ink thin lh-foam" />
    </g>
  );
}

// ----------------------------------------------------------------- mark ----

const M: Pt = [24, 14]; // lamp centre in the mark

function buildMark(): Paths {
  const s = new Sketch(5, 0.22);
  const bands = [
    [24.2, 28.2],
    [32.6, 36.6],
  ].map(([y1, y2]) => {
    const x = (y: number) => [19.6 - (y - 19) * (2.4 / 21), 28.4 + (y - 19) * (2.4 / 21)];
    const [a1, b1] = x(y1);
    const [a2, b2] = x(y2);
    return `M${a1} ${y1}H${b1}L${b2} ${y2}H${a2}Z`;
  });
  return {
    bodyFill: "M19.6 19H28.4L30.8 40H17.2Z",
    bandsFill: bands.join(""),
    body: s.poly([
      [19.6, 19],
      [28.4, 19],
      [30.8, 40],
      [17.2, 40],
    ]),
    gallery: s.poly([
      [16.6, 17.2],
      [31.4, 17.2],
      [30.6, 19.2],
      [17.4, 19.2],
    ]),
    lantern: s.poly([
      [20.6, 10.8],
      [27.4, 10.8],
      [27.4, 17.2],
      [20.6, 17.2],
    ]),
    domeFill: "M19.6 10.8Q20.6 6.4 24 5.8Q27.4 6.4 28.4 10.8Z",
    dome: s.curve([
      [19.6, 10.8],
      [21, 7.3],
      [24, 5.8],
      [27, 7.3],
      [28.4, 10.8],
    ]) + s.line([24, 5.8], [24, 2.8]),
    door: s.curve([
      [22.6, 40],
      [22.6, 36.8],
      [24, 35.4],
      [25.4, 36.8],
      [25.4, 40],
    ], 1),
    rock: s.poly(
      [
        [12.5, 42.6],
        [16.4, 40.2],
        [31.6, 40.2],
        [35.5, 42.6],
      ],
      false,
    ),
    sea: s.curve(wave(45.2, 1.1, 2, 46, 4.4), 1),
  };
}

let mark: Paths | null = null;

/** Brand mark: the same lighthouse, small, with its turning beam. */
export function LighthouseMark({
  className = "",
  still = false,
  at,
}: {
  className?: string;
  still?: boolean;
  /** Position inside a parent SVG (x, y, size), when nested in a scene. */
  at?: [number, number, number];
}) {
  mark ??= buildMark();
  const p = mark;
  const id = useId().replace(/:/g, "");
  return (
    <svg
      className={`lh-mark${still ? " still" : ""} ${className}`}
      viewBox="0 0 48 48"
      aria-hidden="true"
      {...(at ? { x: at[0], y: at[1], width: at[2], height: at[2], overflow: "visible" } : {})}
    >
      <defs>
        <linearGradient id={`mb-${id}`} gradientUnits="userSpaceOnUse" x1={M[0]} y1="0" x2="50" y2="0">
          <stop offset="0" stopColor="var(--lamp)" stopOpacity="0.85" />
          <stop offset="1" stopColor="var(--lamp)" stopOpacity="0" />
        </linearGradient>
      </defs>
      <path className="lh-beam" d={`M${M[0]} ${M[1] - 1}L50 6V22L${M[0]} ${M[1] + 1}Z`} fill={`url(#mb-${id})`} />
      <path d={p.bodyFill} className="f-paper" />
      <path d={p.bandsFill} className="f-band solid" />
      <path d={p.body} className="ink" />
      <path d={p.door} className="ink thin" />
      <path d="M20.6 10.8H27.4V17.2H20.6Z" className="f-glass" />
      <circle cx={M[0]} cy={M[1]} r="2.2" className="f-lamp lh-lamp" />
      <path d={p.lantern} className="ink thin" />
      <path d="M16.6 17.2H31.4L30.6 19.2H17.4Z" className="f-roof" />
      <path d={p.gallery} className="ink thin" />
      <path d={p.domeFill} className="f-roof" />
      <path d={p.dome} className="ink" />
      <path d={p.rock} className="ink" />
      <path d={p.sea} className="ink thin lh-sea" />
    </svg>
  );
}
