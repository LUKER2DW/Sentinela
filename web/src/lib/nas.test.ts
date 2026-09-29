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
