// Translations. Brazilian Portuguese is the source language: UI text is
// written in Portuguese and wrapped in t("…"); other languages map those
// strings to theirs (locales/<lang>.ts). A string without a translation
// shows in Portuguese, so the UI never breaks while translations catch up.
//
// The language comes from the person's choice (Aparência), else from the
// device: Portuguese devices get pt-BR, every other one gets English. No IP
// lookup: the device language is more precise and nothing leaves the house.
import { useSyncExternalStore } from "react";
import { en } from "./locales/en";

export type Lang = "pt-BR" | "en";
export const LANGS: { id: Lang | "auto"; label: string }[] = [
  { id: "auto", label: "Automático (idioma do aparelho)" },
  { id: "pt-BR", label: "Português (Brasil)" },
  { id: "en", label: "English" },
];

const KEY = "sentinela.lang";
const dictionaries: Record<Lang, Record<string, string> | null> = { "pt-BR": null, en };

/** The language a device asks for: pt-BR for any Portuguese, else English. */
export function detectLang(languages: readonly string[] = typeof navigator !== "undefined" ? navigator.languages ?? [navigator.language] : []): Lang {
  const first = languages.find(Boolean)?.toLowerCase() ?? "pt-br";
  return first.startsWith("pt") ? "pt-BR" : "en";
}

function stored(): Lang | "auto" {
  try {
    const v = localStorage.getItem(KEY);
    return v === "pt-BR" || v === "en" ? v : "auto";
  } catch {
    return "auto";
  }
}

let choice: Lang | "auto" = stored();
let lang: Lang = choice === "auto" ? detectLang() : choice;
const listeners = new Set<() => void>();

function apply() {
  lang = choice === "auto" ? detectLang() : choice;
  if (typeof document !== "undefined") document.documentElement.lang = lang;
  listeners.forEach((l) => l());
}
apply();

export const getLang = () => lang;
export const getLangChoice = () => choice;

/** Sets the language ("auto" = the device's) and re-renders the UI. */
export function setLang(next: Lang | "auto") {
  choice = next;
  try {
    if (next === "auto") localStorage.removeItem(KEY);
    else localStorage.setItem(KEY, next);
  } catch {
    // private window: the choice lasts for this tab only
  }
  apply();
}

/** Translates a Portuguese UI string; {name} placeholders are filled from vars. */
export function t(pt: string, vars?: Record<string, string | number>): string {
  const dict = dictionaries[lang];
  let out = (dict && dict[pt]) || pt;
  if (vars) for (const [k, v] of Object.entries(vars)) out = out.replaceAll(`{${k}}`, String(v));
  return out;
}

/** Re-renders a component when the language changes. */
export function useLang(): Lang {
  return useSyncExternalStore(
    (cb) => {
      listeners.add(cb);
      return () => listeners.delete(cb);
    },
    () => lang,
    () => lang,
  );
}
