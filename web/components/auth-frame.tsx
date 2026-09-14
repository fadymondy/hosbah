"use client"

import type { ReactNode } from "react"

import { ThemedCubeMark } from "@/components/cube-mark"
import { LanguageSwitch, ThemeToggle } from "@/components/preferences"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { useI18n } from "@/lib/i18n"

/**
 * The login-03 composition (centred column, mark above a Card) drawn on the grid: the column is
 * bounded by hairlines, with full-bleed rules above and below the card row.
 */
export function AuthFrame({
  title,
  description,
  children,
  footer,
}: {
  title: string
  description?: ReactNode
  children: ReactNode
  footer?: ReactNode
}) {
  const { t } = useI18n()
  return (
    <div className="flex min-h-dvh flex-col overflow-hidden">
      <header className="grid-rule-bottom">
        <div className="mx-auto flex h-14 max-w-md items-center justify-between border-x px-4">
          <span className="flex items-center gap-2 font-medium">
            <ThemedCubeMark size={20} />
            <span dir="ltr">Cloudy</span>
          </span>
          <div className="flex items-center gap-1">
            <LanguageSwitch />
            <ThemeToggle />
          </div>
        </div>
      </header>
      <div className="grid-hatch h-8 grid-rule-bottom" aria-hidden />
      <main className="flex flex-1 flex-col">
        <div className="mx-auto flex w-full max-w-md flex-1 flex-col gap-6 border-x px-4 py-10">
          <Card>
            <CardHeader className="text-center">
              <CardTitle className="text-xl">{title}</CardTitle>
              {description ? <CardDescription>{description}</CardDescription> : null}
            </CardHeader>
            <CardContent>{children}</CardContent>
          </Card>
          {footer ? <div className="text-center text-sm text-muted-foreground">{footer}</div> : null}
          <p className="grid-micro text-center">{t("auth.devNotice")}</p>
        </div>
      </main>
    </div>
  )
}
