"use client"

import {
  Button,
  DataTable,
  DataTableFacetFilter,
  DataTablePagination,
  DataTableSearch,
  DataTableToolbar,
  EmptyState,
  Status,
  useDataTable,
  type DataTableColumn,
  type DataTableRowAction,
  type StatusTone,
} from "@fadymondy/nasaq/web"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { useCallback, useEffect, useMemo, useState } from "react"
import { ArchiveIcon, DatabaseIcon, HardDriveDownloadIcon, SettingsIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { useErrorText } from "@/components/form-error"
import { useTimeAgo } from "@/components/home/sparkline"
import { ErrorBand, LoadingRows } from "@/components/states"
import { apiFetch } from "@/lib/api"
import { humanBytes, type DataService } from "@/lib/databases"
import { useDocumentTitle, useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

type Health = "failing" | "unprotected" | "running" | "ok" | "unsupported"

/** How a service stands for backups: the newest one failed, it has none and no schedule, … */
function health(s: DataService): Health {
  if (s.origin_kind === "guest") return "unsupported"
  const b = s.backups
  if (b?.last_status === "failed") return "failing"
  if (b?.last_status === "pending" || b?.last_status === "running") return "running"
  if (!b?.last_ok_at && !b?.schedule) return "unprotected"
  return b?.last_ok_at ? "ok" : "unprotected"
}

const TONE: Record<Health, StatusTone> = { failing: "danger", unprotected: "warning", running: "info", ok: "success", unsupported: "neutral" }
const ORDER: Record<Health, number> = { failing: 0, unprotected: 1, running: 2, ok: 3, unsupported: 4 }
const HEALTHS: Health[] = ["failing", "unprotected", "running", "ok", "unsupported"]

/**
 * Backups across every data service: what is failing, what has never been backed up, when each was
 * last backed up and on what schedule. Back up now runs from here; restore tests, history and the
 * schedule are on the service's own page.
 */
export default function BackupsPage() {
  const { t, locale } = useI18n()
  const { team, manage } = useTeam()
  useDocumentTitle(`${t("nav.backups")} — ${team.name}`)
  const errorText = useErrorText()
  const ago = useTimeAgo(locale)
  const router = useRouter()
  const base = `/${locale}/t/${team.slug}`

  const [services, setServices] = useState<DataService[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      const d = await apiFetch<{ services: DataService[] }>(`/api/teams/${team.slug}/data-services`)
      setServices(d.services ?? [])
      setError(null)
    } catch (err) {
      setError(errorText(err))
      setServices((x) => x ?? [])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [team.slug])

  const anyRunning = (services ?? []).some((s) => health(s) === "running")
  useEffect(() => {
    void load()
    const id = setInterval(load, anyRunning ? 5_000 : 30_000)
    return () => clearInterval(id)
  }, [load, anyRunning])

  const counts = useMemo(() => {
    const c: Record<Health, number> = { failing: 0, unprotected: 0, running: 0, ok: 0, unsupported: 0 }
    for (const s of services ?? []) c[health(s)]++
    return c
  }, [services])

  async function backupNow(s: DataService) {
    setBusy(s.id)
    try {
      await apiFetch(`/api/teams/${team.slug}/data-services/${s.id}/backups`, { method: "POST" })
      await load()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(null)
    }
  }

  const detail = (s: DataService) => `${base}/databases/${s.id}`

  const columns = useMemo<DataTableColumn<DataService>[]>(
    () => [
      {
        id: "name",
        header: t("databases.name"),
        label: t("databases.name"),
        sortValue: (s) => s.name,
        searchValue: (s) => `${s.name} ${s.kind}`,
        cell: (s) => (
          <span className="flex min-w-0 items-center gap-3">
            <DatabaseIcon className="size-4 shrink-0 text-muted-foreground" />
            <span className="min-w-0">
              <span dir="ltr" className="block truncate font-medium">
                {s.name}
              </span>
              <span dir="ltr" className="block truncate text-caption text-muted-foreground">
                {s.kind} {s.version}
              </span>
            </span>
          </span>
        ),
      },
      {
        id: "health",
        header: t("databases.status"),
        label: t("databases.status"),
        sortValue: (s) => ORDER[health(s)],
        filterValue: (s) => health(s),
        cell: (s) => {
          const h = health(s)
          const b = s.backups
          return (
            <span className="flex max-w-80 flex-col items-start gap-1">
              <Status tone={TONE[h]}>{t(`backups2.health.${h}`)}</Status>
              {h === "failing" && b?.last_error ? (
                <span dir="ltr" className="line-clamp-2 font-mono text-caption text-destructive">
                  {b.last_error}
                </span>
              ) : null}
              {b?.last_restore_status === "failed" ? (
                <span className="text-caption text-destructive">{t("backups2.restoreFailed")}</span>
              ) : null}
            </span>
          )
        },
      },
      {
        id: "last",
        header: t("backups2.lastGood"),
        label: t("backups2.lastGood"),
        sortValue: (s) => s.backups?.last_ok_at ?? "",
        className: "max-md:hidden",
        headerClassName: "max-md:hidden",
        cell: (s) => {
          const b = s.backups
          if (!b?.last_ok_at)
            return (
              <span className="text-caption text-muted-foreground">
                {health(s) === "unsupported" ? t("backups2.guestHint") : t("dbs2.never")}
              </span>
            )
          return (
            <span className="text-caption">
              <span className="block">{ago(b.last_ok_at)}</span>
              <span className="block text-muted-foreground">
                {b.last_ok_size ? `${humanBytes(b.last_ok_size)} · ` : null}
                {t("backups2.count", { n: b.count })}
              </span>
            </span>
          )
        },
      },
      {
        id: "schedule",
        header: t("backups2.schedule"),
        label: t("backups2.schedule"),
        sortValue: (s) => s.backups?.schedule ?? "",
        className: "max-lg:hidden",
        headerClassName: "max-lg:hidden",
        cell: (s) => {
          const b = s.backups
          if (!b?.schedule)
            return (
              <span className="text-caption text-muted-foreground">{health(s) === "unsupported" ? "—" : t("backups2.addSchedule")}</span>
            )
          return (
            <span className="text-caption">
              <code dir="ltr" className="block">
                {b.schedule}
              </code>
              <span className="block text-muted-foreground">
                {b.schedule_paused
                  ? t("dbs2.schedulePaused")
                  : b.next_run_at
                    ? t("backups2.next", { when: new Date(b.next_run_at).toLocaleString(locale) })
                    : null}
                {b.target ? ` · ${b.target}` : null}
              </span>
            </span>
          )
        },
      },
      {
        id: "activity",
        header: t("lists.sortActivity"),
        label: t("lists.sortActivity"),
        sortValue: (s) => s.backups?.last_at ?? "",
        defaultHidden: true,
        cell: (s) => <span className="text-caption text-muted-foreground">{s.backups?.last_at ? ago(s.backups.last_at) : "—"}</span>,
      },
    ],
    [t, ago, locale],
  )
  const table = useDataTable({
    data: services ?? [],
    columns,
    getRowId: (s) => s.id,
    pageSize: 20,
    defaultSort: { id: "health", direction: "asc" },
  })

  const actions = (s: DataService): DataTableRowAction[] => {
    const h = health(s)
    const out: DataTableRowAction[] = [{ id: "open", label: t("lists.manage"), icon: SettingsIcon, onSelect: () => router.push(detail(s)) }]
    if (manage && h !== "unsupported" && h !== "running" && s.status === "running")
      out.unshift({
        id: "backup",
        label: t("databases.backupNow"),
        icon: HardDriveDownloadIcon,
        disabled: busy === s.id,
        onSelect: () => void backupNow(s),
      })
    return out
  }

  return (
    <PageBody title={t("nav.backups")} description={t("backups2.lede")} bleed divider={false}>
      <ErrorBand message={error} />
      {services === null ? (
        <LoadingRows rows={4} />
      ) : services.length === 0 ? (
        <div className="px-6 pb-6">
          <EmptyState
            icon={ArchiveIcon}
            title={t("backups2.emptyTitle")}
            description={t("backups2.emptyBody")}
            actions={
              <Button variant="primary" nativeButton={false} render={<Link href={`${base}/databases`} />}>
                <DatabaseIcon />
                {t("nav.databases")}
              </Button>
            }
          />
        </div>
      ) : (
        <div className="flex flex-col gap-3 px-6 pb-6">
          <DataTableToolbar>
            <DataTableSearch table={table} placeholder={t("backups2.search")} />
            <DataTableFacetFilter
              table={table}
              column="health"
              title={t("databases.status")}
              options={HEALTHS.filter((h) => counts[h] > 0).map((h) => ({ value: h, label: `${t(`backups2.health.${h}`)} (${counts[h]})` }))}
            />
          </DataTableToolbar>
          <DataTable
            table={table}
            label={t("nav.backups")}
            rowLabel={(s) => s.name}
            rowActions={actions}
            onRowClick={(s) => router.push(detail(s))}
            empty={t("backups2.noMatch")}
          />
          <DataTablePagination table={table} />
        </div>
      )}
    </PageBody>
  )
}
