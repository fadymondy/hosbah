"use client"

import { useEffect, type ReactNode } from "react"
import { NasaqProvider, Toaster } from "@fadymondy/nasaq/web"

import { installDiagnostics } from "@/lib/feedback/core/diagnostics"
import { I18nProvider, type Dict } from "@/lib/i18n"
import type { Locale } from "@/lib/i18n-config"

export function Providers({ locale, dict, children }: { locale: Locale; dict: Dict; children: ReactNode }) {
  // The feedback SDK records console + network from mount; a report filed after
  // something breaks cannot ask the browser for what already happened.
  useEffect(() => {
    installDiagnostics()
  }, [])

  return (
    <I18nProvider locale={locale} dict={dict}>
      {/* The locale is owned by the URL (/en, /ar), so it is passed in; the theme is Nasaq's own. */}
      <NasaqProvider brand="hosbah" defaultTheme="system" locale={locale} density="compact">
        {children}
        <Toaster />
      </NasaqProvider>
    </I18nProvider>
  )
}
