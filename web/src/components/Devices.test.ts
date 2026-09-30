import { describe, expect, it } from "vitest";
import { deviceName } from "./Devices";

describe("deviceName", () => {
  it.each([
    [
      "Mozilla/5.0 (Linux; Android 14; SM-A546E Build/UP1A.231005.007; wv) AppleWebKit/537.36 (KHTML, like Gecko) Version/4.0 Chrome/129.0.0.0 Mobile Safari/537.36 SentinelaApp/1",
      "Aplicativo Android (SM-A546E)",
    ],
    // Old sessions: user agent cut before "SentinelaApp/1".
    ["Mozilla/5.0 (Linux; Android 13; moto g54 5G Build/T1TD; wv) AppleWebKit/537.36 (KHTML, like Gecko) Version/4.0 Chrome/12", "Aplicativo Android (moto g54 5G)"],
    ["Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Mobile Safari/537.36", "Chrome no Android"],
    ["Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36 Edg/129.0.0.0", "Edge no Windows"],
    ["Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36", "Chrome no Windows"],
    ["Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1", "Safari no iPhone"],
  ])("%s", (ua, name) => {
    expect(deviceName(ua)).toBe(name);
  });
});
