"use client"

import { useEffect, type ReactNode } from "react"
import { ThemeProvider } from "next-themes"

import { Toaster } from "@/components/ui/sonner"
import { TooltipProvider } from "@/components/ui/tooltip"
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
      <ThemeProvider attribute="class" defaultTheme="system" enableSystem disableTransitionOnChange>
        <TooltipProvider>
          {children}
          <Toaster position={locale === "ar" ? "top-left" : "top-right"} dir={locale === "ar" ? "rtl" : "ltr"} />
        </TooltipProvider>
      </ThemeProvider>
    </I18nProvider>
  )
}
