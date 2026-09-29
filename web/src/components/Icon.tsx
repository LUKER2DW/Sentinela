// Inline stroke icons (24×24 grid). Kept local: no icon library dependency.
const paths: Record<string, string> = {
  live: "M3 7.5A2.5 2.5 0 0 1 5.5 5h9A2.5 2.5 0 0 1 17 7.5v9a2.5 2.5 0 0 1-2.5 2.5h-9A2.5 2.5 0 0 1 3 16.5zM17 10l4-2.5v9L17 14",
  recordings: "M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18zM12 7v5l3 2",
  events: "M4 18h16M6 18V11a6 6 0 0 1 12 0v7M10 21h4M12 3v2",
  system: "M4 6h16M4 12h16M4 18h16M8 4v4M15 10v4M11 16v4",
  up: "M6 15l6-6 6 6",
  down: "M6 9l6 6 6-6",
  left: "M15 6l-6 6 6 6",
  right: "M9 6l6 6-6 6",
  up_left: "M16 16L8 8M8 15V8h7",
  up_right: "M8 16l8-8M9 8h7v7",
  down_left: "M16 8l-8 8M8 9v7h7",
  down_right: "M8 8l8 8M16 9v7H9",
  plus: "M12 5v14M5 12h14",
  minus: "M5 12h14",
  fullscreen: "M4 9V4h5M20 9V4h-5M4 15v5h5M20 15v5h-5",
  mute: "M4 9h4l5-4v14l-5-4H4zM17 9l4 6M21 9l-4 6",
  sound: "M4 9h4l5-4v14l-5-4H4zM17 8.5a5 5 0 0 1 0 7M19.5 6a8.5 8.5 0 0 1 0 12",
  mic: "M12 15a3 3 0 0 0 3-3V6a3 3 0 0 0-6 0v6a3 3 0 0 0 3 3zM6 11a6 6 0 0 0 12 0M12 17v4",
  light: "M9 18h6M10 21h4M12 3a6 6 0 0 0-3.5 10.9c.6.5 1 1.2 1 2.1h5c0-.9.4-1.6 1-2.1A6 6 0 0 0 12 3z",
  moon: "M20 14.5A8 8 0 0 1 9.5 4a8 8 0 1 0 10.5 10.5z",
  download: "M12 4v11M7 10l5 5 5-5M5 20h14",
  lock: "M7 11V8a5 5 0 0 1 10 0v3M5 11h14v10H5z",
  chevronLeft: "M15 6l-6 6 6 6",
  chevronRight: "M9 6l6 6-6 6",
  logout: "M15 4h4v16h-4M10 8l-4 4 4 4M6 12h10",
  play: "M8 5v14l11-7z",
  pause: "M8 5v14M16 5v14",
  hd: "M4 6h16v12H4zM8 9v6M8 12h3M11 9v6M14 9v6h1.5a2.5 2.5 0 0 0 0-5H14",
  x: "M6 6l12 12M18 6L6 18",
  refresh: "M20 11a8 8 0 1 0-2.3 5.7M20 5v6h-6",
  shield: "M12 3l8 3v6c0 4.5-3.4 8.4-8 9-4.6-.6-8-4.5-8-9V6z",
  cloud: "M7 18.5h10.5a4 4 0 0 0 .6-7.95A6 6 0 0 0 6.5 9.2 4.7 4.7 0 0 0 7 18.5z",
  sidebar: "M4 5h16v14H4zM9 5v14",
  copy: "M9 9h10v10H9zM5 15V5h10",
  check: "M5 12.5l4.5 4.5L19 7.5",
  eye: "M2.5 12S6 5.5 12 5.5 21.5 12 21.5 12 18 18.5 12 18.5 2.5 12 2.5 12zM12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6z",
  eyeOff: "M3.5 3.5l17 17M10.2 5.7A9.6 9.6 0 0 1 12 5.5c6 0 9.5 6.5 9.5 6.5a16 16 0 0 1-2.7 3.5M6.3 7.2C3.9 8.9 2.5 12 2.5 12S6 18.5 12 18.5c1.9 0 3.5-.6 4.9-1.5M9.9 9.9a3 3 0 0 0 4.2 4.2",
    disk: "M4 7c0-1.7 3.6-3 8-3s8 1.3 8 3v10c0 1.7-3.6 3-8 3s-8-1.3-8-3zM4 7c0 1.7 3.6 3 8 3s8-1.3 8-3M4 12c0 1.7 3.6 3 8 3s8-1.3 8-3",
};

export function Icon({ name, title }: { name: keyof typeof paths | string; title?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" strokeLinejoin="round" aria-hidden={title ? undefined : true} role={title ? "img" : undefined}>
      {title && <title>{title}</title>}
      <path d={paths[name] ?? ""} />
    </svg>
  );
}
