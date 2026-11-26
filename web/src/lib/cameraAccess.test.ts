import { describe, expect, it } from "vitest";
import { configuredGrant, grantToSave, nextCameraId, sameSelection } from "./cameraAccess";

describe("nextCameraId", () => {
  it("never hands out a removed camera's id", () => {
    const draft = ["cam1", "cam2"];
    const retired = ["cam3"];
    expect(nextCameraId(draft.length + 1, (id) => draft.includes(id) || retired.includes(id))).toBe("cam4");
    expect(nextCameraId(2, (id) => id === "cam1")).toBe("cam2");
  });
});

describe("camera access dialog", () => {
  const configured = ["cam1", "cam2"];
  it("drops removed cameras from what it shows ticked", () => {
    expect(configuredGrant(["cam3", "cam1"], configured)).toEqual(["cam1"]);
  });
  it("saves every camera only when every configured camera is ticked (sets, not counts)", () => {
    // A removed cam3 still on the account never makes a partial pick look complete.
    expect(grantToSave(["cam3", "cam1"], configured)).toEqual(["cam1"]);
    expect(grantToSave(["cam3", "cam2"], configured)).toEqual(["cam2"]);
    expect(grantToSave(["cam1", "cam3", "cam2"].filter((id) => id !== "cam2"), configured)).toEqual(["cam1"]);
    expect(grantToSave(["cam2", "cam1"], configured)).toEqual([]);
    expect(grantToSave([], configured)).toEqual([]);
  });
  it("compares selections on configured cameras only", () => {
    expect(sameSelection(["cam3", "cam1"], ["cam1"], configured)).toBe(true);
    expect(sameSelection(["cam3"], [], configured)).toBe(true);
    expect(sameSelection(["cam1"], ["cam2"], configured)).toBe(false);
  });
});
