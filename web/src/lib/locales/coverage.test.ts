// Every literal t("…") / plural(n, "…", "…") key in the code has a
// Brazilian Portuguese entry, so the pt-BR UI never falls back to English.
import { readFileSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { ptBR } from "./pt-BR";

const SRC = join(__dirname, "..", "..");

function sources(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) return name === "locales" ? [] : sources(p);
    // i18n.ts only mentions t("…") in its comments.
    return /\.tsx?$/.test(name) && !/\.test\./.test(name) && name !== "i18n.ts" ? [p] : [];
  });
}

// A double-quoted JS string literal (escapes allowed).
const STR = String.raw`"((?:[^"\\\n]|\\.)*)"`;
const T_CALL = new RegExp(String.raw`\bt\(\s*` + STR, "g");
const PLURAL = new RegExp(String.raw`\bplural\([^,()]+(?:\([^()]*\))?,\s*` + STR + String.raw`,\s*` + STR, "g");

describe("pt-BR dictionary", () => {
  it("translates every key used in the code", () => {
    const missing = new Set<string>();
    for (const file of sources(SRC)) {
      const text = readFileSync(file, "utf8");
      for (const m of text.matchAll(T_CALL)) if (!(JSON.parse(`"${m[1]}"`) in ptBR)) missing.add(m[1]);
      for (const m of text.matchAll(PLURAL)) {
        for (const k of [m[1], m[2]]) if (!(JSON.parse(`"${k}"`) in ptBR)) missing.add(k);
      }
    }
    expect([...missing]).toEqual([]);
  });
});
