"use client"

import { Button, EmptyState } from "@fadymondy/nasaq/web"
import Link from "next/link"
import { useParams } from "next/navigation"
import type { ReactNode } from "react"

import { AppShell } from "@/components/app-shell"
import { BrandLoader } from "@/components/brand-loader"
import { PageLoading } from "@/components/page-loading"
import { useI18n } from "@/lib/i18n"
import { SessionGate } from "@/lib/session"
import { TeamScope } from "@/lib/team"

/**
 * The signed-in console: /account, /admin and every /t/{team}. One shell for all of them, so moving
 * between areas or switching teams keeps the sidebar, header and event stream mounted and only swaps
 * the page, instead of rebuilding the whole app behind a full-screen loader.
 */
export default function ConsoleLayout({ children }: { children: ReactNode }) {
  const { team } = useParams<{ team?: string }>()
  const { t, locale } = useI18n()
  return (
    <SessionGate fallback={<PageLoading />}>
      <TeamScope
        slug={team ?? null}
        shell={(body) => <AppShell>{body}</AppShell>}
        loading={<BodyLoading />}
        notFound={
          <EmptyState className="flex-1 border-y" title={t("errors.teamNotFound")} description={t("errors.teamNotFoundHint")} actions={<><Button variant="primary" nativeButton={false} render={<Link href={`/${locale}`} />}>
              {t("common.home")}
            </Button></>} />
        }
        failed={(message, retry) => (
          <EmptyState
            className="flex-1 border-y"
            title={t("errors.apiUnavailable")}
            description={<span dir="ltr">{message}</span>}
            actions={<Button variant="primary" onClick={retry}>{t("common.retry")}</Button>}
          />
        )}
      >
        {children}
      </TeamScope>
    </SessionGate>
  )
}

function BodyLoading() {
  const { t } = useI18n()
  return (
    <div className="flex flex-1 items-center justify-center py-24" role="status" aria-live="polite">
      <BrandLoader size={48} />
      <span className="sr-only">{t("common.loading")}</span>
    </div>
  )
}
