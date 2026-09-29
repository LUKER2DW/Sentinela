import { describe, expect, it } from "vitest";
import { NO_ZOOM, clampZoom, onMeteredNetwork } from "./gestures";

describe("clampZoom", () => {
  it("keeps the picture inside the frame", () => {
    expect(clampZoom({ scale: 0.5, x: 10, y: 10 }, 400, 200)).toEqual(NO_ZOOM);
    expect(clampZoom({ scale: 2, x: 999, y: -999 }, 400, 200)).toEqual({ scale: 2, x: 200, y: -100 });
    expect(clampZoom({ scale: 9, x: 0, y: 0 }, 400, 200).scale).toBe(4);
  });
});

describe("onMeteredNetwork", () => {
  it("detects mobile data and data saver", () => {
    expect(onMeteredNetwork({ type: "cellular" })).toBe(true);
    expect(onMeteredNetwork({ saveData: true })).toBe(true);
    expect(onMeteredNetwork({ effectiveType: "3g" })).toBe(true);
    expect(onMeteredNetwork({ type: "wifi", effectiveType: "4g" })).toBe(false);
    expect(onMeteredNetwork(undefined)).toBe(false);
  });
});
