"use client"

import {
  Alert,
  DataTable,
  DataTableFacetFilter,
  DataTablePagination,
  DataTableSearch,
  DataTableToolbar,
  EmptyState,
  Status,
  useDataTable,
  type DataTableColumn,
  type StatusTone,
} from "@fadymondy/nasaq/web"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { useCallback, useEffect, useMemo, useState } from "react"
import { DatabaseIcon, HardDriveIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { useErrorText } from "@/components/form-error"
import { useTimeAgo } from "@/components/home/sparkline"
import { CreateDialog } from "@/components/server-tabs/databases"
import { ErrorBand, LoadingRows } from "@/components/states"
import { apiFetch } from "@/lib/api"
import { humanBytes, type DataService } from "@/lib/databases"
import { useDocumentTitle, useI18n } from "@/lib/i18n"
import type { Server } from "@/lib/servers"
import { useTeam } from "@/lib/team"

/** The state shown for a service: an imported one reports its own. */
function svcState(s: DataService) {
  return s.source === "imported" ? (s.imported_state ?? "stopped") : s.status
}

function stateTone(st: string): StatusTone {
  if (st === "running") return "success"
  if (st === "creating" || st === "provisioning" || st === "queued") return "info"
  if (st === "failed" || st === "missing") return "danger"
  return "neutral"
}

/**
 * Every database service of the team, on whichever server, in Nasaq's server-tools table: engine,
 * status, where it runs, its endpoint and whether it is backed up. MinIO lives under Storage. A row
 * opens the database.
 */
export default function DatabasesPage() {
  const { t, locale } = useI18n()
  const { team, manage } = useTeam()
  useDocumentTitle(`${t("nav.databases")} — ${team.name}`)
  const errorText = useErrorText()
  const ago = useTimeAgo(locale)
  const router = useRouter()
  const base = `/${locale}/t/${team.slug}`

  const [all, setAll] = useState<DataService[] | null>(null)
  const [servers, setServers] = useState<Server[]>([])
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      const [d, s] = await Promise.all([
        apiFetch<{ services: DataService[] }>(`/api/teams/${team.slug}/data-services`),
        apiFetch<{ servers: Server[] }>(`/api/teams/${team.slug}/servers`).catch(() => ({ servers: [] as Server[] })),
      ])
      setAll(d.services ?? [])
      setServers(s.servers ?? [])
      setError(null)
    } catch (err) {
      setError(errorText(err))
      setAll((x) => x ?? [])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [team.slug])

  useEffect(() => {
    void load()
    const id = setInterval(load, 30_000)
    return () => clearInterval(id)
  }, [load])

  const services = useMemo(() => (all ?? []).filter((s) => s.kind !== "minio"), [all])
  const storageCount = (all ?? []).length - services.length
  const serverName = useCallback((id: string) => servers.find((s) => s.id === id)?.name ?? "—", [servers])
  const kinds = useMemo(() => [...new Set(services.map((s) => s.kind))].sort(), [services])
  const onServers = useMemo(() => [...new Set(services.map((s) => s.server_id))], [services])
  const states = useMemo(() => [...new Set(services.map(svcState))].sort(), [services])

  const columns = useMemo<DataTableColumn<DataService>[]>(
    () => [
      {
        id: "name",
        header: t("databases.name"),
        label: t("databases.name"),
        sortValue: (s) => s.name,
        searchValue: (s) => [s.name, s.kind, s.version, s.host, serverName(s.server_id)].filter(Boolean).join(" "),
        cell: (s) => (
          <span className="flex min-w-0 items-center gap-3">
            <span className="flex size-8 shrink-0 items-center justify-center rounded-control border border-border bg-secondary text-muted-foreground">
              <DatabaseIcon aria-hidden className="size-4" />
            </span>
            <span className="flex min-w-0 flex-col">
              <bdi dir="ltr" className="truncate text-start font-medium text-foreground">
                {s.name}
              </bdi>
              <span dir="ltr" className="truncate text-start text-caption text-muted-foreground">
                {s.kind} {s.version}
                {s.source === "imported" ? ` · ${t("apps2.imported")}` : ""}
              </span>
            </span>
          </span>
        ),
      },
      {
        id: "kind",
        header: t("databases.kind"),
        label: t("databases.kind"),
        filterValue: (s) => s.kind,
        cell: () => null,
        defaultHidden: true,
        hideable: false,
      },
      {
        id: "status",
        header: t("databases.status"),
        label: t("databases.status"),
        sortValue: (s) => svcState(s),
        filterValue: (s) => svcState(s),
        cell: (s) => {
          const st = svcState(s)
          return <Status tone={stateTone(st)}>{t(`databases.statuses.${st}`, undefined, st)}</Status>
        },
      },
      {
        id: "server",
        header: t("databases.server"),
        label: t("databases.server"),
        sortValue: (s) => serverName(s.server_id),
        filterValue: (s) => s.server_id,
        className: "max-md:hidden",
        headerClassName: "max-md:hidden",
        cell: (s) => (
          <bdi dir="ltr" className="text-body-sm text-foreground">
            {serverName(s.server_id)}
          </bdi>
        ),
      },
      {
        id: "endpoint",
        header: t("databases.endpoint"),
        label: t("databases.endpoint"),
        className: "max-lg:hidden",
        headerClassName: "max-lg:hidden",
        cell: (s) => (
          <code dir="ltr" className="font-mono text-code text-muted-foreground">
            {s.host && s.port ? `${s.host}:${s.port}` : "—"}
          </code>
        ),
      },
      {
        id: "backups",
        header: t("nav.backups"),
        label: t("nav.backups"),
        sortValue: (s) => s.backups?.last_at ?? s.created_at,
        cell: (s) => {
          const b = s.backups
          return (
            <span className="flex flex-col gap-0.5 text-caption">
              {b?.last_status === "failed" ? (
                <Status tone="danger">{t("dbs2.backupFailed", { when: ago(b.last_at!) })}</Status>
              ) : b?.last_status === "running" || b?.last_status === "pending" ? (
                <Status tone="info">{t("dbs2.backupRunning")}</Status>
              ) : null}
              {b?.last_ok_at ? (
                <span className="text-foreground">
                  {t("dbs2.lastGood", { when: ago(b.last_ok_at) })}
                  {b.last_ok_size ? <span className="text-muted-foreground"> · {humanBytes(b.last_ok_size)}</span> : null}
                </span>
              ) : b?.last_status !== "failed" ? (
                <span className="text-muted-foreground">{s.origin_kind === "guest" ? "—" : t("dbs2.never")}</span>
              ) : null}
              {b?.schedule ? (
                <span dir="auto" className="text-muted-foreground">
                  {b.schedule_paused ? t("dbs2.schedulePaused") : t("dbs2.scheduled", { cron: b.schedule })}
                </span>
              ) : null}
            </span>
          )
        },
      },
    ],
    [t, ago, serverName],
  )
  const table = useDataTable({
    data: services,
    columns,
    getRowId: (s) => s.id,
    pageSize: 20,
    defaultSort: { id: "backups", direction: "desc" },
  })

  const unprotected = services.filter((s) => s.origin_kind !== "guest" && !s.backups?.last_ok_at && !s.backups?.schedule).length
  const create = manage && servers.length > 0 ? <CreateDialog teamSlug={team.slug} serverId={servers[0].id} onCreated={load} kinds={["postgres", "mysql", "redis"]} /> : null

  return (
    <PageBody
      title={t("nav.databases")}
      description={all ? t("dbs2.lede", { n: services.length, servers: onServers.length }) : undefined}
      actions={create}
      bleed
      divider={false}
    >
      <ErrorBand message={error} />
      {all === null ? (
        <LoadingRows rows={4} />
      ) : services.length === 0 ? (
        <div className="px-6 pb-6">
          <EmptyState
            icon={DatabaseIcon}
            title={t("dbs2.emptyTitle")}
            description={servers.length ? t("dbs2.emptyBody") : t("dbs2.emptyNoServer")}
            actions={create}
          />
        </div>
      ) : (
        <div className="flex flex-col gap-3 px-6 pb-6">
          {unprotected > 0 ? (
            <Alert tone="warning">
              <span className="flex flex-wrap items-center gap-2">
                {t("dbs2.unprotected", { n: unprotected })}
                <Link href={`${base}/backups`} className="ms-auto font-medium underline-offset-4 hover:underline">
                  {t("nav.backups")}
                </Link>
              </span>
            </Alert>
          ) : null}
          <DataTableToolbar>
            <DataTableSearch table={table} placeholder={t("lists.searchDatabases")} />
            {kinds.length > 1 ? (
              <DataTableFacetFilter
                table={table}
                column="kind"
                title={t("databases.kind")}
                options={kinds.map((k) => ({ value: k, label: k }))}
              />
            ) : null}
            {states.length > 1 ? (
              <DataTableFacetFilter
                table={table}
                column="status"
                title={t("databases.status")}
                options={states.map((st) => ({ value: st, label: t(`databases.statuses.${st}`, undefined, st) }))}
              />
            ) : null}
            {onServers.length > 1 ? (
              <DataTableFacetFilter
                table={table}
                column="server"
                title={t("nav.servers")}
                options={onServers.map((id) => ({ value: id, label: serverName(id) }))}
              />
            ) : null}
          </DataTableToolbar>
          <DataTable
            table={table}
            label={t("nav.databases")}
            rowLabel={(s) => s.name}
            onRowClick={(s) => router.push(`${base}/databases/${s.id}`)}
            empty={t("dbs2.noMatch")}
          />
          <div className="flex flex-wrap items-center justify-between gap-2">
            <DataTablePagination table={table} />
            {storageCount > 0 ? (
              <Link
                href={`${base}/storage`}
                className="ms-auto flex items-center gap-1.5 text-caption text-muted-foreground hover:text-foreground hover:underline"
              >
                <HardDriveIcon aria-hidden className="size-3.5" />
                {t("dbs2.storageHint", { n: storageCount })}
              </Link>
            ) : null}
          </div>
        </div>
      )}
    </PageBody>
  )
}
