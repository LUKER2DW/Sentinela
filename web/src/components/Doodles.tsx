// Hand-drawn line doodles (ocean theme). Stroke-only, currentColor, slightly
// wobbly paths so they read as sketches. Motion lives in CSS (.dd-*) and is
// disabled under prefers-reduced-motion.

const stroke = {
  fill: "none",
  stroke: "currentColor",
  strokeWidth: 2,
  strokeLinecap: "round" as const,
  strokeLinejoin: "round" as const,
};

export function Boat({ className = "" }: { className?: string }) {
  return (
    <svg className={`dd-boat ${className}`} viewBox="0 0 120 80" aria-hidden="true">
      <g className="dd-bob" {...stroke}>
        <path d="M24 50 Q60 58 97 49.5 L88 61 Q60 66 32 61.5 Z" />
        <path d="M60 50.5 L60.5 14" />
        <path d="M61 16 Q80 30 84.5 46 Q72 47.5 61.5 47.5" />
        <path d="M59 20 Q46 33 42 46.5 Q51 47.8 58.8 47.8" />
      </g>
      <g {...stroke} strokeWidth={1.8}>
        <path className="dd-sway" d="M6 68 q9 -5 18 0 t18 0 t18 0 t18 0 t18 0 t18 0" />
        <path className="dd-sway dd-sway-slow" d="M18 76 q9 -4 18 0 t18 0 t18 0 t18 0" opacity={0.55} />
      </g>
    </svg>
  );
}

export function Fish({ className = "" }: { className?: string }) {
  return (
    <svg className={`dd-fish ${className}`} viewBox="0 0 120 70" aria-hidden="true">
      <g className="dd-swim" {...stroke}>
        <path d="M28 35 Q48 14 76 33.5 Q48 56 28 35 Z" />
        <path d="M76 33.5 L92 22 Q89 34 92.5 46 Z" />
        <circle cx="40" cy="32" r="1.8" fill="currentColor" stroke="none" />
        <path d="M52 27 Q56 34 52.5 41" opacity={0.6} />
      </g>
      <g {...stroke} strokeWidth={1.6} className="dd-bubbles">
        <circle cx="20" cy="26" r="2.6" />
        <circle cx="13" cy="16" r="1.8" />
        <circle cx="17" cy="8" r="1.2" />
      </g>
    </svg>
  );
}

export function Sun({ className = "", style }: { className?: string; style?: React.CSSProperties }) {
  return (
    <svg className={className} style={style} viewBox="0 0 24 24" aria-hidden="true">
      <g {...stroke} strokeWidth={1.7}>
        <circle cx="12" cy="12" r="4.3" />
        <path d="M12 2.5v2.4M12 19.1v2.4M2.5 12h2.4M19.1 12h2.4M5.3 5.3l1.7 1.7M17 17l1.7 1.7M5.3 18.7 7 17M17 7l1.7-1.7" />
      </g>
    </svg>
  );
}

export function Moon({ className = "", style }: { className?: string; style?: React.CSSProperties }) {
  return (
    <svg className={className} style={style} viewBox="0 0 24 24" aria-hidden="true">
      <path {...stroke} strokeWidth={1.7} d="M19.5 14.6A7.8 7.8 0 0 1 9.4 4.5a7.8 7.8 0 1 0 10.1 10.1z" />
    </svg>
  );
}

function wavePath(y: number, amp: number, period: number, width = 480, height = 120) {
  let d = `M0 ${y}`;
  for (let x = 0; x < width; x += period / 2) {
    const dir = (x / (period / 2)) % 2 === 0 ? -1 : 1;
    d += ` q${period / 4} ${dir * amp} ${period / 2} 0`;
  }
  return `${d} L${width} ${height} L0 ${height} Z`;
}

/** Layered pastel sea for the sidebar foot: three drifting wave bands, a boat, a fish. */
export function OceanScene({ className = "" }: { className?: string }) {
  return (
    <svg className={`ocean ${className}`} viewBox="0 0 240 120" preserveAspectRatio="xMidYMax slice" aria-hidden="true">
      <path className="o-wave o-back" d={wavePath(46, 5, 60)} />
      <g className="o-boat">
        <path d="M150 44 Q162 49 175 44 L171 50 Q162 52.5 154 50 Z" />
        <path d="M162.4 44.6 V28.5 L172 42.4 Z M161.6 31 L154.5 42.6 H161.6 Z" />
      </g>
      <path className="o-wave o-mid" d={wavePath(62, 6, 80)} />
      <g className="o-fish">
        <path d="M60 86 Q66 81 73 85.5 Q66 90 60 86 Z M73 85.5 L77 82.5 L76.6 88.6 Z" />
      </g>
      <path className="o-wave o-front" d={wavePath(80, 7, 120)} />
    </svg>
  );
}

/** A small whale, for the Nuvem page. */
export function Whale({ className = "" }: { className?: string }) {
  return (
    <svg className={`dd-whale ${className}`} viewBox="0 0 160 100" aria-hidden="true">
      <g className="dd-bob" {...stroke}>
        <path d="M22 58 Q26 34 58 32 Q96 30 112 52 Q120 62 132 54 L140 44 Q142 58 134 64 Q140 70 141 80 L130 70 Q116 76 98 76 Q56 80 36 72 Q22 67 22 58 Z" />
        <path d="M38 64 Q58 70 84 67" opacity={0.6} />
        <circle cx="44" cy="50" r="2" fill="currentColor" stroke="none" />
        <path d="M60 30 Q57 20 50 16 M60 30 Q62 18 70 14 M60 30 V16" className="dd-spout" />
      </g>
      <g {...stroke} strokeWidth={1.8}>
        <path className="dd-sway" d="M8 88 q10 -5 20 0 t20 0 t20 0 t20 0 t20 0 t20 0 t20 0" />
      </g>
    </svg>
  );
}
