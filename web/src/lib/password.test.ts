import { describe, expect, it } from "vitest";
import { passwordStrength } from "./password";

describe("passwordStrength", () => {
  it.each([
    ["", 0],
    ["abc", 1],
    ["admin123", 1],
    ["aaaaaaaaaaaa", 1],
    ["maré-cheia-7", 3],
    ["Farol na Pedra 42!", 4],
  ])("%s → %i", (pw, score) => {
    expect(passwordStrength(pw).score).toBe(score);
  });

  it("names the minimum when too short", () => {
    expect(passwordStrength("abc").label).toContain("8");
  });
});
