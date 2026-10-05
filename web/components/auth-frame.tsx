"use client"

import { AuthFooter, AuthLayout, ProductLogo } from "@fadymondy/nasaq/web"
import Link from "next/link"
import type { ReactNode } from "react"
import { DatabaseBackupIcon, RocketIcon, ServerIcon, UsersIcon } from "lucide-react"

import { LanguageMenu, ThemeToggle } from "@/components/preferences"
import { useI18n } from "@/lib/i18n"

const POINTS = [
  { key: "servers", icon: ServerIcon },
  { key: "deploys", icon: RocketIcon },
  { key: "backups", icon: DatabaseBackupIcon },
  { key: "panels", icon: UsersIcon },
] as const

/** The brand side of the split sign-in: logo, headline and what Hosbah does. Hidden below lg. */
function BrandPanel() {
  const { t, locale } = useI18n()
  return (
    <div className="relative flex flex-1 flex-col justify-between gap-10">
      <Link href={`/${locale}`} aria-label={t("common.brand")} className="w-fit">
        <ProductLogo brand="hosbah" size={32} />
      </Link>
      <div className="flex max-w-md flex-col gap-6">
        <p className="eyebrow">{t("auth.eyebrow")}</p>
        <h2 className="text-h1 text-foreground">{t("auth.split.headline")}</h2>
        <ul className="flex flex-col gap-4">
          {POINTS.map(({ key, icon: Icon }) => (
            <li key={key} className="flex items-start gap-3">
              <span className="mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-lg border border-border bg-background/70 text-nq-action [&_svg]:size-4">
                <Icon />
              </span>
              <span className="text-body-sm text-nq-fg-body">{t(`auth.split.points.${key}`)}</span>
            </li>
          ))}
        </ul>
      </div>
      <p className="text-caption text-muted-foreground">
        © {new Date().getFullYear()} Hosbah · {t("auth.devNotice")}
      </p>
    </div>
  )
}

/**
 * Every signed-out page (sign-in, register, invitation, onboarding, 404): Nasaq's split AuthLayout, the
 * brand panel on the inline start and the form on the other side. `footer` is the page's own prompt
 * ("No account? Register"); the language and theme controls sit in the AuthFooter.
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
      variant="split"
      panel={<BrandPanel />}
      mark={
        <Link href={`/${locale}`} aria-label={t("common.brand")} className="lg:hidden">
          <ProductLogo brand="hosbah" size={28} />
        </Link>
      }
      title={title}
      description={
        eyebrow ? (
          <>
            <span className="eyebrow block">{eyebrow}</span>
            {description}
          </>
        ) : (
          description
        )
      }
      prompt={footer}
      footer={
        <AuthFooter
          links={[]}
          end={
            <>
              <LanguageMenu />
              <ThemeToggle />
            </>
          }
        />
      }
    >
      {children}
    </AuthLayout>
  )
}
