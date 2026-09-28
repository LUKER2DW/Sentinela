import { useId } from "react";

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

/** Repeating wave line; drifts sideways when animated. */
export function Waves({ className = "", rows = 2 }: { className?: string; rows?: number }) {
  const line = (y: number) =>
    // Period 50 units: the pattern repeats exactly, so a -200 unit drift loops seamlessly.
    `M0 ${y} ` + Array.from({ length: 16 }, (_, i) => `q12.5 ${i % 2 ? 6 : -6} 25 0`).join(" ");
  return (
    <svg className={`dd-waves ${className}`} viewBox="0 0 200 40" preserveAspectRatio="none" aria-hidden="true">
      <g className="dd-drift" {...stroke}>
        {Array.from({ length: rows }, (_, r) => (
          <path key={r} d={line(14 + r * 12)} opacity={1 - r * 0.35} transform={`translate(${-r * 9} 0)`} />
        ))}
      </g>
    </svg>
  );
}

/** The sentinel: a small lighthouse with a pulsing lamp. */
export function Lighthouse({ className = "" }: { className?: string }) {
  return (
    <svg className={`dd-lighthouse ${className}`} viewBox="0 0 160 170" aria-hidden="true">
      <g className="dd-beam" {...stroke} strokeWidth={1.6}>
        <path d="M92 55 L154 36" />
        <path d="M92 61 L156 78" />
        <path d="M68 55 L8 38" />
        <path d="M68 61 L5 80" />
      </g>
      <g {...stroke}>
        {/* lamp room + roof */}
        <path d="M69 64 L68.5 47 Q80 44.5 91.5 47.2 L91 64" />
        <path d="M65 47.5 L80.5 34 L95.5 47.8" />
        <path d="M80.5 34 L80.7 28.5" />
        <circle className="dd-lamp" cx="80" cy="56" r="4.2" fill="currentColor" stroke="none" />
        {/* railing */}
        <path d="M63 64.5 Q80 62.5 97.5 64.8" />
        {/* tower */}
        <path d="M70.5 65 L64 146" />
        <path d="M90 65 L96.5 146.5" />
        <path d="M67.8 101 Q80.4 98.5 92.8 101.4" />
        <path d="M66.3 121 Q80.6 118.3 94.6 121.6" />
        <path d="M76.8 80.5 q3.3 -2.2 6.6 0.2 l0.1 7.8 q-3.4 1.1 -6.8 0 z" />
        {/* rock + sea */}
        <path d="M46 147 Q58 141 72 144.5 Q88 140 103 144 Q115 142 120 148" />
        <path d="M18 157 q10 -6 20 0 t20 0 t20 0 t20 0 t20 0 t20 0" className="dd-sway" />
        <path d="M34 166 q10 -5 20 0 t20 0 t20 0 t20 0" opacity={0.55} className="dd-sway dd-sway-slow" />
      </g>
    </svg>
  );
}

export function Gull({ className = "" }: { className?: string }) {
  return (
    <svg className={`dd-gull ${className}`} viewBox="0 0 28 12" aria-hidden="true">
      <path {...stroke} strokeWidth={1.8} d="M2 9 Q7.5 1.5 14 8 Q20 1 26 8.5" />
    </svg>
  );
}

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

/** Small lighthouse glyph used as the brand mark. */
export function Mark({ className = "" }: { className?: string }) {
  return (
    <svg className={className} viewBox="0 0 32 32" aria-hidden="true">
      <g {...stroke} strokeWidth={1.9}>
        <path d="M12.6 11.4 L12.5 7.6 Q16 6.8 19.5 7.7 L19.4 11.4" />
        <path d="M11.4 7.8 L16 3.8 L20.6 7.9" />
        <path d="M12.9 11.6 L10.8 25.6 M19.1 11.6 L21.3 25.7" />
        <path d="M11.9 18.2 Q16 17.4 20.2 18.4" />
        <path d="M4 27.5 q3 -2.2 6 0 t6 0 t6 0 t6 0" />
      </g>
      <circle className="dd-lamp" cx="16" cy="9.6" r="1.4" fill="currentColor" />
    </svg>
  );
}

/**
 * Detailed brand lighthouse: striped tower, gallery, lantern and a beam that
 * sweeps around (a cone whose width flips through zero, like a turning lamp).
 */
export function LighthouseMark({ className = "" }: { className?: string }) {
  const clip = `lh-clip-${useId().replace(/:/g, "")}`;
  return (
    <svg className={`lh-mark ${className}`} viewBox="0 0 48 48" aria-hidden="true">
      <defs>
        <clipPath id={clip}>
          <path d="M19.6 18.2 H28.4 L31.2 40 H16.8 Z" />
        </clipPath>
      </defs>
      {/* beam: drawn first so the tower sits on top of it */}
      <path className="lh-beam" d="M24 13.4 L47 6.5 Q48.5 13.4 47 20.3 Z" />
      {/* tower body + stripes */}
      <path d="M19.6 18.2 H28.4 L31.2 40 H16.8 Z" className="lh-body" />
      <g clipPath={`url(#${clip})`} className="lh-stripe">
        <rect x="10" y="23" width="28" height="4.2" />
        <rect x="10" y="31.4" width="28" height="4.2" />
      </g>
      <path d="M19.6 18.2 H28.4 L31.2 40 H16.8 Z" className="lh-line" />
      <path d="M22.4 40 V36.4 Q24 34.6 25.6 36.4 V40" className="lh-line" />
      {/* gallery + railing */}
      <rect x="16.8" y="16.4" width="14.4" height="1.9" rx="0.6" className="lh-roof" />
      <path d="M17.6 16.4 V14.6 M20.4 16.4 V14.6 M27.6 16.4 V14.6 M30.4 16.4 V14.6 M17.2 14.6 H30.8" className="lh-line thin" />
      {/* lantern room with glass and lamp */}
      <rect x="20.6" y="10.6" width="6.8" height="5.8" rx="0.8" className="lh-glass" />
      <circle cx="24" cy="13.4" r="1.9" className="lh-lamp" />
      <path d="M20.6 10.6 H27.4 M24 10.6 V16.4" className="lh-line thin" />
      {/* dome + finial */}
      <path d="M19.8 10.8 Q24 4.6 28.2 10.8 Z" className="lh-roof" />
      <path d="M24 6.2 V3.8" className="lh-line" />
      {/* rock + sea */}
      <path d="M12.5 41.5 Q17 39 24 40 Q31 39 35.5 41.6 Z" className="lh-rock" />
      <path d="M4 44.6 q3.3 -2.2 6.6 0 t6.6 0 t6.6 0 t6.6 0 t6.6 0 t6.6 0" className="lh-line lh-sea" />
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
