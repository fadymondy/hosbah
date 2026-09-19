"use client"

import Link from "next/link"
import type { ReactNode } from "react"

import { ThemedCubeMark } from "@/components/cube-mark"
import { LanguageMenu, ThemeToggle } from "@/components/preferences"
import { useI18n } from "@/lib/i18n"

/**
 * Every signed-out page (sign-in, register, invitation, onboarding, 404), in the fadymondy.com
 * /en/login anatomy (web/components/layout/page-shell.tsx there): a full-width site header, an
 * eyebrow / title / description band between full-bleed hairlines, the form centred at 384px inside
 * the 860px column — no floating card — and the column's rules running down to a real footer.
 * At phone widths the same structure simply goes full width.
 */
export function AuthFrame({
  eyebrow,
  title,
  description,
  children,
  footer,
}: {
  eyebrow?: string
  title: string
  description?: ReactNode
  children: ReactNode
  footer?: ReactNode
}) {
  const { t, locale } = useI18n()
  return (
    <div className="relative isolate flex min-h-dvh flex-col">
      <header className="sticky top-0 z-50 max-w-screen overflow-x-clip bg-background px-2">
        <div className="grid-screen-line-top grid-screen-line-bottom mx-auto flex h-14 max-w-[860px] items-center gap-2 border-x ps-4 pe-2 sm:gap-4">
          <Link href={`/${locale}`} className="flex items-center gap-2.5" aria-label={t("common.brand")}>
            <ThemedCubeMark size={22} />
            <span className="text-[15px] font-medium">{t("common.brand")}</span>
          </Link>
          <div className="flex-1" />
          <div className="flex items-center gap-1">
            <LanguageMenu />
            <ThemeToggle />
          </div>
        </div>
      </header>

      <main className="flex max-w-screen flex-1 flex-col overflow-x-clip px-2">
        <div className="mx-auto flex w-full max-w-[860px] flex-1 flex-col">
          <div className="h-10 border-x" />
          <div className="border-x" data-slot="page-heading">
            <div className="px-4 pb-2 font-mono text-sm/none tracking-wider text-muted-foreground">
              {eyebrow ?? t("auth.eyebrow")}
            </div>
            <h1 className="grid-screen-line-top grid-screen-line-bottom px-4 text-3xl font-medium tracking-tight text-balance">
              {title}
            </h1>
            {description ? (
              <>
                <p className="max-w-[64ch] p-4 text-base text-pretty text-muted-foreground">{description}</p>
                <div className="grid-screen-line-bottom h-px" />
              </>
            ) : null}
          </div>

          <section className="grid-screen-line-bottom border-x">
            <div className="flex justify-center px-4 py-10">
              <div className="flex w-full max-w-sm flex-col gap-6">
                {children}
                {footer ? <div className="text-sm text-muted-foreground">{footer}</div> : null}
              </div>
            </div>
          </section>

          {/* Keeps the column's vertical rules running down to the footer. */}
          <div className="min-h-10 flex-1 border-x" />
        </div>
      </main>

      <footer className="max-w-screen overflow-x-clip px-2">
        <div className="mx-auto max-w-[860px] border-x">
          <div className="grid-screen-line-top grid-screen-line-bottom">
            <div className="grid-hatch h-12" aria-hidden />
          </div>
          <div className="grid-screen-line-bottom flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1 px-4 py-3 font-mono text-sm">
            <span className="font-medium">{t("common.brand")}</span>
            <span className="font-sans text-muted-foreground">{t("auth.footerTagline")}</span>
          </div>
          <dl className="grid grid-cols-1 font-mono text-xs sm:grid-cols-3">
            <div className="flex flex-col gap-1 border-b p-4 sm:border-e sm:border-b-0">
              <dt className="text-muted-foreground">{t("auth.footerStatus")}</dt>
              <dd className="font-sans text-sm">{t("auth.devNotice")}</dd>
            </div>
            <div className="flex flex-col gap-1 border-b p-4 sm:border-e sm:border-b-0">
              <dt className="text-muted-foreground">{t("auth.footerBy")}</dt>
              <dd dir="ltr" className="text-start">
                <a href="https://fadymondy.com" className="underline-offset-4 hover:underline">
                  fadymondy.com
                </a>
              </dd>
            </div>
            <div className="flex flex-col gap-1 p-4">
              <dt className="text-muted-foreground">{t("auth.footerProduct")}</dt>
              <dd className="font-sans text-sm">{t("auth.footerProductValue")}</dd>
            </div>
          </dl>
        </div>
        <div className="h-4" />
      </footer>
    </div>
  )
}
