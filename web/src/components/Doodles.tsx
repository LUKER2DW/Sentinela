import { Sketch, type Pt } from "../lib/sketch";
import { LighthouseMark } from "./Lighthouse";

// Empty-state and error doodles, drawn with the same hand (lib/sketch) as the
// lighthouse: doubled wobbly strokes, pencil hachure. Paths are generated once.
// Motion lives in CSS (.dd-*) and is disabled under prefers-reduced-motion.

const stroke = {
  fill: "none",
  stroke: "currentColor",
  strokeWidth: 2,
  strokeLinecap: "round" as const,
  strokeLinejoin: "round" as const,
};

const thin = { ...stroke, strokeWidth: 1.2 };

function waveLine(y: number, amp: number, from: number, to: number, step: number): Pt[] {
  const pts: Pt[] = [];
  for (let x = from, i = 0; x <= to; x += step, i++) pts.push([x, y + (i % 2 ? amp : -amp)]);
  return pts;
}

let boat: Record<string, string> | null = null;
function boatPaths() {
  const k = new Sketch(21, 0.7);
  const hull: Pt[] = [
    [24, 50],
    [97, 49.5],
    [88, 61],
    [32, 61.5],
  ];
  return {
    hull: k.poly(hull),
    hullHatch: new Sketch(4, 0.5).hachure(hull, 3.6, -35),
    mast: k.line([60, 50.5], [60.5, 12]) + k.poly(
      [
        [60.5, 13],
        [68, 15.5],
        [60.5, 18],
      ],
      false,
      1,
    ),
    sails:
      k.curve([
        [61.5, 18],
        [74, 30],
        [84.5, 46],
        [61.5, 47.5],
      ]) +
      k.curve([
        [59, 22],
        [48, 34],
        [42, 46.5],
        [58.8, 47.8],
      ]),
    sea1: k.curve(waveLine(68, 2.6, 4, 116, 9), 1),
    sea2: k.curve(waveLine(76, 2, 18, 100, 9), 1),
  };
}

export function Boat({ className = "" }: { className?: string }) {
  boat ??= boatPaths();
  return (
    <svg className={`dd-boat ${className}`} viewBox="0 0 120 80" aria-hidden="true">
      <g className="dd-bob">
        <path d={boat.hullHatch} {...thin} opacity={0.6} />
        <path d={boat.hull} {...stroke} strokeWidth={1.5} />
        <path d={boat.mast} {...stroke} strokeWidth={1.5} />
        <path d={boat.sails} {...stroke} strokeWidth={1.5} />
      </g>
      <path className="dd-sway" d={boat.sea1} {...stroke} strokeWidth={1.6} />
      <path className="dd-sway dd-sway-slow" d={boat.sea2} {...stroke} strokeWidth={1.4} opacity={0.55} />
    </svg>
  );
}

let fish: Record<string, string> | null = null;
function fishPaths() {
  const k = new Sketch(33, 0.6);
  const body: Pt[] = [
    [28, 35],
    [38, 25],
    [52, 22.5],
    [66, 27],
    [76, 33.5],
    [66, 40.5],
    [52, 45],
    [38, 43],
    [28, 35],
  ];
  return {
    body: k.curve(body),
    scales: new Sketch(8, 0.4).hachure(
      [
        [54, 26],
        [66, 28],
        [74, 33.5],
        [66, 39.5],
        [54, 42],
      ],
      3.2,
      60,
    ),
    tail: k.poly([
      [76, 33.5],
      [92, 22],
      [89, 34],
      [92.5, 46],
    ]),
    gill: k.curve(
      [
        [49, 27],
        [52.5, 34],
        [49.5, 41],
      ],
      1,
    ),
    fin: k.curve(
      [
        [56, 23.5],
        [60, 17],
        [66, 26.5],
      ],
      1,
    ),
    bubbles: k.ellipse(20, 26, 2.6, 2.6, 1) + k.ellipse(13, 16, 1.8, 1.8, 1) + k.ellipse(17, 8, 1.2, 1.2, 1),
  };
}

export function Fish({ className = "" }: { className?: string }) {
  fish ??= fishPaths();
  return (
    <svg className={`dd-fish ${className}`} viewBox="0 0 120 70" aria-hidden="true">
      <g className="dd-swim">
        <path d={fish.scales} {...thin} opacity={0.55} />
        <path d={fish.body} {...stroke} strokeWidth={1.5} />
        <path d={fish.tail} {...stroke} strokeWidth={1.5} />
        <path d={fish.gill} {...stroke} strokeWidth={1.3} opacity={0.7} />
        <path d={fish.fin} {...stroke} strokeWidth={1.3} />
        <circle cx="38" cy="31" r="1.8" fill="currentColor" />
      </g>
      <path d={fish.bubbles} {...stroke} strokeWidth={1.3} className="dd-bubbles" />
    </svg>
  );
}

export function Sun({ className = "", style }: { className?: string; style?: React.CSSProperties }) {
  return (
    <svg className={className} style={style} viewBox="0 0 24 24" aria-hidden="true">
      <g {...stroke} strokeWidth={1.7}>
        <circle cx="12" cy="12" r="4.3" />
        <path d="M12 2.5v2.4M12 19.1v2.4M2.5 12h2.4M19.1 12h2.4M5.3 5.3l1.7 1.7M17 17l1.7 1.7M5.3 18.7 7 17M17 7l1.7-1.7" />
      </g>
    </svg>
  );
}

export function Moon({ className = "", style }: { className?: string; style?: React.CSSProperties }) {
  return (
    <svg className={className} style={style} viewBox="0 0 24 24" aria-hidden="true">
      <path {...stroke} strokeWidth={1.7} d="M19.5 14.6A7.8 7.8 0 0 1 9.4 4.5a7.8 7.8 0 1 0 10.1 10.1z" />
    </svg>
  );
}

function wavePath(y: number, amp: number, period: number, width = 480, height = 120) {
  let d = `M0 ${y}`;
  for (let x = 0; x < width; x += period / 2) {
    const dir = (x / (period / 2)) % 2 === 0 ? -1 : 1;
    d += ` q${period / 4} ${dir * amp} ${period / 2} 0`;
  }
  return `${d} L${width} ${height} L0 ${height} Z`;
}

/** Layered pastel sea for the sidebar foot: three drifting wave bands, a boat, a fish. */
export function OceanScene({ className = "" }: { className?: string }) {
  return (
    <svg className={`ocean ${className}`} viewBox="0 0 240 120" preserveAspectRatio="xMidYMax slice" aria-hidden="true">
      <LighthouseMark at={[92, 8, 50]} className="o-lighthouse" />
      <path className="o-wave o-back" d={wavePath(46, 5, 60)} />
      <g transform="translate(34 0)">
        <g className="o-boat">
          <path d="M150 44 Q162 49 175 44 L171 50 Q162 52.5 154 50 Z" />
          <path d="M162.4 44.6 V28.5 L172 42.4 Z M161.6 31 L154.5 42.6 H161.6 Z" />
        </g>
      </g>
      <path className="o-wave o-mid" d={wavePath(62, 6, 80)} />
      <g className="o-fish">
        <path d="M60 86 Q66 81 73 85.5 Q66 90 60 86 Z M73 85.5 L77 82.5 L76.6 88.6 Z" />
      </g>
      <path className="o-wave o-front" d={wavePath(80, 7, 120)} />
    </svg>
  );
}

let whale: Record<string, string> | null = null;
function whalePaths() {
  const k = new Sketch(55, 0.8);
  return {
    body: k.curve([
      [22, 58],
      [30, 40],
      [58, 32],
      [92, 34],
      [112, 52],
      [124, 60],
      [134, 54],
      [140, 44],
    ]) +
      k.curve([
        [140, 44],
        [141, 58],
        [134, 64],
        [140, 71],
        [141, 80],
        [130, 70],
      ]) +
      k.curve([
        [130, 70],
        [100, 76],
        [60, 78],
        [36, 72],
        [22, 58],
      ]),
    belly: new Sketch(12, 0.5).hachure(
      [
        [34, 66],
        [60, 71],
        [96, 70],
        [118, 66],
        [100, 75],
        [60, 77],
        [38, 72],
      ],
      3.4,
      -30,
    ),
    flipper: k.curve(
      [
        [62, 62],
        [70, 72],
        [80, 70],
      ],
      1,
    ),
    spout: k.curve(
      [
        [60, 30],
        [57, 21],
        [50, 16],
      ],
      1,
    ) +
      k.curve(
        [
          [60, 30],
          [62, 19],
          [70, 14],
        ],
        1,
      ) +
      k.line([60, 30], [60, 15], 1),
    sea: k.curve(waveLine(88, 2.6, 6, 154, 10), 1),
  };
}

/** A small whale, for the Nuvem page. */
export function Whale({ className = "" }: { className?: string }) {
  whale ??= whalePaths();
  return (
    <svg className={`dd-whale ${className}`} viewBox="0 0 160 100" aria-hidden="true">
      <g className="dd-bob">
        <path d={whale.belly} {...thin} opacity={0.55} />
        <path d={whale.body} {...stroke} strokeWidth={1.5} />
        <path d={whale.flipper} {...stroke} strokeWidth={1.4} />
        <circle cx="44" cy="50" r="2" fill="currentColor" />
        <path d={whale.spout} {...stroke} strokeWidth={1.4} className="dd-spout" />
      </g>
      <path className="dd-sway" d={whale.sea} {...stroke} strokeWidth={1.6} />
    </svg>
  );
}
