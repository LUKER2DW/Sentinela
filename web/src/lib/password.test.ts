import { describe, expect, it } from "vitest";
import { passwordStrength, policyError } from "./password";

describe("passwordStrength", () => {
  it.each([
    ["", 0],
    ["abc", 1],
    ["admin1234", 1],
    ["aaaaaaaaaaaa", 1],
    ["abcdefghijkl", 1],
    ["maré-cheia-7", 3],
    ["Farol na Pedra 42!", 4],
  ])("%s → %i", (pw, score) => {
    expect(passwordStrength(pw).score).toBe(score);
  });

  it("names the minimum when too short", () => {
    expect(passwordStrength("abc").label).toContain("10");
  });
});

describe("policyError mirrors the server", () => {
  it.each([
    ["casa2026!x", true],
    ["long enough pw", true],
    ["umapalavrasolongalonga", true],
    ["admin1234", false],
    ["abcdefghijkl", false],
    ["1234567890", false],
    ["aaaaaaaaaaaaaaaaaa", false],
  ])("%s ok=%s", (pw, ok) => {
    expect(policyError(pw) === "").toBe(ok);
  });
});
