"use client"

import Link from "next/link"
import { usePathname, useSearchParams } from "next/navigation"
import { useTheme } from "next-themes"
import { LanguagesIcon, MoonIcon, SunIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import { useI18n } from "@/lib/i18n"
import type { Locale } from "@/lib/i18n-config"

/** The same path in the other locale, query kept. */
export function useOtherLocaleHref(): { href: string; target: Locale } {
  const { locale } = useI18n()
  const pathname = usePathname() ?? "/"
  const search = useSearchParams()
  const target: Locale = locale === "ar" ? "en" : "ar"
  const rest = pathname.replace(/^\/(en|ar)(?=\/|$)/, "")
  const qs = search?.toString()
  return { href: `/${target}${rest}${qs ? `?${qs}` : ""}`, target }
}

export function LanguageSwitch() {
  const { t } = useI18n()
  const { href, target } = useOtherLocaleHref()
  return (
    <Button
      variant="ghost"
      size="sm"
      nativeButton={false}
      render={<Link href={href} hrefLang={target} lang={target} />}
      aria-label={t("prefs.switchLanguage")}
    >
      <LanguagesIcon />
      {target === "ar" ? "العربية" : "English"}
    </Button>
  )
}

export function ThemeToggle() {
  const { t } = useI18n()
  const { resolvedTheme, setTheme } = useTheme()
  return (
    <Button
      variant="ghost"
      size="icon-sm"
      aria-label={t("prefs.toggleTheme")}
      title={t("prefs.toggleTheme")}
      onClick={() => setTheme(resolvedTheme === "dark" ? "light" : "dark")}
    >
      <SunIcon className="hidden dark:block" />
      <MoonIcon className="dark:hidden" />
    </Button>
  )
}
