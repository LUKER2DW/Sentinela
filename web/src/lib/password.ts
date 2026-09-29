// Rough password strength for the meter under new-password fields. Advisory
// only: the server enforces the real rule (minimum length).

export interface Strength {
  /** 0 = empty, 1 weak … 4 strong */
  score: 0 | 1 | 2 | 3 | 4;
  label: string;
}

const COMMON = ["admin", "senha", "password", "sentinela", "qwerty", "1234", "abcd", "camera", "casa", "0000", "1111"];

export function passwordStrength(pw: string, minLen = 8): Strength {
  if (!pw) return { score: 0, label: "" };
  if (pw.length < minLen) return { score: 1, label: `Curta: mínimo ${minLen} caracteres` };

  let pool = 0;
  if (/[a-z]/.test(pw)) pool += 26;
  if (/[A-Z]/.test(pw)) pool += 26;
  if (/[0-9]/.test(pw)) pool += 10;
  if (/[^a-zA-Z0-9]/.test(pw)) pool += 33;
  // Repeated characters add little: count distinct ones at full weight.
  const distinct = new Set(pw).size;
  let bits = (distinct + (pw.length - distinct) * 0.3) * Math.log2(Math.max(pool, 2));

  const low = pw.toLowerCase();
  for (const w of COMMON) if (low.includes(w)) bits -= 12;
  if (/(.)\1\1/.test(pw)) bits -= 6;

  if (bits < 40) return { score: 1, label: "Fraca" };
  if (bits < 55) return { score: 2, label: "Razoável" };
  if (bits < 72) return { score: 3, label: "Boa" };
  return { score: 4, label: "Forte" };
}
