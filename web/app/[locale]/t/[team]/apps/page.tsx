"use client"

import Link from "next/link"
import { useCallback, useEffect, useState } from "react"
import { BoxesIcon, PlusIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { AppStatusBadge, DeploymentStatusBadge } from "@/components/app-status"
import { FormError, useErrorText } from "@/components/form-error"
import { Button } from "@/components/ui/button"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Skeleton } from "@/components/ui/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { apiFetch } from "@/lib/api"
import { runtimeLabel, shortSha, type App } from "@/lib/apps"
import { useDocumentTitle, useFormatDate, useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

export default function AppsPage() {
  const { t, locale } = useI18n()
  const { team, manage } = useTeam()
  useDocumentTitle(`${t("nav.apps")} — ${team.name}`)
  const fmt = useFormatDate()
  const errorText = useErrorText()
  const [apps, setApps] = useState<App[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const base = `/${locale}/t/${team.slug}`

  const load = useCallback(async () => {
    try {
      const res = await apiFetch<{ apps: App[] }>(`/api/teams/${team.slug}/apps`)
      setApps(res.apps ?? [])
      setError(null)
    } catch (err) {
      setError(errorText(err))
      setApps((a) => a ?? [])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [team.slug])

  useEffect(() => {
    void load()
    const id = setInterval(load, 10_000) // the detail page is live; the list refreshes
    return () => clearInterval(id)
  }, [load])

  const add = manage ? (
    <Button size="sm" nativeButton={false} render={<Link href={`${base}/apps/new`} />}>
      <PlusIcon />
      {t("apps.add")}
    </Button>
  ) : null

  return (
    <PageBody title={t("nav.apps")} description={t("apps.description")} actions={add}>
      <FormError message={error} />
      {apps === null ? (
        <div className="flex flex-col gap-2">
          <Skeleton className="h-8 w-full" />
          <Skeleton className="h-8 w-full" />
        </div>
      ) : apps.length === 0 ? (
        <Empty className="border">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <BoxesIcon />
            </EmptyMedia>
            <EmptyTitle>{t("apps.emptyTitle")}</EmptyTitle>
            <EmptyDescription>{manage ? t("apps.emptyHint") : t("common.readOnly")}</EmptyDescription>
          </EmptyHeader>
          {add}
        </Empty>
      ) : (
        <div className="border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("apps.name")}</TableHead>
                <TableHead>{t("apps.status")}</TableHead>
                <TableHead>{t("apps.server")}</TableHead>
                <TableHead>{t("apps.runtime")}</TableHead>
                <TableHead>{t("apps.latestDeploy")}</TableHead>
                <TableHead>{t("apps.domains")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {apps.map((a) => {
                const d = a.latest_deployment
                return (
                  <TableRow key={a.id} data-app={a.name}>
                    <TableCell>
                      <Link
                        href={`${base}/apps/${a.name}`}
                        className="font-medium underline-offset-4 hover:underline"
                        dir="ltr"
                      >
                        {a.name}
                      </Link>
                      <div dir="ltr" className="max-w-64 truncate text-start font-mono text-xs text-muted-foreground">
                        {a.branch}
                      </div>
                    </TableCell>
                    <TableCell>
                      <AppStatusBadge status={a.status} />
                    </TableCell>
                    <TableCell dir="ltr" className="text-start font-mono text-xs">
                      {a.server_name ? (
                        <Link href={`${base}/servers/${a.server_id}`} className="underline-offset-4 hover:underline">
                          {a.server_name}
                        </Link>
                      ) : (
                        "—"
                      )}
                    </TableCell>
                    <TableCell dir="ltr" className="text-start font-mono text-xs">
                      {runtimeLabel(a.runtime) ?? "—"}
                    </TableCell>
                    <TableCell>
                      {d ? (
                        <div className="flex flex-col items-start gap-1">
                          <div className="flex items-center gap-2">
                            <span dir="ltr" className="font-mono text-xs">
                              #{d.number} · {shortSha(d.commit_sha)}
                            </span>
                            <DeploymentStatusBadge status={d.status} />
                          </div>
                          <span dir="ltr" className="text-xs text-muted-foreground">
                            {fmt(d.created_at)}
                          </span>
                        </div>
                      ) : (
                        <span className="text-xs text-muted-foreground">{t("apps.neverDeployed")}</span>
                      )}
                    </TableCell>
                    <TableCell dir="ltr" className="text-start font-mono text-xs">
                      {a.domains?.length ? a.domains.map((h) => <div key={h.id}>{h.hostname}</div>) : "—"}
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        </div>
      )}
    </PageBody>
  )
}
