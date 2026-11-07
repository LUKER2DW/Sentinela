import { describe, expect, it } from "vitest";
import { Sketch } from "./sketch";

describe("Sketch", () => {
  it("is deterministic for a seed", () => {
    expect(new Sketch(3).line([0, 0], [100, 0])).toBe(new Sketch(3).line([0, 0], [100, 0]));
  });

  it("draws each line twice by default", () => {
    expect(new Sketch(1).line([0, 0], [50, 50]).match(/M/g)).toHaveLength(2);
  });

  it("keeps hachure strokes inside the shape", () => {
    const d = new Sketch(2, 0).hachure(
      [
        [0, 0],
        [100, 0],
        [100, 50],
        [0, 50],
      ],
      5,
    );
    const nums = d.match(/-?\d+(\.\d+)?/g)!.map(Number);
    expect(nums.length).toBeGreaterThan(20);
    for (let i = 0; i < nums.length; i += 2) {
      expect(nums[i]).toBeGreaterThanOrEqual(-0.5);
      expect(nums[i]).toBeLessThanOrEqual(100.5);
    }
  });
});
