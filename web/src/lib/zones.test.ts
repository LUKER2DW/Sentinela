import { describe, expect, it } from "vitest";
import { inside, midpoint, movePoints, nextZoneName, rectPoints, selfIntersects, snapPt, svgPoints, toNorm, zoneHits, zoneProblem, type Pt, type Zone } from "./zones";

describe("zones", () => {
  const rect = { left: 100, top: 50, width: 400, height: 200 };
  it("normalises and clamps pointer positions", () => {
    expect(toNorm(300, 150, rect)).toEqual([0.5, 0.5]);
    expect(toNorm(0, 999, rect)).toEqual([0, 1]);
    expect(toNorm(100 + 400 / 3, 50, rect)).toEqual([0.3333, 0]);
  });
  it("draws in the viewBox", () => {
    expect(svgPoints([[0, 0], [0.5, 1]], 1000, 560)).toBe("0.0,0.0 500.0,560.0");
  });
  it("names new zones", () => {
    const z = (name: string): Zone => ({ name, points: [], objects: ["person"] });
    expect(nextZoneName([])).toBe("Zona 1");
    expect(nextZoneName([z("Zona 1"), z("Zona 3")])).toBe("Zona 2");
  });
  it("reports what the server would refuse", () => {
    const ok: Zone = { name: "Portão", points: [[0, 0], [1, 0], [1, 1]], objects: ["person"] };
    expect(zoneProblem([ok])).toBeNull();
    expect(zoneProblem([{ ...ok, points: ok.points.slice(0, 2) }])).toMatch(/3 pontos/);
    expect(zoneProblem([{ ...ok, objects: [] }])).toMatch(/vigia/);
    expect(zoneProblem([{ ...ok, name: " " }])).toMatch(/nome/);
  });
});

describe("zone geometry", () => {
  const sq: Pt[] = [[0.2, 0.2], [0.6, 0.2], [0.6, 0.6], [0.2, 0.6]];
  it("snaps to the picture's edges and clamps", () => {
    expect(snapPt([0.005, 0.5])).toEqual([0, 0.5]);
    expect(snapPt([1.4, 0.995])).toEqual([1, 1]);
    expect(snapPt([0.3333333, 0.5])).toEqual([0.3333, 0.5]);
  });
  it("tests points in polygons like the detector", () => {
    expect(inside([0.4, 0.4], sq)).toBe(true);
    expect(inside([0.7, 0.4], sq)).toBe(false);
  });
  it("finds bow ties", () => {
    expect(selfIntersects(sq)).toBe(false);
    expect(selfIntersects([[0.2, 0.2], [0.6, 0.6], [0.6, 0.2], [0.2, 0.6]])).toBe(true);
    expect(selfIntersects([[0, 0], [1, 0], [0.5, 1]])).toBe(false);
  });
  it("builds rectangles from any two corners", () => {
    expect(rectPoints([0.6, 0.6], [0.2, 0.2])).toEqual(sq);
  });
  it("moves a zone but keeps it inside the picture", () => {
    expect(movePoints(sq, 0.1, 0)).toEqual([[0.3, 0.2], [0.7, 0.2], [0.7, 0.6], [0.3, 0.6]]);
    expect(movePoints(sq, 0.9, -0.9)[1]).toEqual([1, 0]);
  });
  it("finds edge midpoints, including the closing edge", () => {
    expect(midpoint(sq, 0)).toEqual([0.4, 0.2]);
    expect(midpoint(sq, 3)).toEqual([0.2, 0.4]);
  });
  it("counts objects by their feet", () => {
    const zone = { name: "Portão", points: sq, objects: ["person" as const] };
    const person = { kind: "person" as const, x: 0.35, y: 0.0, w: 0.1, h: 0.5 }; // feet at y=0.5
    expect(zoneHits(person, [zone])).toEqual(["Portão"]);
    expect(zoneHits({ ...person, h: 0.15 }, [zone])).toEqual([]); // feet above the zone
    expect(zoneHits({ ...person, kind: "vehicle" }, [zone])).toEqual([]);
    expect(zoneHits({ ...person, kind: "vehicle" }, [], ["person", "vehicle"])).toEqual(["*"]);
  });
});
