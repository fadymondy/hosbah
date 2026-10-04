"use client"

import { EmptyState, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@fadymondy/nasaq/web"
import Link from "next/link"
import { useCallback, useEffect, useMemo, useState } from "react"
import { ArchiveIcon, DatabaseIcon, HardDriveIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { useErrorText } from "@/components/form-error"
import { useTimeAgo } from "@/components/home/sparkline"
import { appTone, FilterPills, ListBar, SortSelect, StatusDot } from "@/components/lists/list-bar"
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

/**
 * Every database service of the team, on whichever server: engine, status, where it runs, its
 * endpoint and whether it is backed up. MinIO lives under Storage. A row opens the database.
 */
export default function DatabasesPage() {
  const { t, locale } = useI18n()
  const { team, manage } = useTeam()
  useDocumentTitle(`${t("nav.databases")} — ${team.name}`)
  const errorText = useErrorText()
  const ago = useTimeAgo(locale)
  const base = `/${locale}/t/${team.slug}`

  const [all, setAll] = useState<DataService[] | null>(null)
  const [servers, setServers] = useState<Server[]>([])
  const [error, setError] = useState<string | null>(null)
  const [q, setQ] = useState("")
  const [kind, setKind] = useState("")
  const [server, setServer] = useState("")

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
  const serverName = (id: string) => servers.find((s) => s.id === id)?.name ?? "—"
  const kinds = useMemo(() => [...new Set(services.map((s) => s.kind))].sort(), [services])
  const onServers = useMemo(() => [...new Set(services.map((s) => s.server_id))], [services])

  const shown = useMemo(() => {
    const needle = q.trim().toLowerCase()
    return services.filter(
      (s) =>
        (!kind || s.kind === kind) &&
        (!server || s.server_id === server) &&
        (!needle || [s.name, s.kind, s.version, s.host, serverName(s.server_id)].filter(Boolean).some((v) => (v as string).toLowerCase().includes(needle))),
    )
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [services, q, kind, server, servers])

  const unprotected = services.filter((s) => s.source !== "imported" && !s.backups?.last_ok_at && !s.backups?.schedule).length
  const create =
    manage && servers.length > 0 ? <CreateDialog teamSlug={team.slug} serverId={servers[0].id} onCreated={load} /> : null

  return (
    <PageBody
      title={t("nav.databases")}
      description={all ? t("dbs2.lede", { n: services.length, servers: onServers.length }) : undefined}
      actions={create}
      bleed
    >
      <ErrorBand message={error} />
      {all === null ? (
        <LoadingRows rows={4} />
      ) : services.length === 0 ? (
        <EmptyState
          className="border-b"
          icon={DatabaseIcon}
          title={t("dbs2.emptyTitle")}
          description={servers.length ? t("dbs2.emptyBody") : t("dbs2.emptyNoServer")}
          actions={create}
        />
      ) : (
        <>
          {unprotected > 0 ? (
            <p className="flex items-center gap-2 border-b border-line bg-grid-soft px-6 py-2.5 text-sm text-grid-body">
              <ArchiveIcon className="size-4 shrink-0 text-grid-muted" />
              {t("dbs2.unprotected", { n: unprotected })}
              <Link href={`${base}/backups`} className="ms-auto shrink-0 font-medium text-grid-action hover:underline">
                {t("nav.backups")}
              </Link>
            </p>
          ) : null}
          <ListBar q={q} onQ={setQ} placeholder={t("lists.searchDatabases")}>
            {kinds.length > 1 ? (
              <FilterPills
                label={t("databases.kind")}
                value={kind}
                onChange={setKind}
                options={[["", t("home.filterAll"), services.length], ...kinds.map((k) => [k, k, services.filter((s) => s.kind === k).length] as [string, string, number])]}
              />
            ) : null}
            {onServers.length > 1 ? (
              <SortSelect
                label={t("nav.servers")}
                value={server}
                onChange={setServer}
                options={[["", t("apps2.allServers")], ...onServers.map((id) => [id, serverName(id)] as [string, string])]}
              />
            ) : null}
          </ListBar>

          {shown.length === 0 ? (
            <p className="border-b border-line px-6 py-8 text-center text-sm text-grid-muted">{t("dbs2.noMatch")}</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="ps-6">{t("databases.name")}</TableHead>
                  <TableHead>{t("databases.status")}</TableHead>
                  <TableHead className="max-md:hidden">{t("databases.server")}</TableHead>
                  <TableHead className="max-lg:hidden">{t("databases.endpoint")}</TableHead>
                  <TableHead className="pe-6">{t("nav.backups")}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {shown.map((s) => {
                  const st = svcState(s)
                  const b = s.backups
                  return (
                    <TableRow key={s.id} className="group">
                      <TableCell className="ps-6">
                        <Link href={`${base}/databases/${s.id}`} className="flex min-w-0 items-center gap-3">
                          <span className="flex size-8 shrink-0 items-center justify-center border border-line bg-grid-soft text-grid-muted">
                            <DatabaseIcon className="size-4" />
                          </span>
                          <span className="min-w-0">
                            <span dir="ltr" className="block truncate font-medium text-grid-fg group-hover:underline">
                              {s.name}
                            </span>
                            <span dir="ltr" className="block truncate text-xs text-grid-muted">
                              {s.kind} {s.version}
                              {s.source === "imported" ? ` · ${t("apps2.imported")}` : ""}
                            </span>
                          </span>
                        </Link>
                      </TableCell>
                      <TableCell>
                        <StatusDot tone={appTone(st)} label={t(`databases.statuses.${st}`, undefined, st)} />
                      </TableCell>
                      <TableCell className="max-md:hidden">
                        <Link href={`${base}/servers/${s.server_id}/databases`} dir="ltr" className="text-sm text-grid-body hover:text-grid-fg hover:underline">
                          {serverName(s.server_id)}
                        </Link>
                      </TableCell>
                      <TableCell className="max-lg:hidden">
                        <code dir="ltr" className="text-xs text-grid-muted">
                          {s.host && s.port ? `${s.host}:${s.port}` : "—"}
                        </code>
                      </TableCell>
                      <TableCell className="pe-6 text-xs">
                        {b?.last_status === "failed" ? (
                          <span className="block text-grid-danger">{t("dbs2.backupFailed", { when: ago(b.last_at!) })}</span>
                        ) : b?.last_status === "running" || b?.last_status === "pending" ? (
                          <StatusDot tone="progress" label={t("dbs2.backupRunning")} />
                        ) : null}
                        {b?.last_ok_at ? (
                          <span className="block text-grid-body">
                            {t("dbs2.lastGood", { when: ago(b.last_ok_at) })}
                            {b.last_ok_size ? <span className="text-grid-muted"> · {humanBytes(b.last_ok_size)}</span> : null}
                          </span>
                        ) : b?.last_status !== "failed" ? (
                          <span className="block text-grid-muted">{s.source === "imported" ? "—" : t("dbs2.never")}</span>
                        ) : null}
                        <span dir="auto" className="block text-grid-muted">
                          {b?.schedule ? (b.schedule_paused ? t("dbs2.schedulePaused") : t("dbs2.scheduled", { cron: b.schedule })) : null}
                        </span>
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          )}
          <div className="flex flex-wrap items-center justify-between gap-2 px-6 py-3 text-xs text-grid-muted">
            <span>{t("lists.count", { shown: shown.length, total: services.length })}</span>
            {storageCount > 0 ? (
              <Link href={`${base}/storage`} className="flex items-center gap-1.5 hover:text-grid-fg hover:underline">
                <HardDriveIcon className="size-3.5" />
                {t("dbs2.storageHint", { n: storageCount })}
              </Link>
            ) : null}
          </div>
        </>
      )}
    </PageBody>
  )
}
