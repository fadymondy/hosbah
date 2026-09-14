"use client"

import Link from "next/link"
import { useCallback, useEffect, useState } from "react"
import { PlusIcon, ServerIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { FormError, useErrorText } from "@/components/form-error"
import { PresenceBadge } from "@/components/server-status"
import { Button } from "@/components/ui/button"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Skeleton } from "@/components/ui/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { apiFetch } from "@/lib/api"
import { useDocumentTitle, useFormatDate, useI18n } from "@/lib/i18n"
import { formatBytes, type Server } from "@/lib/servers"
import { useTeam } from "@/lib/team"

export default function ServersPage() {
  const { t, locale } = useI18n()
  const { team, manage } = useTeam()
  useDocumentTitle(`${t("nav.servers")} — ${team.name}`)
  const fmt = useFormatDate()
  const errorText = useErrorText()
  const [servers, setServers] = useState<Server[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const base = `/${locale}/t/${team.slug}`

  const load = useCallback(async () => {
    try {
      const res = await apiFetch<{ servers: Server[] }>(`/api/teams/${team.slug}/servers`)
      setServers(res.servers ?? [])
      setError(null)
    } catch (err) {
      setError(errorText(err))
      setServers((s) => s ?? [])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [team.slug])

  useEffect(() => {
    void load()
    const id = setInterval(load, 10_000) // heartbeats are every 15 s; the detail page is live
    return () => clearInterval(id)
  }, [load])

  const add = manage ? (
    <Button size="sm" nativeButton={false} render={<Link href={`${base}/servers/new`} />}>
      <PlusIcon />
      {t("servers.add")}
    </Button>
  ) : null

  return (
    <PageBody title={t("nav.servers")} description={t("servers.description")} actions={add}>
      <FormError message={error} />
      {servers === null ? (
        <div className="flex flex-col gap-2">
          <Skeleton className="h-8 w-full" />
          <Skeleton className="h-8 w-full" />
        </div>
      ) : servers.length === 0 ? (
        <Empty className="border">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <ServerIcon />
            </EmptyMedia>
            <EmptyTitle>{t("servers.emptyTitle")}</EmptyTitle>
            <EmptyDescription>{manage ? t("servers.emptyHint") : t("common.readOnly")}</EmptyDescription>
          </EmptyHeader>
          {add}
        </Empty>
      ) : (
        <div className="border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("servers.name")}</TableHead>
                <TableHead>{t("servers.status")}</TableHead>
                <TableHead>{t("servers.address")}</TableHead>
                <TableHead>{t("servers.os")}</TableHead>
                <TableHead>{t("servers.cpu")}</TableHead>
                <TableHead>{t("servers.memory")}</TableHead>
                <TableHead>{t("servers.agent")}</TableHead>
                <TableHead>{t("servers.lastSeen")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {servers.map((s) => (
                <TableRow key={s.id} data-server={s.name}>
                  <TableCell>
                    <Link href={`${base}/servers/${s.id}`} className="font-medium underline-offset-4 hover:underline" dir="ltr">
                      {s.name}
                    </Link>
                    <div className="text-xs text-muted-foreground">{t(`servers.providers.${s.provider}`)}</div>
                  </TableCell>
                  <TableCell>
                    <PresenceBadge server={s} />
                  </TableCell>
                  <TableCell dir="ltr" className="text-start font-mono text-xs">
                    {s.ip_address ?? "—"}
                  </TableCell>
                  <TableCell dir="ltr" className="text-start">
                    {s.os ? `${s.os} ${s.os_version ?? ""}` : "—"}
                  </TableCell>
                  <TableCell dir="ltr" className="text-start font-mono text-xs tabular-nums">
                    {s.metrics ? `${s.metrics.cpu_percent.toFixed(1)}%` : "—"}
                  </TableCell>
                  <TableCell dir="ltr" className="text-start font-mono text-xs tabular-nums">
                    {s.metrics ? `${formatBytes(s.metrics.mem_used_bytes)} / ${formatBytes(s.metrics.mem_total_bytes)}` : "—"}
                  </TableCell>
                  <TableCell dir="ltr" className="text-start font-mono text-xs">
                    {s.agent_version ?? "—"}
                  </TableCell>
                  <TableCell dir="ltr" className="text-start text-xs">
                    {s.last_seen_at ? fmt(s.last_seen_at) : t("servers.never")}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </PageBody>
  )
}
