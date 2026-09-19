import type { Metadata, Viewport } from "next"
import { notFound } from "next/navigation"
import { Suspense, type ReactNode } from "react"

import "../globals.css"
import { MahaamFeedback } from "@/components/feedback/mahaam-feedback"
import { Providers } from "@/components/providers"
import { RegisterServiceWorker } from "@/components/pwa"
import en from "@/lang/en.json"
import ar from "@/lang/ar.json"
import { dirFor, isLocale, LOCALES } from "@/lib/i18n-config"

export async function generateMetadata({ params }: { params: Promise<{ locale: string }> }): Promise<Metadata> {
  const { locale } = await params
  const brand = locale === "ar" ? ar.common.brand : en.common.brand
  return {
    title: { default: brand, template: `%s · ${brand}` },
    description: locale === "ar" ? "حوسبة — خوادمك وتطبيقاتك، مُدارة." : "Hosbah — servers and apps, managed.",
    applicationName: brand,
    // Installable (PWA): app/manifest.ts, icons from scripts/make-icons.mjs, sw.js for offline.
    manifest: "/manifest.webmanifest",
    appleWebApp: { capable: true, title: brand, statusBarStyle: "black-translucent" },
    formatDetection: { telephone: false },
  }
}

/** The browser/OS chrome follows the page's ground: paper in light, navy in dark. */
export const viewport: Viewport = {
  themeColor: [
    { media: "(prefers-color-scheme: light)", color: "#f0ebe1" },
    { media: "(prefers-color-scheme: dark)", color: "#0b1429" },
  ],
  viewportFit: "cover",
}

export function generateStaticParams() {
  return LOCALES.map((locale) => ({ locale }))
}

export default async function RootLayout({
  children,
  params,
}: {
  children: ReactNode
  params: Promise<{ locale: string }>
}) {
  const { locale } = await params
  if (!isLocale(locale)) notFound()

  return (
    <html
      lang={locale}
      dir={dirFor(locale)}
      data-brand="cloudy"
      className="grid-surface"
      suppressHydrationWarning
    >
      <body className="antialiased">
        <Providers locale={locale} dict={locale === "ar" ? ar : en}>
          {/* useSearchParams (language switch, ?next=) needs a boundary for static rendering. */}
          <Suspense>{children}</Suspense>
          {/* Mahaam Feedback SDK: its floating launcher on every page (no-op when not configured). */}
          <MahaamFeedback />
          <RegisterServiceWorker />
        </Providers>
      </body>
    </html>
  )
}
