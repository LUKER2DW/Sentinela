import { describe, expect, it } from "vitest";
import { timelapseRange } from "./Timelapse";

const day = new Date(2026, 8, 29);
const at = (h: number) => day.getTime() + h * 3600_000;

describe("timelapseRange", () => {
  it("covers the whole period on a past day", () => {
    const r = timelapseRange(day, 6, 18, at(48));
    expect(r?.from.getTime()).toBe(at(6));
    expect(r?.to.getTime()).toBe(at(18));
  });
  it("stops at now on the current day", () => {
    expect(timelapseRange(day, 0, 24, at(10))?.to.getTime()).toBe(at(10));
  });
  it("is empty before the period starts", () => {
    expect(timelapseRange(day, 18, 24, at(10))).toBeNull();
  });
});
