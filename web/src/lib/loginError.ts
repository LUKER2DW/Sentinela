import { plural, t } from "./i18n";

/** Portuguese text for the server's sign-in errors. */
export function loginError(msg: string): string {
  if (msg.includes("invalid username")) return t("Wrong username, email or password.");
  if (msg.includes("invalid code")) return t("Wrong or already used code. Wait for the next code and try again.");
  if (msg.includes("from this address")) return t("Too many wrong attempts from this address. Wait an hour.");
  // The two-step lock (wrong codes after the right password) lasts minutes,
  // not the hour of the account limit, and from outside a good code at home
  // ends it.
  const codeLock = /wrong codes.*locked for (\d+) min/.exec(msg);
  if (codeLock) {
    const mins = Number(codeLock[1]);
    const wait = plural(mins, "Too many wrong codes: two-step verification is locked for {n} minute.", "Too many wrong codes: two-step verification is locked for {n} minutes.");
    return msg.includes("at home") ? `${wait} ${t("A good code at home (home Wi-Fi or Tailscale) unlocks it now.")}` : wait;
  }
  if (msg.includes("for this account")) return t("Too many attempts on this account. Wait an hour.");
  if (msg.includes("too many")) return t("Too many attempts. Wait a minute and try again.");
  if (msg.includes("invalid or expired invite")) return t("Invalid or expired invite. Ask the administrator for a new one.");
  if (msg.includes("disabled")) return t("This account is disabled.");
  if (msg.includes("at least") || msg.includes("too weak") || msg.includes("too common")) return t("Weak password: use 10+ characters mixing letters with numbers or symbols.");
  return msg;
}
