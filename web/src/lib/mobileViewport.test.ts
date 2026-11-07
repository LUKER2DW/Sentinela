import { afterEach, describe, expect, it, vi } from "vitest";
import { isMobileViewport, mobilePage } from "./mobileViewport";

afterEach(() => vi.unstubAllGlobals());

function device(width: number, height: number, coarse: boolean, userAgent = "Browser", native = false) {
  vi.stubGlobal("navigator", { userAgent });
  vi.stubGlobal("window", {
    Capacitor: { isNativePlatform: () => native },
    matchMedia: () => ({ matches: width <= 760 || (coarse && height <= 760) }),
  });
}

describe("mobile routing across rotation", () => {
  it.each([
    [390, 844, true, true],
    [844, 390, true, true],
    [1440, 600, false, false],
    [1440, 900, true, false],
  ])("keeps the intended device layout at %i × %i", (width, height, coarse, mobile) => {
    device(width, height, coarse);
    expect(isMobileViewport()).toBe(mobile);
    expect(mobilePage("services", isMobileViewport())).toBe(mobile ? "system" : "services");
    expect(mobilePage("events", isMobileViewport())).toBe("events");
  });

  it("recognizes the native app independently of viewport size", () => {
    device(1440, 900, false, "SentinelaApp/1.14");
    expect(isMobileViewport()).toBe(true);
    device(1440, 900, false, "Browser", true);
    expect(isMobileViewport()).toBe(true);
  });
});
