import { useId } from "react";
import { Sketch, type Pt } from "../lib/sketch";
import { LAMP, LighthouseFigure } from "./Lighthouse";

// The sign-in panorama: a whole seascape drawn by hand (lib/sketch) that
// fills the left half of the screen. 800×900 units, sliced to cover. The
// lighthouse is the subject; around it: sky (sun or moon, clouds, stars, a
// flock), the horizon with islands and a steamer crossing, a brig at anchor,
// a blinking buoy, a whale, a jumping fish and the sea. At night the
// Southern Cross and now and then a shooting star; far hills, pines and a
// second small lighthouse on the islands; the moon (or sun) path on the
// water; kelp, a jellyfish, bubbles and a school of fish below the surface.
// All motion is CSS and stops under prefers-reduced-motion.

const FIG = { x: 96, y: 296, k: 1.2 }; // lighthouse placement
const LX = FIG.x + LAMP[0] * FIG.k;
const LY = FIG.y + LAMP[1] * FIG.k;
const HORIZON = 610;

type Paths = Record<string, string>;

function waves(y: number, amp: number, step: number, from = -40, to = 840): Pt[] {
  const pts: Pt[] = [];
  for (let x = from, i = 0; x <= to; x += step, i++) pts.push([x, y + (i % 2 ? amp : -amp)]);
  return pts;
}

function cloud(s: Sketch, cx: number, cy: number, w: number): string {
  const h = w * 0.42;
  const top: [number, number][] = [
    [-0.5, 0],
    [-0.46, -0.32],
    [-0.32, -0.5],
    [-0.18, -0.46],
    [-0.04, -0.82],
    [0.16, -0.66],
    [0.3, -0.74],
    [0.44, -0.38],
    [0.5, 0],
  ];
  return s.curve(top.map(([x, y]) => [cx + x * w, cy + y * h])) + s.line([cx + 0.5 * w, cy], [cx - 0.5 * w, cy]);
}

function gull(s: Sketch, x: number, y: number, k: number): string {
  return s.curve(
    [
      [x - 12 * k, y + 3 * k],
      [x - 6 * k, y - 4 * k],
      [x, y + 2 * k],
      [x + 6 * k, y - 4.5 * k],
      [x + 12 * k, y + 2.5 * k],
    ],
    1,
  );
}

function build(): Paths {
  const s = new Sketch(101, 1);
  const h = new Sketch(202, 0.8);

  // steamer, local: waterline y=0, bow at the left (it sails left)
  const hull: Pt[] = [
    [-48, -11],
    [42, -11],
    [38, 0],
    [-40, 0],
  ];
  const steamer =
    s.poly(hull) +
    s.poly([
      [-14, -11],
      [-14, -19],
      [18, -19],
      [18, -11],
    ], false) +
    s.poly([
      [-8, -19],
      [-8, -24],
      [4, -24],
      [4, -19],
    ], false, 1) +
    s.poly([
      [7, -19],
      [8, -32],
      [14, -32],
      [14, -19],
    ], false) +
    s.line([-38, -11], [-38, -28], 1) +
    s.line([-38, -27], [-8, -23], 1) +
    [-30, -20, -10, 0, 10, 20].map((x) => s.ellipse(x, -5.5, 1.3, 1.3, 1)).join("");
  const steamerHatch = h.hachure(hull, 3, 30);
  const smoke = s.curve(
    [
      [11, -34],
      [18, -42],
      [30, -44],
      [44, -52],
      [60, -52],
    ],
    1,
  );

  // brig, local: waterline y=0, bow at the right
  const brigHull: Pt[] = [
    [-60, -14],
    [58, -14],
    [48, 2],
    [-50, 2],
  ];
  const sail = (mx: number, y1: number, y2: number, w: number) =>
    s.curve([
      [mx - w / 2, y1],
      [mx, y1 - 2],
      [mx + w / 2, y1],
    ], 1) +
    s.curve([
      [mx + w / 2, y1],
      [mx + w / 2 + 3, (y1 + y2) / 2],
      [mx + w / 2, y2],
    ], 1) +
    s.line([mx + w / 2, y2], [mx - w / 2, y2], 1) +
    s.curve([
      [mx - w / 2, y2],
      [mx - w / 2 + 3, (y1 + y2) / 2],
      [mx - w / 2, y1],
    ], 1);
  const brig =
    s.poly(brigHull) +
    s.poly([
      [-60, -14],
      [-60, -24],
      [-42, -24],
      [-42, -14],
    ], false) +
    s.line([-25, -14], [-25, -94]) +
    s.line([12, -14], [12, -102]) +
    s.line([58, -14], [84, -28]) +
    sail(-25, -88, -66, 30) +
    sail(-25, -62, -34, 38) +
    sail(12, -96, -72, 32) +
    sail(12, -68, -38, 40) +
    s.poly([
      [14, -98],
      [82, -28],
      [16, -34],
    ], false, 1) +
    s.poly([
      [12, -102],
      [24, -99],
      [12, -96],
    ], false, 1) +
    [-36, -22, -8, 6, 20, 34].map((x) => s.ellipse(x, -7, 1.6, 1.6, 1)).join("");
  const brigHatch = h.hachure(brigHull, 3.4, -35);

  // buoy, local: waterline y=0
  const buoyBody: Pt[] = [
    [-12, 0],
    [12, 0],
    [7, -34],
    [-7, -34],
  ];
  const buoy =
    s.poly(buoyBody) +
    s.ellipse(0, 1, 17, 4.5, 1) +
    s.line([-5, -34], [-4, -46], 1) +
    s.line([5, -34], [4, -46], 1) +
    s.line([-5.5, -46], [5.5, -46], 1);
  const buoyBands = h.hachure(
    [
      [-10.5, -8],
      [10.5, -8],
      [9, -19],
      [-9, -19],
    ],
    2.6,
    -40,
  );

  const island1: Pt[] = [
    [462, HORIZON],
    [492, 598],
    [520, 591],
    [548, 579],
    [574, 584],
    [604, 596],
    [650, HORIZON],
  ];
  const island2: Pt[] = [
    [690, HORIZON],
    [716, 601],
    [742, 604],
    [770, HORIZON],
  ];

  const sunRays = Array.from({ length: 12 }, (_, i) => {
    const a = (i * Math.PI) / 6;
    return s.line([650 + Math.cos(a) * 40, 128 + Math.sin(a) * 40], [650 + Math.cos(a) * 54, 128 + Math.sin(a) * 54], 1);
  }).join("");
  const moon = s.curve([
    ...[-60, -95, -130, -165, -200, -235, -270, -300].map((deg) => {
      const a = (deg * Math.PI) / 180;
      return [650 + Math.cos(a) * 34, 128 + Math.sin(a) * 34] as Pt;
    }),
    [653, 142],
    [648, 124],
    [654, 106],
    [667, 98.5],
  ]);

  const stars: Pt[] = [
    [60, 70],
    [170, 40],
    [300, 90],
    [420, 50],
    [520, 150],
    [760, 60],
    [740, 230],
    [40, 250],
    [210, 210],
    [560, 330],
    [770, 420],
    [120, 400],
  ];

  const flock: [number, number][] = [
    [0, 0],
    [-18, -9],
    [-36, -18],
    [-54, -27],
    [-18, 9],
    [-36, 18],
    [-54, 27],
  ];

  // Southern Cross (Cruzeiro do Sul): the sky over the house.
  const cross: Pt[] = [
    [388, 112],
    [390, 222],
    [352, 160],
    [428, 150],
    [410, 186],
  ];
  const crossSize = [5, 6, 4.5, 4, 2.5];

  const hills: Pt[] = [
    [236, HORIZON],
    [270, 596],
    [302, 600],
    [338, 582],
    [372, 590],
    [410, 578],
    [446, HORIZON],
  ];
  const pine = (x: number, y: number, k: number) =>
    s.poly([
      [x - 4 * k, y],
      [x, y - 13 * k],
      [x + 4 * k, y],
    ], false, 1) + s.line([x - 5 * k, y - 4 * k], [x + 5 * k, y - 4 * k], 1);

  const weed = (x: number, top: number, lean: number) =>
    s.curve(
      Array.from({ length: 6 }, (_, i) => [x + Math.sin(i * 1.3) * 5 + (i * lean) / 5, 905 - (i * (905 - top)) / 5] as Pt),
      1,
    );

  const jelly =
    s.curve([
      [-14, 0],
      [-12, -10],
      [0, -15],
      [12, -10],
      [14, 0],
    ]) +
    s.curve([
      [-14, 0],
      [-7, 2.5],
      [0, 0],
      [7, 2.5],
      [14, 0],
    ], 1) +
    [-9, -3, 3, 9]
      .map((x, i) =>
        s.curve([
          [x, 2],
          [x + (i % 2 ? 3 : -3), 10],
          [x, 18],
          [x + (i % 2 ? -3 : 3), 26],
        ], 1),
      )
      .join("");

  const smallFish = (x: number, y: number, k: number) =>
    s.curve([
      [x, y],
      [x + 5 * k, y - 3 * k],
      [x + 11 * k, y],
      [x + 5 * k, y + 3 * k],
      [x, y],
    ], 1) +
    s.poly([
      [x + 11 * k, y],
      [x + 15 * k, y - 3 * k],
      [x + 15 * k, y + 3 * k],
    ], true, 1);

  const reflect = [618, 628, 640, 654, 670, 688, 708]
    .map((y, i) => {
      const w = 26 - i * 2.6;
      return s.line([650 - w + (i % 2) * 5, y], [650 + w - (i % 2 ? 0 : 6), y], 1);
    })
    .join("");

  return {
    cross: cross.map(([x, y], i) => {
      const r = crossSize[i];
      return s.line([x - r, y], [x + r, y], 1) + s.line([x, y - r], [x, y + r], 1);
    }).join("|"),
    crossLines: s.line(cross[0], cross[1], 1) + s.line(cross[2], cross[3], 1),
    shooting: s.line([640, 34], [548, 78], 1),
    hills: s.poly(hills, false, 1),
    hillsHatch: h.hachure(hills, 6, 60),
    pines: [pine(506, 592, 1), pine(518, 588, 1.2), pine(560, 581, 1.1), pine(588, 588, 0.9)].join(""),
    farTower:
      s.poly([
        [737, 604],
        [747, 604],
        [745.5, 588],
        [738.5, 588],
      ], false, 1) + s.line([736.5, 588], [747.5, 588], 1),
    reflect,
    weeds: [weed(28, 812, 8), weed(52, 842, -6), weed(74, 826, 4), weed(760, 830, -8), weed(784, 850, 5)].join("|"),
    jelly,
    bubbles: [
      [34, 800, 3],
      [44, 772, 2],
      [30, 748, 1.6],
      [770, 812, 2.6],
      [778, 786, 1.8],
    ]
      .map(([x, y, r]) => s.ellipse(x, y, r, r, 1))
      .join("|"),
    school: smallFish(0, 0, 1) + smallFish(20, 9, 0.8) + smallFish(-2, 17, 0.9),
    moreClouds: [cloud(s, 300, 318, 64), cloud(s, 600, 52, 86)].join("|"),
    stars: stars.map(([x, y]) => s.line([x - 4, y], [x + 4, y], 1) + s.line([x, y - 4], [x, y + 4], 1)).join("|"),
    sun: s.ellipse(650, 128, 28, 28),
    sunRays,
    moon,
    craters: s.ellipse(638, 118, 3.5, 3.5, 1) + s.ellipse(632, 140, 2.4, 2.4, 1),
    clouds: [cloud(s, 150, 160, 130), cloud(s, 470, 222, 92), cloud(s, 712, 330, 118)].join("|"),
    flock: flock.map(([x, y]) => gull(s, x, y, 0.8)).join("|"),
    gulls: [gull(s, 560, 470, 1.3), gull(s, 612, 446, 1)].join("|"),
    horizon: s.line([-20, HORIZON], [820, HORIZON], 1),
    island1: s.poly(island1, false),
    island1Fill: `M${island1.map((p) => p.join(" ")).join("L")}Z`,
    island1Hatch: h.hachure(island1, 4, 50),
    island2: s.poly(island2, false),
    island2Fill: `M${island2.map((p) => p.join(" ")).join("L")}Z`,
    steamer,
    steamerHatch,
    smoke,
    brig,
    brigHatch,
    brigHullFill: `M${brigHull.map((p) => p.join(" ")).join("L")}Z`,
    buoy,
    buoyBands,
    buoyFill: `M${buoyBody.map((p) => p.join(" ")).join("L")}Z`,
    whale: s.curve([
      [238, 652],
      [250, 643],
      [268, 641],
      [284, 648],
    ], 1) + s.curve([
      [262, 640],
      [259, 630],
      [252, 625],
    ], 1) + s.curve([
      [262, 640],
      [264, 628],
      [271, 623],
    ], 1),
    fish: s.curve([
      [0, 0],
      [7, -5],
      [16, 0],
      [7, 5],
      [0, 0],
    ], 1) + s.poly([
      [16, 0],
      [22, -4],
      [21, 4],
    ], true, 1),
    sailboat:
      s.poly([
        [-20, -4],
        [20, -4],
        [14, 4],
        [-14, 4],
      ]) +
      s.line([0, -4], [0, -38]) +
      s.poly([
        [2, -36],
        [18, -8],
        [2, -8],
      ], false) +
      s.poly([
        [-2, -30],
        [-14, -8],
        [-2, -8],
      ], false, 1),
    beamEdges: s.line([LX, LY], [1150, 250], 1) + s.line([LX, LY], [1150, 625], 1),
    glitterR: [470, 520, 575, 630, 690, 745].map((x, i) => s.line([x, 652 + (i % 3) * 14], [x + 14 + (i % 2) * 6, 652 + (i % 3) * 14], 1)).join(""),
    glitterL: [20, 70, 120, 170].map((x, i) => s.line([x, 650 + (i % 3) * 14], [x + 14, 650 + (i % 3) * 14], 1)).join(""),
    waterFill: `M-20 ${HORIZON}H820V900H-20Z`,
    back: [
      [628, 1.4, 18],
      [646, 1.8, 20],
      [668, 2.3, 22],
      [694, 2.8, 26],
    ]
      .map(([y, a, st]) => s.curve(waves(y, a, st), 1))
      .join("|"),
    front: [
      [728, 3.3, 30],
      [768, 3.8, 34],
      [812, 4.4, 38],
      [862, 5, 44],
    ]
      .map(([y, a, st]) => s.curve(waves(y, a, st)))
      .join("|"),
  };
}

let paths: Paths | null = null;

export function Panorama({ className = "" }: { className?: string }) {
  paths ??= build();
  const p = paths;
  const id = useId().replace(/:/g, "");
  return (
    <svg className={`pano ${className}`} viewBox="0 0 800 900" preserveAspectRatio="xMidYMid slice" aria-hidden="true">
      <defs>
        <linearGradient id={`pb-${id}`} gradientUnits="userSpaceOnUse" x1={LX} y1="0" x2="1150" y2="0">
          <stop offset="0" stopColor="var(--lamp)" stopOpacity="0.7" />
          <stop offset="0.45" stopColor="var(--lamp)" stopOpacity="0.2" />
          <stop offset="1" stopColor="var(--lamp)" stopOpacity="0" />
        </linearGradient>
        <radialGradient id={`ph-${id}`}>
          <stop offset="0" stopColor="var(--lamp)" stopOpacity="0.9" />
          <stop offset="0.4" stopColor="var(--lamp)" stopOpacity="0.35" />
          <stop offset="1" stopColor="var(--lamp)" stopOpacity="0" />
        </radialGradient>
      </defs>

      {/* sky */}
      <g className="lh-stars">
        {p.stars.split("|").map((d, i) => (
          <path key={i} d={d} style={{ animationDelay: `${-i * 0.9}s` }} />
        ))}
      </g>
      <g className="lh-stars pano-cross">
        <path d={p.crossLines} className="pano-cross-lines" />
        {p.cross.split("|").map((d, i) => (
          <path key={i} d={d} style={{ animationDelay: `${-i * 1.3}s` }} />
        ))}
      </g>
      <path d={p.shooting} className="pano-shoot" />
      <g className="pano-sun">
        <path d={p.sun} className="ink" />
        <path d={p.sunRays} className="ink thin pano-rays" />
      </g>
      <g className="pano-moon">
        <path d={p.moon} className="ink" />
        <path d={p.craters} className="ink thin" />
      </g>
      {p.clouds.split("|").map((d, i) => (
        <path key={i} d={d} className={`pano-cloud c${i}`} />
      ))}
      {p.moreClouds.split("|").map((d, i) => (
        <path key={i} d={d} className={`pano-cloud small c${i + 1}`} />
      ))}

      <g className="lh-beam pano-beam">
        <path d={`M${LX} ${LY - 4}L1150 250V625L${LX} ${LY + 4}Z`} fill={`url(#pb-${id})`} />
        <path d={p.beamEdges} className="lh-beam-edge" />
      </g>

      <g className="pano-flock">
        <g transform="translate(0 270)">
          {p.flock.split("|").map((d, i) => (
            <path key={i} d={d} className="pano-bird" style={{ animationDelay: `${-i * 0.23}s` }} />
          ))}
        </g>
      </g>
      <g className="lh-gulls">
        {p.gulls.split("|").map((d, i) => (
          <path key={i} d={d} className={`g${i}`} />
        ))}
      </g>

      {/* horizon */}
      <path d={p.waterFill} className="pano-water" />
      <path d={p.horizon} className="ink thin faint-ink" />
      <path d={p.hillsHatch} className="hatch-rock pano-far" />
      <path d={p.hills} className="ink thin pano-far" />
      <path d={p.reflect} className="pano-reflect" />
      <g transform={`translate(0 ${HORIZON + 1})`}>
        <g className="pano-steamer">
          <path d={p.smoke} className="lh-smoke pano-smoke" />
          <path d={p.steamerHatch} className="hatch-rock" />
          <path d={p.steamer} className="ink thin" />
        </g>
      </g>
      <path d={p.island1Fill} className="f-bg" />
      <path d={p.island1Hatch} className="hatch-rock" />
      <path d={p.island1} className="ink thin" />
      <path d={p.island2Fill} className="f-bg" />
      <path d={p.island2} className="ink thin" />
      <path d={p.pines} className="ink thin pano-pines" />
      <path d={p.farTower} className="ink thin" />
      <circle cx="742" cy="585" r="2.6" className="pano-far-lamp" />
      <path d={p.whale} className="ink thin pano-whale" />

      {p.back.split("|").map((d, i) => (
        <path key={i} d={d} className={`ink thin lh-sea pano-back b${i}`} />
      ))}

      <g transform="translate(606 706)">
        <g className="pano-brig">
          <path d={p.brigHullFill} className="f-paper" />
          <path d={p.brigHatch} className="hatch-band" />
          <path d={p.brig} className="ink thin" />
        </g>
      </g>

      <g transform={`translate(${FIG.x} ${FIG.y}) scale(${FIG.k})`}>
        <LighthouseFigure halo={<circle cx={LAMP[0]} cy={LAMP[1]} r="30" fill={`url(#ph-${id})`} className="lh-halo" />} />
      </g>

      <path d={p.glitterR} className="lh-glitter r" />
      <path d={p.glitterL} className="lh-glitter l" />
      {p.front.split("|").map((d, i) => (
        <path key={i} d={d} className={`ink lh-sea pano-front f${i}`} />
      ))}

      <g transform="translate(712 784)">
        <g className="pano-buoy">
          <path d={p.buoyFill} className="f-paper" />
          <path d={p.buoyBands} className="hatch-band" />
          <path d={p.buoy} className="ink thin" />
          <circle cx="0" cy="-50" r="4.5" className="pano-buoy-lamp" />
        </g>
      </g>
      <g transform="translate(520 842)">
        <g className="pano-sailboat">
          <path d={p.sailboat} className="ink thin" />
        </g>
      </g>
      <g transform="translate(420 800)">
        <path d={p.fish} className="ink thin pano-fish" />
      </g>

      {/* below the surface */}
      {p.weeds.split("|").map((d, i) => (
        <path key={i} d={d} className={`pano-weed w${i}`} />
      ))}
      {p.bubbles.split("|").map((d, i) => (
        <path key={i} d={d} className={`pano-bubble u${i}`} />
      ))}
      <g transform="translate(318 790)">
        <g className="pano-jelly">
          <path d={p.jelly} className="ink thin" />
        </g>
      </g>
      <g transform="translate(610 872)">
        <g className="pano-school">
          <path d={p.school} className="ink thin" />
        </g>
      </g>
    </svg>
  );
}
