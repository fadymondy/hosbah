"use client"

import { Button, DropdownMenu, DropdownMenuContent, DropdownMenuGroup, DropdownMenuLabel, DropdownMenuRadioGroup, DropdownMenuRadioItem, DropdownMenuTrigger } from "@fadymondy/nasaq/web"
import { usePathname, useSearchParams } from "next/navigation"
import { useTheme } from "next-themes"
import { LanguagesIcon, MoonIcon, SunIcon } from "lucide-react"



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

/** The language picker for the app's top bar (Mahaam's pattern): an icon that opens both locales. */
export function LanguageMenu() {
  const { t, locale } = useI18n()
  const { href } = useOtherLocaleHref()
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={<Button variant="ghost" size="icon-sm" aria-label={t("prefs.language")} title={t("prefs.language")} />}
      >
        <LanguagesIcon />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-40">
        <DropdownMenuGroup>
          <DropdownMenuLabel>{t("prefs.language")}</DropdownMenuLabel>
          <DropdownMenuRadioGroup
            value={locale}
            onValueChange={(v) => {
              // A full navigation: the root layout sets <html lang dir> on the server.
              if (v !== locale) window.location.assign(href)
            }}
          >
            <DropdownMenuRadioItem value="en" lang="en">
              English
            </DropdownMenuRadioItem>
            <DropdownMenuRadioItem value="ar" lang="ar">
              العربية
            </DropdownMenuRadioItem>
          </DropdownMenuRadioGroup>
        </DropdownMenuGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
