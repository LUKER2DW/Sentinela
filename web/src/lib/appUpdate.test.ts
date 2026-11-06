import { describe, expect, it } from "vitest";
import { nativeSize } from "./appUpdate";

describe("nativeSize", () => {
  it("forces a Long for plugins that ignored int sizes (≤ 1.15)", () => {
    expect(nativeSize(5157898, 15)).toBe(5157898 + 2 ** 32);
    expect(nativeSize(5157898, 16)).toBe(5157898 + 2 ** 32);
  });
  it("sends the real size to fixed plugins and unknown versions", () => {
    expect(nativeSize(5157898, 17)).toBe(5157898);
    expect(nativeSize(5157898, 0)).toBe(5157898);
  });
});
