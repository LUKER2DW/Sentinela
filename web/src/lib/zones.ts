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
  }
  return null;
}
