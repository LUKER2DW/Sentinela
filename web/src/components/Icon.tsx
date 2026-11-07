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
  phone: "M8 3h8a1.5 1.5 0 0 1 1.5 1.5v15A1.5 1.5 0 0 1 16 21H8a1.5 1.5 0 0 1-1.5-1.5v-15A1.5 1.5 0 0 1 8 3zM11 18h2",
  monitor: "M3 5h18v11H3zM8 20h8M12 16v4",
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
  timer: "M12 21a8 8 0 1 0 0-16 8 8 0 0 0 0 16zM12 9v4l2.5 1.5M9.5 2.5h5M18.5 6.5l1.5-1.5",
  eye: "M2.5 12S6 5.5 12 5.5 21.5 12 21.5 12 18 18.5 12 18.5 2.5 12 2.5 12zM12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6z",
  eyeOff: "M3.5 3.5l17 17M10.2 5.7A9.6 9.6 0 0 1 12 5.5c6 0 9.5 6.5 9.5 6.5a16 16 0 0 1-2.7 3.5M6.3 7.2C3.9 8.9 2.5 12 2.5 12S6 18.5 12 18.5c1.9 0 3.5-.6 4.9-1.5M9.9 9.9a3 3 0 0 0 4.2 4.2",
  folder: "M3.5 7.5A2 2 0 0 1 5.5 5.5h4l2 2.5h7a2 2 0 0 1 2 2v7.5a2 2 0 0 1-2 2h-13a2 2 0 0 1-2-2z",
  file: "M6 3.5h8l4 4v13H6zM14 3.5v4h4",
  image: "M4 5h16v14H4zM4 16l5-5 4 4 2.5-2.5L20 17M15.5 9.5h.01",
  film: "M4 5h16v14H4zM8 5v14M16 5v14M4 9.5h4M4 14.5h4M16 9.5h4M16 14.5h4",
  music: "M9 18V6l10-2v12M9 18a2.5 2.5 0 1 1-5 0 2.5 2.5 0 0 1 5 0zM19 16a2.5 2.5 0 1 1-5 0 2.5 2.5 0 0 1 5 0z",
  upload: "M12 20V9M7 14l5-5 5 5M5 4h14",
  folderPlus: "M3.5 7.5A2 2 0 0 1 5.5 5.5h4l2 2.5h7a2 2 0 0 1 2 2v7.5a2 2 0 0 1-2 2h-13a2 2 0 0 1-2-2zM12 11v5M9.5 13.5h5",
  trash: "M4 7h16M9.5 7V4.5h5V7M6 7l1 13h10l1-13M10 11v5M14 11v5",
  share: "M12 3v12M7.5 7.5L12 3l4.5 4.5M5 12v8h14v-8",
  dots: "M5 12h.01M12 12h.01M19 12h.01",
  grid: "M4 4h7v7H4zM13 4h7v7h-7zM4 13h7v7H4zM13 13h7v7h-7z",
  list: "M8 6h12M8 12h12M8 18h12M4 6h.01M4 12h.01M4 18h.01",
  edit: "M4 20h4L19 9l-4-4L4 16zM13.5 6.5l4 4",
  move: "M3.5 7.5A2 2 0 0 1 5.5 5.5h4l2 2.5h7a2 2 0 0 1 2 2v7.5a2 2 0 0 1-2 2h-13a2 2 0 0 1-2-2zM10 14h6M13.5 11.5L16 14l-2.5 2.5",
  restore: "M4 12a8 8 0 1 0 2.3-5.7M4 5v6h6",
  camera: "M4 8.5A1.5 1.5 0 0 1 5.5 7h2.2l1.5-2h5.6l1.5 2h2.2A1.5 1.5 0 0 1 20 8.5v9a1.5 1.5 0 0 1-1.5 1.5h-13A1.5 1.5 0 0 1 4 17.5zM12 16a3.2 3.2 0 1 0 0-6.4 3.2 3.2 0 0 0 0 6.4z",
  record: "M12 18a6 6 0 1 0 0-12 6 6 0 0 0 0 12z",
  stop: "M7 7h10v10H7z",
  user: "M12 12a4 4 0 1 0 0-8 4 4 0 0 0 0 8zM4.5 20a7.5 7.5 0 0 1 15 0",
  users: "M9 11a3.5 3.5 0 1 0 0-7 3.5 3.5 0 0 0 0 7zM2.5 19.5a6.5 6.5 0 0 1 13 0M16 4.3a3.5 3.5 0 0 1 0 6.4M18 13.6a6.5 6.5 0 0 1 3.5 5.9",
  bell: "M6 16V11a6 6 0 0 1 12 0v5l1.5 2h-15zM10 20.5h4",
  home: "M4 11l8-6.5 8 6.5M6 9.5V20h12V9.5M10 20v-5h4v5",
  away: "M12 21s-6.5-5.6-6.5-11a6.5 6.5 0 0 1 13 0c0 5.4-6.5 11-6.5 11zM12 12.5a2.5 2.5 0 1 0 0-5 2.5 2.5 0 0 0 0 5z",
  gauge: "M4.5 17a8 8 0 1 1 15 0M12 13l3.5-4M12 14a1 1 0 1 0 0-2 1 1 0 0 0 0 2z",
  server: "M4 5h16v6H4zM4 13h16v6H4zM7.5 8h.01M7.5 16h.01",
  video: "M3 7.5A2.5 2.5 0 0 1 5.5 5h9A2.5 2.5 0 0 1 17 7.5v9a2.5 2.5 0 0 1-2.5 2.5h-9A2.5 2.5 0 0 1 3 16.5zM17 10l4-2.5v9L17 14",
  scan: "M4 8V4h4M16 4h4v4M20 16v4h-4M8 20H4v-4M9 10a3 3 0 1 0 6 0 3 3 0 0 0-6 0M7.5 18a4.5 4.5 0 0 1 9 0",
  history: "M4 12a8 8 0 1 0 2.3-5.7M4 5v6h6M12 8v4l3 2",
  search: "M11 18a7 7 0 1 0 0-14 7 7 0 0 0 0 14zM20 20l-4-4",
  info: "M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18zM12 11v5M12 7.5h.01",
  sort: "M7 4v16M4 17l3 3 3-3M17 20V4M14 7l3-3 3 3",
  scissors: "M6 9a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM6 21a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM8.2 7.8L20 19M8.2 16.2L20 5",
  skipBack: "M18 6l-8 6 8 6zM6 6v12",
  skipFwd: "M6 6l8 6-8 6zM18 6v12",
  keyboard: "M3 7h18v10H3zM7 10h.01M11 10h.01M15 10h.01M7 14h10",
  network: "M12 3v6M5 21v-3a2 2 0 0 1 2-2h10a2 2 0 0 1 2 2v3M12 9a2 2 0 1 0 0 .01M12 11v5",
  chevronDown: "M6 9l6 6 6-6",
  folderOpen: "M3.5 18V7.5a2 2 0 0 1 2-2h4l2 2.5h6a2 2 0 0 1 2 2V11M3.5 18l2.6-6.2a1.5 1.5 0 0 1 1.4-.8h13a1 1 0 0 1 .9 1.4L19 18.2a1.5 1.5 0 0 1-1.4.8H4.5a1 1 0 0 1-1-1z",
  collapse: "M4 5h16v14H4zM8 12h8",
  panel: "M4 5h16v14H4zM15 5v14",
  pdf: "M6 3.5h8l4 4v13H6zM14 3.5v4h4M9 13h6M9 16.5h4",
  text: "M6 3.5h8l4 4v13H6zM14 3.5v4h4M9 11h6M9 14h6M9 17h4",
  archive: "M4 4h16v4H4zM5.5 8v12h13V8M10 12h4",
  open: "M14 4h6v6M20 4l-9 9M18 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h5",
  folderUp: "M3.5 7.5A2 2 0 0 1 5.5 5.5h4l2 2.5h7a2 2 0 0 1 2 2v7.5a2 2 0 0 1-2 2h-13a2 2 0 0 1-2-2zM12 17v-5.5M9.5 14l2.5-2.5 2.5 2.5",
  alert: "M12 4l9 16H3zM12 10v4.5M12 17.5h.01",
  key: "M14.5 13a4.5 4.5 0 1 0-4.2-2.9L4 16.5V20h3.5v-2h2v-2h2l1.3-1.3a4.5 4.5 0 0 0 1.7.3zM16 8.5h.01",
  app: "M8 3h8a1.5 1.5 0 0 1 1.5 1.5v15A1.5 1.5 0 0 1 16 21H8a1.5 1.5 0 0 1-1.5-1.5v-15A1.5 1.5 0 0 1 8 3zM12 8v6M9.5 11.5L12 14l2.5-2.5",
  sun: "M12 16a4 4 0 1 0 0-8 4 4 0 0 0 0 8zM12 2.5v2M12 19.5v2M4.5 12h-2M21.5 12h-2M5.3 5.3l1.4 1.4M17.3 17.3l1.4 1.4M5.3 18.7l1.4-1.4M17.3 6.7l1.4-1.4",
  bellOff: "M6 16V11a6 6 0 0 1 9.5-4.9M18 11v5l1.5 2H9M10 20.5h4M3.5 3.5l17 17",
  car: "M5 16.5V12l1.8-4.2A2 2 0 0 1 8.6 6.5h6.8a2 2 0 0 1 1.8 1.3L19 12v4.5M5 12h14M4 16.5h16v2H4zM7.5 14.2h.01M16.5 14.2h.01",
  paw: "M12 20c-2.8 0-5-1.6-5-3.6 0-2 2.2-4.4 5-4.4s5 2.4 5 4.4c0 2-2.2 3.6-5 3.6zM6 10.5a1.8 2.2 0 1 0 0-.01M18 10.5a1.8 2.2 0 1 0 0-.01M9.3 6.3a1.8 2.2 0 1 0 0-.01M14.7 6.3a1.8 2.2 0 1 0 0-.01",
  line: "M4 19L20 5M8 7l2.5 2.5M13.5 14.5L16 17",
  zone: "M4 7l6-3 10 4-2 10-9 2-5-6zM12 10v4M10 12h4",
  pointer: "M5 3.5l13 7-5.6 1.6L10 18zM12.4 12.1l4.6 5.4",
  polygon: "M5 8l7-4.5 7 5-2.5 9.5h-9zM5 8h.01M12 3.5h.01M19 8.5h.01M16.5 18h.01M7.5 18h.01",
  square: "M5 5h14v14H5zM5 5h.01M19 5h.01M19 19h.01M5 19h.01",
  undo: "M9 14L4 9l5-5M4 9h10.5a5.5 5.5 0 0 1 0 11H11",
  redo: "M15 14l5-5-5-5M20 9H9.5a5.5 5.5 0 0 0 0 11H13",
  motion: "M13 5a1.5 1.5 0 1 0 0-.01M9 20l2.5-5.5L14 17v4M8 12l3-3.5 3 2 3 .5M11.5 14.5L10 9",
  disk: "M4 7c0-1.7 3.6-3 8-3s8 1.3 8 3v10c0 1.7-3.6 3-8 3s-8-1.3-8-3zM4 7c0 1.7 3.6 3 8 3s8-1.3 8-3M4 12c0 1.7 3.6 3 8 3s8-1.3 8-3",
};

// Hand-drawn look (owner likes the doodle icons): one shared SVG filter
// makes every stroke wobble a little, like ink on paper, and a second,
// fainter pass slightly off the first reads as a pen going over it twice.
// Defined once in the document; deterministic (fixed seed).
if (typeof document !== "undefined" && !document.getElementById("ink-wobble")) {
  const defs = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  defs.setAttribute("aria-hidden", "true");
  defs.setAttribute("width", "0");
  defs.setAttribute("height", "0");
  defs.style.position = "absolute";
  defs.innerHTML =
    '<filter id="ink-wobble" x="-2" y="-2" width="28" height="28" filterUnits="userSpaceOnUse">' +
    '<feTurbulence type="fractalNoise" baseFrequency="0.09" numOctaves="1" seed="7" result="n"/>' +
    '<feDisplacementMap in="SourceGraphic" in2="n" scale="1.3" xChannelSelector="R" yChannelSelector="G"/></filter>';
  document.body.appendChild(defs);
}

export function Icon({ name, title }: { name: keyof typeof paths | string; title?: string }) {
  const d = paths[name] ?? "";
  return (
    <svg className="ico" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" strokeLinejoin="round" aria-hidden={title ? undefined : true} role={title ? "img" : undefined}>
      {title && <title>{title}</title>}
      <g filter="url(#ink-wobble)">
        <path d={d} />
        <path className="ico-echo" d={d} transform="translate(0.6 0.45) rotate(0.8 12 12)" strokeWidth={1} opacity={0.5} />
      </g>
    </svg>
  );
}
