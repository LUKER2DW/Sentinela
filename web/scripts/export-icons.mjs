// The Sentinela icon is the web app's lighthouse mark (components/Lighthouse
// .tsx, LighthouseMark): the same hand-drawn tower, lamp, beam and sea, in
// the dark theme's colours on a night tile. Every icon is made from it:
//   web/public/icon.svg (favicon, PWA), icon-192/512.png, icon-maskable-512.png
//   and the Android launcher (legacy, round, and the adaptive icon's two
//   layers: the night behind, the lighthouse in front).
// PNGs are rendered with librsvg in a throwaway container (needs Docker).
// Run after changing the mark:  cd web && node scripts/export-icons.mjs
import { execFileSync } from "node:child_process";
import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { createServer } from "vite";

const web = join(dirname(fileURLToPath(import.meta.url)), "..");
const res = join(web, "..", "mobile", "android", "app", "src", "main", "res");

// Dark theme tokens (styles.css, :root[data-theme="dark"]).
const C = { ink: "#a4d0c5", paper: "#222524", band: "#d68e7d", glass: "#233631", roof: "#4f8c80", lamp: "#f0c36e" };
const NIGHT_TOP = "#1f2b2a";
const NIGHT_BOTTOM = "#141716";

const vite = await createServer({ root: web, server: { middlewareMode: true }, appType: "custom", logLevel: "error" });
const { LighthouseMark } = await vite.ssrLoadModule("/src/components/Lighthouse.tsx");
const markup = renderToStaticMarkup(createElement(LighthouseMark, { still: true }));
await vite.close();

// The mark's inner drawing (its own 48×48 units), with CSS variables resolved.
const inner = markup
  .replace(/^<svg[^>]*>/, "")
  .replace(/<\/svg>$/, "")
  .replaceAll("var(--lamp)", C.lamp);

// Icon strokes are a little heavier than on the page so they survive 48 px.
const style = `<style>
  .ink{fill:none;stroke:${C.ink};stroke-width:1.35;stroke-linecap:round;stroke-linejoin:round}
  .ink.thin{stroke-width:1}
  .f-paper{fill:${C.paper}}.f-band{fill:${C.band}}.f-glass{fill:${C.glass}}.f-lamp{fill:${C.lamp}}.f-roof{fill:${C.roof}}
  .lh-beam{opacity:.75}
</style>`;
const night = `<defs><linearGradient id="night" x1="0" y1="0" x2="0" y2="1">
  <stop offset="0" stop-color="${NIGHT_TOP}"/><stop offset="1" stop-color="${NIGHT_BOTTOM}"/></linearGradient></defs>`;

// The mark scaled into the middle of a 48-unit tile.
const mark = (scale) => {
  const off = (48 - 48 * scale) / 2;
  return `<g transform="translate(${off} ${off + 0.6}) scale(${scale})">${inner}</g>`;
};
const svg = (viewBox, body) => `<svg xmlns="http://www.w3.org/2000/svg" viewBox="${viewBox}">${style}${night}${body}</svg>\n`;
const bg = (b = 0) => `<rect x="${-b}" y="${-b}" width="${48 + 2 * b}" height="${48 + 2 * b}" fill="url(#night)"/>`;

const sources = {
  // Rounded tile: favicon, PWA "any", Android legacy.
  tile: svg("0 0 48 48", `<clipPath id="t"><rect width="48" height="48" rx="11"/></clipPath><g clip-path="url(#t)">${bg()}${mark(0.84)}</g>`),
  round: svg("0 0 48 48", `<clipPath id="c"><circle cx="24" cy="24" r="24"/></clipPath><g clip-path="url(#c)">${bg()}${mark(0.8)}</g>`),
  // Full bleed, drawing inside the maskable safe circle (80 %).
  maskable: svg("-6 -6 60 60", `${bg(6)}${mark(0.84)}`),
  // Android adaptive: 108 dp canvas of which 72 dp show → 48 units of 72.
  adaptiveBack: svg("-12 -12 72 72", bg(12)),
  adaptiveFront: svg("-12 -12 72 72", mark(0.9)),
};

writeFileSync(join(web, "public", "icon.svg"), sources.tile);

execFileSync("docker", ["build", "-q", "-t", "sentinela-rsvg", "-"], { input: "FROM alpine:3.22\nRUN apk add --no-cache rsvg-convert\n" });
const render = (src, px, out) => {
  const png = execFileSync("docker", ["run", "--rm", "-i", "sentinela-rsvg", "rsvg-convert", "-w", String(px), "-h", String(px)], { input: sources[src] });
  mkdirSync(dirname(out), { recursive: true });
  writeFileSync(out, png);
};
render("tile", 192, join(web, "public", "icon-192.png"));
render("tile", 512, join(web, "public", "icon-512.png"));
render("maskable", 512, join(web, "public", "icon-maskable-512.png"));
for (const [d, s] of Object.entries({ mdpi: 1, hdpi: 1.5, xhdpi: 2, xxhdpi: 3, xxxhdpi: 4 })) {
  render("tile", 48 * s, join(res, `mipmap-${d}`, "ic_launcher.png"));
  render("round", 48 * s, join(res, `mipmap-${d}`, "ic_launcher_round.png"));
  render("adaptiveFront", 108 * s, join(res, `mipmap-${d}`, "ic_launcher_foreground.png"));
  render("adaptiveBack", 108 * s, join(res, `mipmap-${d}`, "ic_launcher_background.png"));
}
console.log("icons written");
