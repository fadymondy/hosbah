"use client"

import { useEffect, type ReactNode } from "react"
import { ConfirmProvider, NasaqProvider, Toaster } from "@fadymondy/nasaq/web"

import { installDiagnostics } from "@/lib/feedback/core/diagnostics"
import { I18nProvider, type Dict } from "@/lib/i18n"
import type { Locale } from "@/lib/i18n-config"

const LOCALE_OPTIONS = [
  { value: "en", label: "English" },
  { value: "ar", label: "العربية", dir: "rtl" },
] as const

/** The locale lives in the URL: choosing one from the account menu reloads the same page under it. */
function switchLocale(next: string) {
  const { pathname, search, hash } = window.location
  const rest = pathname.replace(/^\/(en|ar)(?=\/|$)/, "")
  window.location.assign(`/${next}${rest}${search}${hash}`)
}

export function Providers({ locale, dict, children }: { locale: Locale; dict: Dict; children: ReactNode }) {
  // The feedback SDK records console + network from mount; a report filed after
  // something breaks cannot ask the browser for what already happened.
  useEffect(() => {
    installDiagnostics()
  }, [])

  return (
    <I18nProvider locale={locale} dict={dict}>
      {/* The locale is owned by the URL (/en, /ar), so it is passed in; the theme is Nasaq's own. */}
      <NasaqProvider brand="hosbah" defaultTheme="system" locale={locale} locales={LOCALE_OPTIONS} onLocaleChange={switchLocale} density="compact" expression="native">
        <ConfirmProvider>{children}</ConfirmProvider>
        <Toaster />
      </NasaqProvider>
    </I18nProvider>
  )
}
