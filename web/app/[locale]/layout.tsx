import type { Metadata } from "next"
import { notFound } from "next/navigation"
import { Suspense, type ReactNode } from "react"

import "../globals.css"
import { Providers } from "@/components/providers"
import en from "@/lang/en.json"
import ar from "@/lang/ar.json"
import { dirFor, isLocale, LOCALES } from "@/lib/i18n-config"

export const metadata: Metadata = {
  title: { default: "Hosbah", template: "%s · Hosbah" },
  description: "Hosbah — servers and apps, managed.",
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
        </Providers>
      </body>
    </html>
  )
}
