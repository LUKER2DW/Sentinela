import { describe, expect, it } from "vitest";
import { getZone, isPublicZone, setZone, zoneLabel } from "./access";

describe("access zones", () => {
  it("keeps only known zones and flags the public ones", () => {
    setZone("funnel");
    expect(getZone()).toBe("funnel");
    expect(isPublicZone()).toBe(true);
    setZone("nonsense");
    expect(getZone()).toBe("funnel");
    setZone("lan");
    expect(isPublicZone()).toBe(false);
    expect(zoneLabel("public")).toContain("público");
  });
});
