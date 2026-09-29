import { describe, expect, it } from "vitest";
import { crumbs, label, parentPath, restorePath } from "./nas";

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
