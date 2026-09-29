import { setTheme, useTheme } from "../lib/theme";
import { Sketch } from "../lib/sketch";

// One round button: a hand-drawn sun in light mode, a moon in dark mode. The
// icon shows the current theme; clicking switches to the other one.

const sk = new Sketch(9, 0.12);
const SUN =
  sk.ellipse(12, 12, 4.4, 4.4) +
  Array.from({ length: 8 }, (_, i) => {
    const a = (i * Math.PI) / 4;
    return sk.line([12 + Math.cos(a) * 7, 12 + Math.sin(a) * 7], [12 + Math.cos(a) * 9.6, 12 + Math.sin(a) * 9.6], 1);
  }).join("");
// Crescent: outer arc of a circle, then a shallower inner arc back.
const MOON = sk.curve([
  ...[-60, -95, -130, -165, -200, -235, -270, -300].map((deg) => {
    const a = (deg * Math.PI) / 180;
    return [12 + Math.cos(a) * 8.4, 12 + Math.sin(a) * 8.4] as const;
  }),
  [12.8, 15.6],
  [11.6, 11.2],
  [13.1, 6.6],
  [16.2, 4.7],
]);
const STARS = sk.line([19, 5], [19, 8.4], 1) + sk.line([17.3, 6.7], [20.7, 6.7], 1);

export function ThemeToggle({ className = "" }: { className?: string }) {
  const theme = useTheme();
  const dark = theme === "dark";
  const label = dark ? "Usar tema claro" : "Usar tema escuro";
  return (
    <button className={`theme-btn ${className}`} onClick={() => setTheme(dark ? "light" : "dark")} aria-label={label} title={label}>
      <svg viewBox="0 0 24 24" aria-hidden="true" data-theme-icon={theme}>
        <path className="ti-sun" d={SUN} />
        <g className="ti-moon">
          <path d={MOON} />
          <path d={STARS} />
        </g>
      </svg>
    </button>
  );
}
