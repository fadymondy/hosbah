"use client"

import Link from "next/link"
import { useCallback, useEffect, useState } from "react"
import { BoxesIcon, PlusIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { AppStatusBadge, DeploymentStatusBadge } from "@/components/app-status"
import { ImportedBadge, originSummary } from "@/components/imported"
import { useErrorText } from "@/components/form-error"
import { ErrorBand, LoadingRows, TableBand } from "@/components/states"
import { Button } from "@/components/ui/button"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { apiFetch } from "@/lib/api"
import { isImported, runtimeLabel, shortSha, type App } from "@/lib/apps"
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
    <Button nativeButton={false} render={<Link href={`${base}/apps/new`} />}>
      <PlusIcon />
      {t("apps.add")}
    </Button>
  ) : null

  return (
    <PageBody title={t("nav.apps")} description={t("apps.description")} actions={add} bleed>
      <ErrorBand message={error} />
      {apps === null ? (
        <LoadingRows />
      ) : apps.length === 0 ? (
        <Empty className="border-b">
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
        <TableBand>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("apps.name")}</TableHead>
                <TableHead>{t("apps.status")}</TableHead>
                <TableHead>{t("apps.server")}</TableHead>
                <TableHead>{t("apps.runtime")}</TableHead>
                <TableHead>{t("apps.latestDeploy")}</TableHead>
                <TableHead>{t("apps.domainsLabel")}</TableHead>
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
                        {isImported(a) ? originSummary(a, t) : a.branch}
                      </div>
                    </TableCell>
                    <TableCell>
                      {isImported(a) ? <ImportedBadge item={a} /> : <AppStatusBadge status={a.status} />}
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
                      {isImported(a) ? (
                        <span className="text-xs text-muted-foreground">{t("imported.notDeployed")}</span>
                      ) : d ? (
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
        </TableBand>
      )}
    </PageBody>
  )
}
