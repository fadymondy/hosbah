import type { Locale } from "@/lib/i18n-config"

/**
 * A `?next=` target, only if it is a same-site path (never `//host` or a URL), re-prefixed with
 * the current locale so switching language on the login page does not send you back in the old one.
 */
export function safeNext(raw: string | null | undefined, locale: Locale): string | null {
  if (!raw || !raw.startsWith("/") || raw.startsWith("//") || raw.startsWith("/\\")) return null
  return raw.replace(/^\/(en|ar)(?=\/|$|\?)/, `/${locale}`)
}
