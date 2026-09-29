import { describe, expect, it } from "vitest";
import type { SystemInfo } from "./api";
import { buildNotices, isRead } from "./notices";

const base = {
  now: "2026-09-29T12:00:00Z",
  recorders: [],
  gaps_24h: {},
  disk_health: null,
  backup: null,
  go2rtc_error: "",
} as unknown as SystemInfo;

describe("buildNotices", () => {
  it("is empty when everything is fine", () => {
    expect(buildNotices(base, (id) => id, [])).toEqual([]);
  });
  it("comes back unread when the situation changes", () => {
    const gaps = (count: number, from: string) =>
      ({ ...base, gaps_24h: { cam1: { count, longest_secs: 60, recent: [{ from, to: from }] } } }) as unknown as SystemInfo;
    const [first] = buildNotices(gaps(1, "2026-09-29T09:00:00Z"), () => "Frente", []);
    expect(first.title).toContain("Frente");
    const read = { [first.id]: first.sig };
    expect(isRead(first, read)).toBe(true);
    const [again] = buildNotices(gaps(2, "2026-09-29T11:00:00Z"), () => "Frente", []);
    expect(isRead(again, read)).toBe(false);
  });
  it("warns about the vendor cloud per camera", () => {
    const out = buildNotices(base, () => "Frente", ["cam1"]);
    expect(out.map((n) => n.id)).toEqual(["cloud:cam1"]);
  });
});
