import { describe, expect, it } from "vitest";
import { formProblems, normalizeRepo, sourceText, splitLogLine, stateTone, validRepo, versionText, type AppConfig } from "./hosting";

const cfg = (over: Partial<AppConfig> = {}): AppConfig => ({
  id: "bot",
  name: "Bot",
  source: "github",
  repo: "ana/bot",
  ref: "main",
  mode: "script",
  runtime: "node",
  start: "npm start",
  env: [],
  ports: [{ container: 3000, host: 0, proto: "tcp" }],
  exposure: "lan",
  memory_mb: 256,
  cpus: 0.5,
  autostart: true,
  root: false,
  ...over,
});

describe("hosting helpers", () => {
  it("normalizes repository addresses", () => {
    expect(normalizeRepo("https://github.com/ana/bot.git")).toBe("ana/bot");
    expect(normalizeRepo("git@github.com:ana/bot.git")).toBe("ana/bot");
    expect(normalizeRepo(" ana/bot/ ")).toBe("ana/bot");
    expect(validRepo("ana/bot")).toBe(true);
    expect(validRepo("https://gitlab.com/ana/bot")).toBe(false);
  });

  it("names sources and versions", () => {
    expect(sourceText(cfg())).toBe("ana/bot");
    expect(sourceText(cfg({ ref: "dev", dir: "api" }))).toBe("ana/bot @ dev / api");
    expect(sourceText(cfg({ source: "local", path: "ana/bot" }))).toBe("pasta ana/bot");
    expect(sourceText(cfg({ source: "zip", path: "0123456789abcdef01234567" }))).toBe("Projeto enviado por ZIP");
    expect(versionText({ commit: "0123456789abcdef", version: "0123456789ab" })).toBe("0123456");
    expect(versionText({ commit: "", version: "local-20261001-101500" })).toBe("pasta de 20261001-101500");
  });

  it("splits docker log timestamps", () => {
    const r = splitLogLine("2026-10-01T10:00:00.123456789Z listening on 3000");
    expect(r.text).toBe("listening on 3000");
    expect(r.time).not.toBe("");
    expect(splitLogLine("plain")).toEqual({ time: "", text: "plain" });
  });

  it("maps states to buoy tones", () => {
    expect(stateTone("running")).toBe("ok");
    expect(stateTone("deploying")).toBe("busy");
    expect(stateTone("crashloop")).toBe("bad");
    expect(stateTone("failed")).toBe("bad");
    expect(stateTone("stopped")).toBe("idle");
  });

  it("lists form problems", () => {
    expect(formProblems(cfg())).toEqual([]);
    expect(formProblems(cfg({ repo: "x" }))).toHaveLength(1);
    expect(formProblems(cfg({ start: "" }))).toHaveLength(1);
    expect(formProblems(cfg({ runtime: "static", start: "" }))).toEqual([]);
    expect(formProblems(cfg({ env: [{ key: "A", value: "" }, { key: "A", value: "" }] }))).toHaveLength(1);
    expect(formProblems(cfg({ source: "local", path: "" }))).toHaveLength(1);
    expect(formProblems(cfg({ source: "zip", path: "" }))).toHaveLength(1);
    expect(formProblems(cfg({ source: "zip", path: "0123456789abcdef01234567" }))).toEqual([]);
  });
});
