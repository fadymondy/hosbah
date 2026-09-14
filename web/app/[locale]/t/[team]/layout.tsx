"use client"

import Link from "next/link"
import { useParams } from "next/navigation"
import type { ReactNode } from "react"

import { AppShell } from "@/components/app-shell"
import { PageLoading } from "@/components/page-loading"
import { Button } from "@/components/ui/button"
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty"
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
          <Empty className="min-h-dvh">
            <EmptyHeader>
              <EmptyTitle>{t("errors.teamNotFound")}</EmptyTitle>
              <EmptyDescription>{t("errors.teamNotFoundHint")}</EmptyDescription>
            </EmptyHeader>
            <Button nativeButton={false} render={<Link href={`/${locale}`} />}>
              {t("common.home")}
            </Button>
          </Empty>
        }
        failed={(message, retry) => (
          <Empty className="min-h-dvh">
            <EmptyHeader>
              <EmptyTitle>{t("errors.apiUnavailable")}</EmptyTitle>
              <EmptyDescription dir="ltr">{message}</EmptyDescription>
            </EmptyHeader>
            <Button onClick={retry}>{t("common.retry")}</Button>
          </Empty>
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
