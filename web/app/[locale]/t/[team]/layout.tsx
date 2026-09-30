"use client"

import { Button, EmptyState } from "@fadymondy/nasaq/web"
import Link from "next/link"
import { useParams } from "next/navigation"
import type { ReactNode } from "react"

import { AppShell } from "@/components/app-shell"
import { PageLoading } from "@/components/page-loading"


import { useI18n } from "@/lib/i18n"
import { SessionGate } from "@/lib/session"
import { TeamGate, useTeam } from "@/lib/team"

export default function TeamLayout({ children }: { children: ReactNode }) {
  const { team } = useParams<{ team: string }>()
  const { t, locale } = useI18n()
  return (
    <SessionGate fallback={<PageLoading />}>
      <TeamGate
        slug={team}
        loading={<PageLoading />}
        notFound={
          <EmptyState className="min-h-dvh" title={t("errors.teamNotFound")} description={t("errors.teamNotFoundHint")} actions={<><Button variant="primary" nativeButton={false} render={<Link href={`/${locale}`} />}>
              {t("common.home")}
            </Button></>} />
        }
        failed={(message, retry) => (
          <EmptyState
            className="min-h-dvh"
            title={t("errors.apiUnavailable")}
            description={<span dir="ltr">{message}</span>}
            actions={<Button variant="primary" onClick={retry}>{t("common.retry")}</Button>}
          />
        )}
      >
        <TeamShell>{children}</TeamShell>
      </TeamGate>
    </SessionGate>
  )
}

function TeamShell({ children }: { children: ReactNode }) {
  const { team } = useTeam()
  return <AppShell title={team.name}>{children}</AppShell>
}
