import { describe, expect, it } from "vitest";
import { nextZoneName, svgPoints, toNorm, zoneProblem, type Zone } from "./zones";

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
