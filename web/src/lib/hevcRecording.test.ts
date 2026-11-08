import { describe, expect, it } from "vitest";
import { mimeOf, splitBoxes } from "./hevcRecording";

const box = (type: string, payload: number[] = []) => {
  const size = 8 + payload.length;
  return [size >>> 24, (size >>> 16) & 255, (size >>> 8) & 255, size & 255, ...[...type].map((c) => c.charCodeAt(0)), ...payload];
};
const ascii = (s: string) => [...s].map((c) => c.charCodeAt(0));

describe("recording boxes", () => {
  it("returns complete top-level boxes and keeps the rest for later", () => {
    const bytes = new Uint8Array([...box("ftyp", [1, 2]), ...box("moov", [3]), ...box("moof", [4, 5, 6]).slice(0, 9)]);
    const { boxes, rest } = splitBoxes(bytes);
    expect(boxes.map((b) => b.type)).toEqual(["ftyp", "moov"]);
    expect(rest.length).toBe(9);
  });

  it("names the codecs of an H.265 init segment, and refuses H.264", () => {
    expect(mimeOf(new Uint8Array([...box("moov", [...ascii("hvc1"), ...ascii("mp4a")])]))).toBe('video/mp4; codecs="hvc1.1.6.L153.B0,mp4a.40.2"');
    expect(mimeOf(new Uint8Array([...box("moov", ascii("hev1"))]))).toBe('video/mp4; codecs="hvc1.1.6.L153.B0"');
    expect(mimeOf(new Uint8Array([...box("moov", ascii("avc1"))]))).toBe("");
  });
});
