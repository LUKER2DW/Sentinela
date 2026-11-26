import { describe, expect, it } from "vitest";
import { loginError } from "./loginError";

describe("loginError", () => {
  it.each([
    // The server's code lock (handlers_auth.go codeLockedMessage): minutes, not an hour.
    [
      "too many wrong codes for this account: two-step login is locked for 1 min (a good code at home unlocks it)",
      "Muitos códigos errados: a verificação em 2 etapas está bloqueada por 1 minuto. Um código certo em casa (Wi-Fi de casa ou Tailscale) desbloqueia na hora.",
    ],
    [
      "too many wrong codes for this account: two-step login is locked for 32 min (a good code at home unlocks it)",
      "Muitos códigos errados: a verificação em 2 etapas está bloqueada por 32 minutos. Um código certo em casa (Wi-Fi de casa ou Tailscale) desbloqueia na hora.",
    ],
    // At home there is no other place to unlock it from.
    ["too many wrong codes for this account: two-step login is locked for 4 min", "Muitos códigos errados: a verificação em 2 etapas está bloqueada por 4 minutos."],
    ["too many attempts for this account, wait an hour", "Muitas tentativas nesta conta. Espere uma hora."],
    ["too many failed attempts from this address, wait an hour", "Muitas tentativas erradas deste endereço. Espere uma hora."],
    ["too many attempts, wait a minute", "Muitas tentativas. Espere um minuto e tente de novo."],
  ])("%s", (msg, want) => {
    expect(loginError(msg)).toBe(want);
  });
});
