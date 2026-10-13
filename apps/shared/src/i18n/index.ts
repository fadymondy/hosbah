import type { Locale } from "../types";
import { ar } from "./ar";
import { en } from "./en";

export { ar, en };
export type MessageKey = keyof typeof en;

export const DEFAULT_LOCALE: Locale = "ar";
export const LOCALES: readonly Locale[] = ["ar", "en"];

const catalogs: Record<Locale, Record<MessageKey, string>> = { ar, en };

export function isRTL(locale: Locale): boolean {
  return locale === "ar";
}

/** An OS locale tag ("en-US", "ar-EG", "fr") to one we ship; anything not English is Arabic. */
export function pickLocale(osLocale: string | null | undefined): Locale {
  return osLocale && /^en(?:[-_]|$)/i.test(osLocale.trim()) ? "en" : DEFAULT_LOCALE;
}

/** Look up a string and fill {name} placeholders; unknown placeholders stay as written. */
export function t(locale: Locale, key: MessageKey, vars?: Record<string, string | number>): string {
  const raw = catalogs[locale]?.[key] ?? en[key] ?? key;
  if (!vars) return raw;
  return raw.replace(/\{(\w+)\}/g, (m, name: string) => (name in vars ? String(vars[name]) : m));
}
