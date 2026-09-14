"use client"

import Link from "next/link"
import { useEffect, useState } from "react"
import { ServerIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Skeleton } from "@/components/ui/skeleton"
import { apiFetch, type Member } from "@/lib/api"
import { useDocumentTitle, useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

export default function OverviewPage() {
  const { t, locale } = useI18n()
  const { team } = useTeam()
  useDocumentTitle(`${t("nav.overview")} — ${team.name}`)
  const [count, setCount] = useState<number | null | "error">(null)

  useEffect(() => {
    apiFetch<{ members: Member[] }>(`/api/teams/${team.slug}/members`)
      .then((r) => setCount(r.members?.length ?? 0))
      .catch(() => setCount("error"))
  }, [team.slug])

  const base = `/${locale}/t/${team.slug}`

  return (
    <PageBody title={team.name} description={t("overview.description")}>
      <dl className="grid-cells grid sm:grid-cols-3">
        <div className="flex flex-col gap-1 p-4">
          <dt className="grid-micro">{t("overview.yourRole")}</dt>
          <dd>
            <Badge variant="secondary">{t(`roles.${team.role}`)}</Badge>
          </dd>
        </div>
        <div className="flex flex-col gap-1 p-4">
          <dt className="grid-micro">{t("overview.members")}</dt>
          <dd className="font-mono text-2xl">
            {count === null ? <Skeleton className="h-7 w-10" /> : count === "error" ? "—" : count}
          </dd>
        </div>
        <div className="flex flex-col gap-1 p-4">
          <dt className="grid-micro">{t("overview.slug")}</dt>
          <dd dir="ltr" className="text-start font-mono text-sm">
            {team.slug}
          </dd>
        </div>
      </dl>

      <Empty className="border">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <ServerIcon />
          </EmptyMedia>
          <EmptyTitle>{t("overview.serversTitle")}</EmptyTitle>
          <EmptyDescription>{t("overview.serversHint")}</EmptyDescription>
        </EmptyHeader>
      </Empty>

      <div className="flex flex-wrap gap-2">
        <Button variant="outline" size="sm" nativeButton={false} render={<Link href={`${base}/members`} />}>
          {t("nav.members")}
        </Button>
        <Button variant="outline" size="sm" nativeButton={false} render={<Link href={`${base}/tokens`} />}>
          {t("nav.tokens")}
        </Button>
      </div>
    </PageBody>
  )
}
