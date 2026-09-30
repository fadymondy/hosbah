"use client"

import { AuthLayout, ProductLogo } from "@fadymondy/nasaq/web"
import Link from "next/link"
import type { ReactNode } from "react"

import { LanguageMenu, ThemeToggle } from "@/components/preferences"
import { useI18n } from "@/lib/i18n"

/**
 * Every signed-out page (sign-in, register, invitation, onboarding, 404): Nasaq's AuthLayout
 * (mark, h1, description, the form, a footer slot). The language and theme controls sit in the
 * footer; `eyebrow` is kept for callers and shown above the title.
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
    <AuthLayout
      mark={
        <Link href={`/${locale}`} aria-label={t("common.brand")}>
          <ProductLogo brand="hosbah" size={28} />
        </Link>
      }
      title={title}
      description={description}
      footer={
        <div className="flex flex-col gap-4">
          {footer ? <div className="text-body-sm text-muted-foreground">{footer}</div> : null}
          <div className="flex flex-wrap items-center justify-between gap-2 border-t border-border pt-3">
            <span className="eyebrow">{eyebrow ?? t("auth.eyebrow")}</span>
            <div className="flex items-center gap-1">
              <LanguageMenu />
              <ThemeToggle />
            </div>
          </div>
          <p className="text-caption text-muted-foreground">
            {t("auth.footerTagline")} · {t("auth.devNotice")}
          </p>
        </div>
      }
    >
      {children}
    </AuthLayout>
  )
}
