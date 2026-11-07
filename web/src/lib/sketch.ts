// Tiny hand-drawn stroke generator in the spirit of rough.js (ideas only, no
// dependency): every line is drawn twice with a little jitter and bow, closed
// shapes overshoot, and fills are hachure lines. Output is plain SVG path
// data, generated once per drawing from a fixed seed, so it is deterministic.

export type Pt = readonly [number, number];

/** mulberry32: small, fast, seedable PRNG. */
export function rng(seed: number) {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const f = (n: number) => (Math.round(n * 10) / 10).toString();

export class Sketch {
  private rnd: () => number;
  /** Jitter in drawing units; scale it with the viewBox. */
  private rough: number;

  constructor(seed: number, rough = 1) {
    this.rnd = rng(seed);
    this.rough = rough;
  }

  private j(v = 1) {
    return (this.rnd() * 2 - 1) * v * this.rough;
  }

  private pass(a: Pt, b: Pt, move = true): string {
    const [x1, y1] = a;
    const [x2, y2] = b;
    const len = Math.hypot(x2 - x1, y2 - y1) || 1;
    // Short lines wobble less, like a steady hand.
    const k = Math.min(1, len / 60);
    const sx = x1 + this.j(0.6 * k);
    const sy = y1 + this.j(0.6 * k);
    const ex = x2 + this.j(0.6 * k);
    const ey = y2 + this.j(0.6 * k);
    const t = 0.4 + this.rnd() * 0.2;
    const bow = this.j(0.9 * k) + (len * 0.008 * (this.rnd() - 0.5)) * this.rough;
    const nx = -(y2 - y1) / len;
    const ny = (x2 - x1) / len;
    const cx = x1 + (x2 - x1) * t + nx * bow;
    const cy = y1 + (y2 - y1) * t + ny * bow;
    return `${move ? `M${f(sx)} ${f(sy)}` : ""}Q${f(cx)} ${f(cy)} ${f(ex)} ${f(ey)}`;
  }

  /** A straight line drawn twice. */
  line(a: Pt, b: Pt, passes = 2): string {
    let d = "";
    for (let i = 0; i < passes; i++) d += this.pass(a, b);
    return d;
  }

  /** Polyline or polygon, every edge doubled. */
  poly(points: readonly Pt[], close = true, passes = 2): string {
    let d = "";
    const n = points.length;
    const edges = close ? n : n - 1;
    for (let i = 0; i < edges; i++) d += this.line(points[i], points[(i + 1) % n], passes);
    return d;
  }

  /** Smooth curve through points (Catmull-Rom → cubic Bézier), doubled. */
  curve(points: readonly Pt[], passes = 2): string {
    let d = "";
    for (let p = 0; p < passes; p++) {
      const pts = points.map(([x, y]) => [x + this.j(0.5), y + this.j(0.5)] as const);
      d += `M${f(pts[0][0])} ${f(pts[0][1])}`;
      for (let i = 0; i < pts.length - 1; i++) {
        const p0 = pts[Math.max(0, i - 1)];
        const p1 = pts[i];
        const p2 = pts[i + 1];
        const p3 = pts[Math.min(pts.length - 1, i + 2)];
        const c1x = p1[0] + (p2[0] - p0[0]) / 6;
        const c1y = p1[1] + (p2[1] - p0[1]) / 6;
        const c2x = p2[0] - (p3[0] - p1[0]) / 6;
        const c2y = p2[1] - (p3[1] - p1[1]) / 6;
        d += `C${f(c1x)} ${f(c1y)} ${f(c2x)} ${f(c2y)} ${f(p2[0])} ${f(p2[1])}`;
      }
    }
    return d;
  }

  /** Hand-drawn ellipse: wobbly radius, and the stroke overshoots its start. */
  ellipse(cx: number, cy: number, rx: number, ry: number, passes = 2): string {
    let d = "";
    for (let p = 0; p < passes; p++) {
      const start = this.rnd() * Math.PI * 2;
      const steps = 14;
      const over = 0.35 + this.rnd() * 0.3;
      const pts: Pt[] = [];
      for (let i = 0; i <= steps; i++) {
        const a = start + ((Math.PI * 2 + over) * i) / steps;
        const w = 1 + this.j(0.03);
        pts.push([cx + Math.cos(a) * rx * w, cy + Math.sin(a) * ry * w]);
      }
      d += this.curve(pts, 1);
    }
    return d;
  }

  /**
   * Hachure fill: parallel strokes at `angle` degrees, `gap` apart, clipped to
   * the polygon (even-odd). One pass each, like pencil shading.
   */
  hachure(points: readonly Pt[], gap: number, angle = -41): string {
    const a = (angle * Math.PI) / 180;
    const cos = Math.cos(a);
    const sin = Math.sin(a);
    // Rotate the polygon so hachure lines become horizontal scanlines.
    const rot = points.map(([x, y]) => [x * cos + y * sin, -x * sin + y * cos] as const);
    const ys = rot.map((p) => p[1]);
    const minY = Math.min(...ys);
    const maxY = Math.max(...ys);
    let d = "";
    for (let y = minY + gap / 2; y < maxY; y += gap) {
      const xs: number[] = [];
      for (let i = 0; i < rot.length; i++) {
        const [x1, y1] = rot[i];
        const [x2, y2] = rot[(i + 1) % rot.length];
        if ((y1 <= y && y < y2) || (y2 <= y && y < y1)) xs.push(x1 + ((y - y1) * (x2 - x1)) / (y2 - y1));
      }
      xs.sort((p, q) => p - q);
      for (let i = 0; i + 1 < xs.length; i += 2) {
        const inset = Math.min(gap * 0.3, (xs[i + 1] - xs[i]) / 4);
        const back = (x: number): Pt => [x * cos - y * sin, x * sin + y * cos];
        d += this.line(back(xs[i] + inset), back(xs[i + 1] - inset), 1);
      }
    }
    return d;
  }
}
