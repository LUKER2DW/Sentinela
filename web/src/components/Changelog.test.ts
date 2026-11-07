import { describe, expect, it } from "vitest";
import { parseChangelog } from "./Changelog";

describe("parseChangelog", () => {
  it("reads versions, groups, wrapped items and intro text", () => {
    const v = parseChangelog(`# Changelog

Intro of the file.

## [Unreleased]

The first release.

### Cameras
- Any number of cameras: XMEye,
  Hikvision and \`generic\`.
- Preset tours.

## [1.0.0] - 2026-11-01
- Plain item.

[Unreleased]: https://example.com
`);
    expect(v.map((x) => x.title)).toEqual(["Unreleased", "1.0.0 - 2026-11-01"]);
    expect(v[0].intro).toEqual(["The first release."]);
    expect(v[0].groups[0]).toEqual({ title: "Cameras", items: ["Any number of cameras: XMEye, Hikvision and `generic`.", "Preset tours."] });
    expect(v[1].groups[0].items).toEqual(["Plain item."]);
  });
});
