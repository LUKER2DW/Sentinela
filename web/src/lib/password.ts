
import { t } from "./i18n";// Password rule and strength meter for new-password fields. policyError
// mirrors the server's rule (internal/auth CheckPasswordPolicy); the meter
// is advisory on top of it.

export const MIN_PASSWORD = 10;
const PASSPHRASE = 16;

export interface Strength {
  /** 0 = empty, 1 weak … 4 strong */
  score: 0 | 1 | 2 | 3 | 4;
  label: string;
}

const COMMON = ["admin", "senha", "password", "sentinela", "qwerty", "1234", "abcd", "camera", "casa", "0000", "1111"];
const REFUSED = new Set([
  "1234567890", "12345678910", "123456789012", "0123456789", "qwertyuiop", "password123", "senha12345", "senha123456",
  "sentinela123", "iloveyou123", "administrador", "admin12345", "abcdefghij", "1q2w3e4r5t", "qwerty12345",
]);

/** Why the server would refuse pw, in Portuguese; "" when it is accepted. */
export function policyError(pw: string): string {
  const chars = [...pw];
  if (chars.length < MIN_PASSWORD) return t("Use at least {MIN_PASSWORD} characters.", { MIN_PASSWORD });
  if (REFUSED.has(pw.toLowerCase()) || chars.every((c) => c === chars[0])) return t("Very common password.");
  const kinds = [/\p{L}/u, /\p{N}/u, /[^\p{L}\p{N}]/u].filter((re) => re.test(pw)).length;
  if (kinds < 2 && chars.length < PASSPHRASE) return t("Mix letters with numbers or symbols, or use a passphrase of 16+ characters.");
  return "";
}

export function passwordStrength(pw: string): Strength {
  if (!pw) return { score: 0, label: "" };
  const bad = policyError(pw);
  if (bad) return { score: 1, label: bad };

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

  if (bits < 40) return { score: 2, label: t("Accepted, but weak") };
  if (bits < 55) return { score: 2, label: t("Fair") };
  if (bits < 72) return { score: 3, label: t("Good") };
  return { score: 4, label: t("Strong") };
}
