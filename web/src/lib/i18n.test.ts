import { describe, expect, it } from "vitest";
import { detectLang, setLang, t } from "./i18n";

describe("i18n", () => {
  it("picks Portuguese for Portuguese devices and English for the others", () => {
    expect(detectLang(["pt-BR", "en"])).toBe("pt-BR");
    expect(detectLang(["pt-PT"])).toBe("pt-BR");
    expect(detectLang(["en-US"])).toBe("en");
    expect(detectLang(["es-AR", "pt-BR"])).toBe("en");
    expect(detectLang([])).toBe("pt-BR");
  });
  it("translates, fills placeholders and falls back to Portuguese", () => {
    setLang("en");
    expect(t("Live")).toBe("Live");
    expect(t("Version {v}", { v: "1.2.0" })).toBe("Version 1.2.0");
    expect(t("Frase que não existe no dicionário")).toBe("Frase que não existe no dicionário");
    setLang("pt-BR");
    expect(t("Live")).toBe("Ao vivo");
    setLang("auto");
  });
});
