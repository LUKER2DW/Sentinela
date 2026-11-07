// Translations. English is the source language of the code: UI text is
// written in English and wrapped in t("…"). Brazilian Portuguese lives in
// lib/locales/pt-BR.ts (English key -> Portuguese). A key without a translation
// shows in English, so the UI never breaks while translations catch up.
//
// The language comes from the person's choice (Appearance), else from the
// device: Portuguese devices get pt-BR, every other one gets English. No IP
// lookup: the device language is more precise and nothing leaves the house.
import { useSyncExternalStore } from "react";
import { ptBR } from "./locales/pt-BR";

export type Lang = "pt-BR" | "en";
export const LANGS: { id: Lang | "auto"; label: string }[] = [
  { id: "auto", label: "Automatic (device language)" },
  { id: "pt-BR", label: "Português (Brasil)" },
  { id: "en", label: "English" },
];

const KEY = "sentinela.lang";
// English is the source language of the code, so it needs no table.
const dictionaries: Record<Lang, Record<string, string> | null> = { "pt-BR": ptBR, en: null };

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
export function t(pt: string, vars?: Record<string, string | number | null | undefined>): string {
  const dict = dictionaries[lang];
  let out = (dict && dict[pt]) || pt;
  if (vars) for (const [k, v] of Object.entries(vars)) out = out.replaceAll(`{${k}}`, v == null ? "" : String(v));
  return out;
}

/** "1 movimento" / "{n} movimentos": picks the form by n and translates it. */
export function plural(n: number, one: string, many: string): string {
  return t(n === 1 ? one : many, { n: fmtNum(n) });
}

/** A number in the UI language (decimal comma in Portuguese). */
export function fmtNum(n: number, maxDigits = 1, minDigits = 0): string {
  return n.toLocaleString(lang, { maximumFractionDigits: maxDigits, minimumFractionDigits: minDigits });
}

/** Wraps a map of Portuguese labels so each read comes back translated. */
export function translated<T extends Record<string, string>>(labels: T): T {
  return new Proxy(labels, {
    get: (o, k) => (typeof k === "string" && Object.prototype.hasOwnProperty.call(o, k) ? t(o[k]) : Reflect.get(o, k)),
  });
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
