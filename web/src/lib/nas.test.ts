import { describe, expect, it } from "vitest";
import { CHUNK_MAX, CHUNK_MIN, crumbs, foldersOf, label, nextChunk, parentPath, renameTop, restorePath, type Picked } from "./nas";

describe("nas paths", () => {
  it("builds breadcrumbs with friendly names and hides the bin's user level", () => {
    expect(crumbs("", "ana")).toEqual([{ name: "Nuvem", path: "" }]);
    expect(crumbs("ana/Fotos", "ana").map((c) => c.name)).toEqual(["Nuvem", "Meus arquivos", "Fotos"]);
    expect(crumbs(".lixeira/ana/ana/Fotos", "ana").map((c) => c.path)).toEqual(["", ".lixeira", ".lixeira/ana/ana", ".lixeira/ana/ana/Fotos"]);
  });
  it("knows where a binned item came from", () => {
    expect(restorePath(".lixeira/ana/ana/Fotos/a.jpg", "ana")).toBe("ana/Fotos/a.jpg");
    expect(restorePath(".lixeira/bia/x", "ana")).toBeNull();
    expect(restorePath("ana/x", "ana")).toBeNull();
  });
  it("labels and parents", () => {
    expect(label(".lixeira", "ana")).toBe("Lixeira");
    expect(parentPath("a/b/c")).toBe("a/b");
    expect(parentPath("a")).toBe("");
  });
});

describe("nas kinds and sorting", async () => {
  const { inside, kindOf, sortEntries } = await import("./nas");
  it("classifies files for preview", () => {
    expect(kindOf({ name: "a.PDF", dir: false })).toBe("pdf");
    expect(kindOf({ name: "notas.md", dir: false })).toBe("text");
    expect(kindOf({ name: "x.zip", dir: false })).toBe("archive");
    expect(kindOf({ name: "Fotos", dir: true })).toBe("folder");
  });
  it("keeps folders first and sorts names naturally", () => {
    const e = (name: string, dir = false, size = 0) => ({ name, dir, size, modified: "2026-09-29T00:00:00Z" });
    const out = sortEntries([e("b10.txt"), e("Zeta", true), e("b2.txt"), e("alpha", true)], "name", true).map((x) => x.name);
    expect(out).toEqual(["alpha", "Zeta", "b2.txt", "b10.txt"]);
    expect(sortEntries([e("a", false, 5), e("b", false, 9)], "size", false)[0].name).toBe("b");
  });
  it("filters by type group and date", async () => {
    const { matchesFilters } = await import("./nas");
    const now = new Date(2026, 8, 29, 12).getTime();
    const f = (name: string, daysAgo: number, dir = false) => ({ name, dir, size: 1, modified: new Date(now - daysAgo * 86_400_000).toISOString() });
    expect(matchesFilters(f("a.pdf", 0), "doc", "", now)).toBe(true);
    expect(matchesFilters(f("a.txt", 0), "image", "", now)).toBe(false);
    expect(matchesFilters(f("a.zip", 0), "other", "", now)).toBe(true);
    expect(matchesFilters(f("a.jpg", 3), "", "7d", now)).toBe(true);
    expect(matchesFilters(f("a.jpg", 3), "", "today", now)).toBe(false);
    expect(matchesFilters(f("Fotos", 40, true), "folder", "30d", now)).toBe(false);
  });
  it("refuses moving a folder into itself", () => {
    expect(inside("ana/Fotos/2024", "ana/Fotos")).toBe(true);
    expect(inside("ana/Fotos", "ana/Fotos")).toBe(true);
    expect(inside("ana/Fotos2", "ana/Fotos")).toBe(false);
  });
});

describe("folder uploads", () => {
  const f = (rel: string): Picked => ({ file: new File([], rel.split("/").pop()!), rel });
  it("creates parent folders first", () => {
    expect(foldersOf([f("Fotos/2026/a.jpg"), f("Fotos/b.jpg"), f("solto.txt"), f("Fotos/2025/x/c.jpg")])).toEqual([
      "Fotos",
      "Fotos/2025",
      "Fotos/2026",
      "Fotos/2025/x",
    ]);
  });
  it("sends into a new name when the folder exists", () => {
    const out = renameTop([f("Fotos/a.jpg"), f("solto.txt")], new Set(["Fotos", "Fotos (2)"]));
    expect(out.map((p) => p.rel)).toEqual(["Fotos (3)/a.jpg", "solto.txt"]);
    expect(renameTop([f("Novas/a.jpg")], new Set(["Fotos"]))[0].rel).toBe("Novas/a.jpg");
  });
});

describe("upload chunk sizing", () => {
  const MiB = 1 << 20;
  it("grows on a fast link, at most 4× per step, up to the cap", () => {
    // 8 MiB in 80 ms (~100 MB/s, gigabit LAN): wants 600 MiB, gets 32 MiB.
    expect(nextChunk(CHUNK_MIN, 8 * MiB, 80)).toBe(32 * MiB);
    expect(nextChunk(64 * MiB, 64 * MiB, 600)).toBe(CHUNK_MAX);
  });
  it("stays small on a slow link", () => {
    // 8 MiB in 16 s (~0.5 MB/s, weak 4G): ~3 MiB per 6 s → the 8 MiB floor.
    expect(nextChunk(CHUNK_MIN, 8 * MiB, 16_000)).toBe(CHUNK_MIN);
    // 2 MB/s → ~12 MiB, whole MiB.
    expect(nextChunk(CHUNK_MIN, 8 * MiB, 4_000)).toBe(12 * MiB);
  });
});

describe("upload speed display", async () => {
  const { SpeedMeter, easeToward, fmtBytesFine, fmtRemaining } = await import("./nas");
  it("measures speed over a sliding window and counts stalls", () => {
    const m = new SpeedMeter(5000);
    m.add(0, 0);
    expect(m.rate(500)).toBe(0); // under a second of history
    for (let t = 1000; t <= 10_000; t += 1000) m.add(t, t * 100_000); // 100 MB/s
    expect(m.rate(10_000)).toBeCloseTo(100e6, -3);
    expect(m.rate(15_000)).toBeLessThan(60e6); // bytes stopped: speed falls
    m.add(16_000, 5); // a cancelled file lowers the total: start over
    expect(m.rate(16_000)).toBe(0);
  });
  it("words the remaining time like the big drives", () => {
    expect(fmtRemaining(30_000)).toBe("falta menos de 1 min");
    expect(fmtRemaining(7 * 60_000 - 1)).toBe("faltam ~7 min");
    expect(fmtRemaining(80 * 60_000)).toBe("faltam ~1 h 20 min");
    expect(fmtRemaining(Infinity)).toBe("");
  });
  it("glides counters without overshooting", () => {
    expect(easeToward(500, 700, 100, 400)).toBe(550);
    expect(easeToward(500, 700, 1000, 400)).toBe(700);
    expect(easeToward(699.5, 700, 16)).toBe(700);
  });
  it("shows moving byte counts finely", () => {
    expect(fmtBytesFine(5_723_000_000)).toBe("5,72 GB");
    expect(fmtBytesFine(523_400_000)).toBe("523,4 MB");
    expect(fmtBytesFine(512)).toBe("512 B");
  });
});
