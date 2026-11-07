import { Sketch, type Pt } from "../lib/sketch";

// Small hand-drawn keepsakes (lib/sketch) tucked into the background: a
// compass rose and an anchor on the sign-in panel, and a few faint finds
// (starfish, shell, bottle, gulls, a wave, a jellyfish, a crab, kelp and a
// ship's wheel) scattered behind the dashboard.
// Purely decorative: aria-hidden, no pointer events, drawn once.

type Paths = Record<string, string>;
let d: Paths | null = null;

function star(cx: number, cy: number, r1: number, r2: number, n: number, rot = -Math.PI / 2): Pt[] {
  return Array.from({ length: n * 2 }, (_, i) => {
    const r = i % 2 ? r2 : r1;
    const a = rot + (i * Math.PI) / n;
    return [cx + Math.cos(a) * r, cy + Math.sin(a) * r] as Pt;
  });
}

function build(): Paths {
  const s = new Sketch(77, 0.5);
  const h = new Sketch(78, 0.4);
  const rose = star(50, 50, 28, 5.5, 4);
  const rose2 = star(50, 50, 17, 4, 4, -Math.PI / 4);
  return {
    compass:
      s.ellipse(50, 50, 44, 44) +
      s.ellipse(50, 50, 30, 30, 1) +
      s.poly(rose) +
      s.poly(rose2, true, 1),
    compassHatch: h.hachure(
      [
        [50, 22],
        [54, 46],
        [50, 50],
        [46, 46],
      ],
      2.2,
      0,
    ),
    anchor:
      s.ellipse(50, 14, 6, 6) +
      s.line([50, 20], [50, 84]) +
      s.line([36, 30], [64, 30]) +
      s.curve([
        [18, 58],
        [24, 76],
        [50, 86],
        [76, 76],
        [82, 58],
      ]) +
      s.poly([
        [14, 62],
        [18, 56],
        [24, 62],
      ], false, 1) +
      s.poly([
        [76, 62],
        [82, 56],
        [86, 62],
      ], false, 1),
    starfish: s.curve([...star(50, 50, 40, 16, 5), star(50, 50, 40, 16, 5)[0]]),
    starfishDots: [0, 1, 2, 3, 4]
      .map((i) => {
        const a = -Math.PI / 2 + (i * 2 * Math.PI) / 5;
        return s.ellipse(50 + Math.cos(a) * 20, 50 + Math.sin(a) * 20, 1.2, 1.2, 1);
      })
      .join(""),
    shell:
      s.curve([
        [14, 70],
        [20, 34],
        [50, 16],
        [80, 34],
        [86, 70],
        [50, 78],
        [14, 70],
      ]) +
      [22, 36, 50, 64, 78].map((x) => s.line([50, 86], [x, 26 + Math.abs(x - 50) * 0.5], 1)).join("") +
      s.poly([
        [40, 78],
        [44, 90],
        [56, 90],
        [60, 78],
      ], false, 1),
    bottle:
      s.poly([
        [30, 40],
        [30, 88],
        [70, 88],
        [70, 40],
        [58, 30],
        [58, 12],
        [42, 12],
        [42, 30],
      ]) +
      s.line([40, 12], [60, 12]) +
      s.curve([
        [38, 52],
        [50, 48],
        [62, 54],
        [60, 74],
        [40, 72],
        [38, 52],
      ], 1) +
      s.line([44, 58], [56, 58], 1) +
      s.line([44, 64], [54, 64], 1),
    gulls:
      s.curve([
        [10, 40],
        [22, 28],
        [34, 38],
        [46, 27],
        [58, 39],
      ], 1) +
      s.curve([
        [50, 62],
        [58, 55],
        [66, 61],
        [74, 54],
        [82, 61],
      ], 1),
    jelly:
      s.curve([
        [22, 44],
        [26, 22],
        [50, 12],
        [74, 22],
        [78, 44],
      ]) +
      s.curve([
        [22, 44],
        [36, 49],
        [50, 44],
        [64, 49],
        [78, 44],
      ], 1) +
      [32, 44, 56, 68]
        .map((x, i) =>
          s.curve([
            [x, 48],
            [x + (i % 2 ? 5 : -5), 62],
            [x, 76],
            [x + (i % 2 ? -5 : 5), 90],
          ], 1),
        )
        .join(""),
    crab:
      s.ellipse(50, 58, 22, 14) +
      s.ellipse(43, 40, 2.4, 2.4, 1) +
      s.ellipse(57, 40, 2.4, 2.4, 1) +
      s.line([45, 46], [43, 42], 1) +
      s.line([55, 46], [57, 42], 1) +
      [-1, 1]
        .map(
          (k) =>
            s.curve([
              [50 + 20 * k, 50],
              [50 + 30 * k, 36],
              [50 + 26 * k, 26],
            ], 1) +
            s.poly([
              [50 + 22 * k, 28],
              [50 + 30 * k, 20],
              [50 + 34 * k, 28],
            ], false, 1) +
            [62, 68, 74].map((y) => s.line([50 + 20 * k, y - 4], [50 + 32 * k, y + 4], 1)).join(""),
        )
        .join(""),
    kelp: [30, 50, 68]
      .map((x, i) =>
        s.curve(
          Array.from({ length: 6 }, (_, j) => [x + Math.sin(j * 1.2 + i) * 6, 96 - j * (14 + i * 2)] as Pt),
          1,
        ),
      )
      .join(""),
    helm:
      s.ellipse(50, 50, 26, 26) +
      s.ellipse(50, 50, 8, 8, 1) +
      Array.from({ length: 8 }, (_, i) => {
        const a = (i * Math.PI) / 4;
        return s.line([50 + Math.cos(a) * 8, 50 + Math.sin(a) * 8], [50 + Math.cos(a) * 40, 50 + Math.sin(a) * 40], 1);
      }).join(""),
    wave: s.curve(
      Array.from({ length: 9 }, (_, i) => [6 + i * 11, 50 + (i % 2 ? 6 : -6)] as Pt),
      1,
    ),
  };
}

function Doodle({ name, className = "", hatch }: { name: string; className?: string; hatch?: string }) {
  d ??= build();
  return (
    <svg className={`decor ${className}`} viewBox="0 0 100 100" aria-hidden="true">
      {hatch && <path d={d[hatch]} className="decor-hatch" />}
      <path d={d[name]} />
      {name === "starfish" && <path d={d.starfishDots} />}
      {name === "compass" && (
        <g className="decor-letters">
          <text x="50" y="16.5">N</text>
          <text x="84.5" y="53.5">E</text>
          <text x="50" y="90.5">S</text>
          <text x="15.5" y="53.5">W</text>
        </g>
      )}
    </svg>
  );
}

export const Compass = ({ className = "" }) => <Doodle name="compass" hatch="compassHatch" className={className} />;
export const Anchor = ({ className = "" }) => <Doodle name="anchor" className={className} />;

/** Faint finds scattered behind the dashboard pages; mostly visible in the margins. */
export function BgDoodles() {
  return (
    <div className="bg-doodles" aria-hidden="true">
      <Doodle name="gulls" className="bd bd-gulls" />
      <Doodle name="starfish" className="bd bd-star" />
      <Doodle name="shell" className="bd bd-shell" />
      <Doodle name="bottle" className="bd bd-bottle" />
      <Doodle name="wave" className="bd bd-wave" />
      <Doodle name="anchor" className="bd bd-anchor" />
      <Doodle name="compass" className="bd bd-compass" />
      <Doodle name="jelly" className="bd bd-jelly" />
      <Doodle name="crab" className="bd bd-crab" />
      <Doodle name="kelp" className="bd bd-kelp" />
      <Doodle name="helm" className="bd bd-helm" />
    </div>
  );
}
