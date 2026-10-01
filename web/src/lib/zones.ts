// Detection zones (ADR-0016): polygons in normalised picture coordinates.

export type ObjectKind = "person" | "vehicle" | "animal";

export interface Zone {
  name: string;
  points: [number, number][];
  objects: ObjectKind[];
}

export interface DetectionSettings {
  enabled: boolean;
  min_score: number;
  zones: Zone[];
  /** Kinds watched over the whole picture when there are no zones. */
  objects: ObjectKind[];
  /** Objects that stay in place (a parked car) stop counting. */
  ignore_parked: boolean;
  /** Objects shorter than this share of the picture height are ignored. */
  min_size: number;
}

export const KIND_LABEL: Record<ObjectKind, string> = { person: "Pessoas", vehicle: "Veículos", animal: "Animais" };

/** Sensitivity presets shown to people; the value is the model's minimum score. */
export const SENSITIVITY = [
  { label: "Alta", value: 0.35 },
  { label: "Normal", value: 0.45 },
  { label: "Baixa", value: 0.6 },
] as const;

export const ZONE_COLORS = ["#2f8f9d", "#e07a5f", "#81b29a", "#c9a227", "#7d6bb3", "#d9667b"];

export const MAX_ZONES = 10;
export const MAX_POINTS = 32;

/** A pointer position inside a rect, as a clamped normalised point (4 decimals). */
export function toNorm(clientX: number, clientY: number, rect: { left: number; top: number; width: number; height: number }): [number, number] {
  const c = (v: number) => Math.round(Math.min(1, Math.max(0, v)) * 10000) / 10000;
  return [c((clientX - rect.left) / rect.width), c((clientY - rect.top) / rect.height)];
}

/** SVG "points" attribute for a polygon drawn in a w×h viewBox. */
export function svgPoints(points: [number, number][], w: number, h: number): string {
  return points.map(([x, y]) => `${(x * w).toFixed(1)},${(y * h).toFixed(1)}`).join(" ");
}

/** Next free default name: "Zona 1", "Zona 2"… */
export function nextZoneName(zones: Zone[]): string {
  for (let i = 1; ; i++) {
    const n = `Zona ${i}`;
    if (!zones.some((z) => z.name === n)) return n;
  }
}

/** Problems that the server would reject, in Portuguese, or null. */
export function zoneProblem(zones: Zone[]): string | null {
  if (zones.length > MAX_ZONES) return `No máximo ${MAX_ZONES} zonas.`;
  for (const z of zones) {
    if (!z.name.trim()) return "Toda zona precisa de um nome.";
    if (z.points.length < 3) return `A zona "${z.name}" precisa de pelo menos 3 pontos.`;
    if (z.objects.length === 0) return `Escolha o que a zona "${z.name}" vigia.`;
    if (zones.some((o) => o !== z && o.name.trim() === z.name.trim())) return `Há duas zonas chamadas "${z.name}".`;
  }
  return null;
}

// ---------- geometry for the zone editor and the overlay ----------

export type Pt = [number, number];

const r4 = (v: number) => Math.round(v * 10000) / 10000;
const clamp01 = (v: number) => Math.min(1, Math.max(0, v));

/** Points this close to an edge of the picture land on it. */
export const SNAP = 0.012;

/** Clamp to the picture and stick to its edges when close. */
export function snapPt([x, y]: Pt, eps = SNAP): Pt {
  const s = (v: number) => (v < eps ? 0 : v > 1 - eps ? 1 : clamp01(v));
  return [r4(s(x)), r4(s(y))];
}

/** Ray-casting point in polygon (same rule as the detector). */
export function inside([x, y]: Pt, poly: Pt[]): boolean {
  let ok = false;
  for (let i = 0, j = poly.length - 1; i < poly.length; j = i++) {
    const [xi, yi] = poly[i];
    const [xj, yj] = poly[j];
    if (yi > y !== yj > y && x < ((xj - xi) * (y - yi)) / (yj - yi) + xi) ok = !ok;
  }
  return ok;
}

function cross(a: Pt, b: Pt, c: Pt): number {
  return (b[0] - a[0]) * (c[1] - a[1]) - (b[1] - a[1]) * (c[0] - a[0]);
}

function segmentsCross(a: Pt, b: Pt, c: Pt, d: Pt): boolean {
  return cross(a, b, c) * cross(a, b, d) < 0 && cross(c, d, a) * cross(c, d, b) < 0;
}

/** True when two edges that do not share a corner cross (a "bow tie"). */
export function selfIntersects(poly: Pt[]): boolean {
  const n = poly.length;
  for (let i = 0; i < n; i++) {
    for (let j = i + 1; j < n; j++) {
      if (j === i + 1 || (i === 0 && j === n - 1)) continue;
      if (segmentsCross(poly[i], poly[(i + 1) % n], poly[j], poly[(j + 1) % n])) return true;
    }
  }
  return false;
}

/** The four corners of the rectangle spanned by a and b. */
export function rectPoints(a: Pt, b: Pt): Pt[] {
  const [x0, x1] = [Math.min(a[0], b[0]), Math.max(a[0], b[0])];
  const [y0, y1] = [Math.min(a[1], b[1]), Math.max(a[1], b[1])];
  return [snapPt([x0, y0]), snapPt([x1, y0]), snapPt([x1, y1]), snapPt([x0, y1])];
}

/** The polygon moved by (dx, dy), stopped at the picture's edges. */
export function movePoints(points: Pt[], dx: number, dy: number): Pt[] {
  const xs = points.map((p) => p[0]);
  const ys = points.map((p) => p[1]);
  const mx = Math.min(Math.max(dx, -Math.min(...xs)), 1 - Math.max(...xs));
  const my = Math.min(Math.max(dy, -Math.min(...ys)), 1 - Math.max(...ys));
  return points.map(([x, y]) => [r4(x + mx), r4(y + my)]);
}

/** Middle of edge i (from point i to the next). */
export function midpoint(points: Pt[], i: number): Pt {
  const a = points[i];
  const b = points[(i + 1) % points.length];
  return [r4((a[0] + b[0]) / 2), r4((a[1] + b[1]) / 2)];
}

/** What the detector reports for one object in the live overlay. */
export interface SeenObject {
  kind: ObjectKind;
  x: number;
  y: number;
  w: number;
  h: number;
  score: number;
  zones: string[];
  parked: boolean;
  counted: boolean;
}

/** Names of the zones that count an object: its base (feet, wheels) is
 * inside a zone watching its kind; no zones = the kinds in `watch`. */
export function zoneHits(o: Pick<SeenObject, "kind" | "x" | "y" | "w" | "h">, zones: Zone[], watch: ObjectKind[] = ["person"]): string[] {
  if (!zones.length) return watch.includes(o.kind) ? ["*"] : [];
  const base: Pt = [o.x + o.w / 2, Math.min(o.y + o.h, 0.999)];
  return zones.filter((z) => z.objects.includes(o.kind) && inside(base, z.points)).map((z) => z.name);
}

export const KIND_ONE: Record<ObjectKind, string> = { person: "Pessoa", vehicle: "Veículo", animal: "Animal" };
export const KIND_ICON: Record<ObjectKind, string> = { person: "user", vehicle: "car", animal: "paw" };

/** Minimum object size presets (share of the picture height). */
export const MIN_SIZE = [
  { label: "Qualquer", value: 0 },
  { label: "Sem os pequenos", value: 0.04 },
  { label: "Só perto", value: 0.1 },
] as const;
