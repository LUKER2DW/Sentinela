import { describe, expect, it } from "vitest";
import { fmtBytes, fmtDuration, mergeRanges, spanDays } from "./time";
import { supportedMseCodecs } from "./player";
import { findSegment } from "./segments";
import type { Segment } from "./api";

describe("mergeRanges", () => {
  it("merges touching segments within tolerance and keeps real gaps", () => {
    const r = mergeRanges([
      [0, 300_000],
      [305_000, 600_000],
      [700_000, 800_000],
    ]);
    expect(r).toEqual([
      [0, 600_000],
      [700_000, 800_000],
    ]);
  });
});

describe("formatting", () => {
  it("formats durations and bytes in pt-BR style", () => {
    expect(fmtDuration(45_000)).toBe("45 s");
    expect(fmtDuration(125_000)).toBe("2 min 05 s");
    expect(fmtDuration(2 * 3600_000 + 5 * 60_000)).toBe("2 h 05 min");
    expect(fmtBytes(21_400_000_000)).toBe("21 GB");
    expect(fmtBytes(1_500_000)).toBe("1,5 MB");
  });
});

describe("findSegment", () => {
  const seg = (id: number, a: string, b: string): Segment => ({ id, camera_id: "cam1", start: a, end: b, bytes: 1, protected: false });
  const segs = [seg(1, "2026-09-28T10:00:00Z", "2026-09-28T10:05:00Z"), seg(2, "2026-09-28T10:10:00Z", "2026-09-28T10:15:00Z")];
  it("finds the segment containing the instant with offset", () => {
    const r = findSegment(segs, Date.parse("2026-09-28T10:02:30Z"));
    expect(r?.seg.id).toBe(1);
    expect(r?.offset).toBe(150);
  });
  it("jumps over gaps to the next segment", () => {
    expect(findSegment(segs, Date.parse("2026-09-28T10:07:00Z"))?.seg.id).toBe(2);
    expect(findSegment(segs, Date.parse("2026-09-28T11:00:00Z"))).toBeNull();
  });
});

describe("supportedMseCodecs", () => {
  it("keeps only codecs the browser accepts, in go2rtc order", () => {
    const ok = (t: string) => t.includes("hvc1") || t.includes("flac");
    expect(supportedMseCodecs(ok)).toBe("hvc1.1.6.L153.B0,flac");
  });
});

describe("spanDays", () => {
  it("spans all cameras from the oldest start to the newest end", () => {
    expect(spanDays([])).toBeNull();
    expect(
      spanDays([
        { oldest: "2026-09-14T00:00:00Z", newest: "2026-09-28T12:00:00Z" },
        { oldest: "2026-09-20T00:00:00Z", newest: "2026-09-29T00:00:00Z" },
      ]),
    ).toBe(15);
  });
});

import { deviceName } from "../components/Devices";

describe("deviceName", () => {
  it("names the app and browsers", () => {
    expect(deviceName("Mozilla/5.0 (Linux; Android 14) Chrome/140 Mobile SentinelaApp/1")).toBe("App no Android");
    expect(deviceName("Mozilla/5.0 (Windows NT 10.0) AppleWebKit Chrome/140 Safari/537 Edg/140")).toBe("Edge no Windows");
    expect(deviceName("Mozilla/5.0 (iPhone; CPU iPhone OS 18_0) Safari/604")).toBe("Safari no iPhone");
  });
});
