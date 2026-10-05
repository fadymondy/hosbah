"use client"

import {
  Badge,
  Button,
  DataTable,
  DataTableFacetFilter,
  DataTablePagination,
  DataTableSearch,
  DataTableToolbar,
  EmptyState,
  Switch,
  toast,
  useDataTable,
  type DataTableColumn,
  type DataTableRowAction,
} from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useMemo, useState } from "react"
import { CircleOffIcon, DatabaseIcon, EyeIcon, RefreshCwIcon, ServerIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { useErrorText } from "@/components/form-error"
import { ErrorBand, LoadingRows } from "@/components/states"
import { apiFetch } from "@/lib/api"
import { useDocumentTitle, useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

export const SHARED_SERVICE_KINDS = [
  "postgres",
  "mysql",
  "redis",
  "nats",
  "minio",
  "searxng",
  "firecrawl",
  "browser",
  "meilisearch",
  "rabbitmq",
  "elasticsearch",
  "opensearch",
  "clickhouse",
  "mongodb",
  "other",
] as const

export type SharedService = {
  id: string
  server_id: string
  server_name: string
  guest_vmid: string
  guest_type: "lxc" | "qemu"
  kind: string
  name: string
  host: string | null
  port: number | null
  hidden: boolean
  name_manual: boolean
  kind_manual: boolean
}

type Patch = Partial<Pick<SharedService, "name" | "kind" | "hidden">>

/** The team's directory of Proxmox guests classified as shared infrastructure. Name and kind edit in place. */
export default function SharedServicesPage() {
  const { t } = useI18n()
  const { team, manage } = useTeam()
  useDocumentTitle(`${t("nav.sharedServices")} — ${team.name}`)
  const errorText = useErrorText()
  const [services, setServices] = useState<SharedService[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [scanning, setScanning] = useState(false)
  const [showHidden, setShowHidden] = useState(false)

  const load = useCallback(async () => {
    try {
      const r = await apiFetch<{ services: SharedService[] }>(`/api/teams/${team.slug}/shared-services`)
      setServices(r.services ?? [])
    } catch (err) {
      setError(errorText(err))
      setServices([])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [team.slug])

  useEffect(() => {
    void load()
  }, [load])

  async function scan() {
    setScanning(true)
    setError(null)
    try {
      const r = await apiFetch<{ services: SharedService[] }>(`/api/teams/${team.slug}/shared-services/scan`, { method: "POST" })
      setServices(r.services ?? [])
      toast.success(t("sharedServices.scanned", { count: (r.services ?? []).length }))
    } catch (err) {
      setError(errorText(err))
    } finally {
      setScanning(false)
    }
  }

  async function update(s: SharedService, patch: Patch): Promise<{ error: string } | void> {
    try {
      const updated = await apiFetch<SharedService>(`/api/teams/${team.slug}/shared-services/${s.id}`, { method: "PUT", body: patch })
      setServices((cur) => (cur ?? []).map((x) => (x.id === s.id ? updated : x)))
    } catch (err) {
      return { error: errorText(err) }
    }
  }

  const visible = useMemo(() => (services ?? []).filter((s) => showHidden || !s.hidden), [services, showHidden])
  const kindLabel = useCallback((k: string) => t(`sharedServices.kinds.${k}`, undefined, k), [t])

  const columns = useMemo<DataTableColumn<SharedService>[]>(
    () => [
      {
        id: "name",
        header: t("databases.name"),
        label: t("databases.name"),
        sortValue: (s) => s.name,
        searchValue: (s) => [s.name, s.kind, s.server_name, s.host ?? ""].join(" "),
        edit: manage ? { type: "text", validate: (v) => (String(v ?? "").trim() ? null : t("sharedServices.nameRequired")) } : undefined,
        cell: (s) => (
          <span className="flex min-w-0 items-center gap-3">
            <DatabaseIcon className="size-4 shrink-0 text-muted-foreground" />
            <span className="min-w-0">
              <span className="block truncate font-medium">{s.name}</span>
              <span className="flex items-center gap-1 text-caption text-muted-foreground">
                <ServerIcon className="size-3" />
                {s.server_name} · {s.guest_type === "qemu" ? "VM" : "CT"} {s.guest_vmid}
              </span>
            </span>
          </span>
        ),
      },
      {
        id: "kind",
        header: t("databases.kind"),
        label: t("databases.kind"),
        sortValue: (s) => kindLabel(s.kind),
        filterValue: (s) => s.kind,
        edit: manage ? { type: "select", value: (s) => s.kind, options: SHARED_SERVICE_KINDS.map((k) => ({ value: k, label: kindLabel(k) })) } : undefined,
        cell: (s) => <Badge variant="neutral">{kindLabel(s.kind)}</Badge>,
      },
      {
        id: "address",
        header: t("databases.endpoint"),
        label: t("databases.endpoint"),
        sortValue: (s) => s.host ?? "",
        className: "max-md:hidden",
        headerClassName: "max-md:hidden",
        cell: (s) => (
          <span dir="ltr" className="font-mono text-caption text-muted-foreground">
            {s.host ?? "—"}
            {s.port ? `:${s.port}` : ""}
          </span>
        ),
      },
      {
        id: "hidden",
        header: <span className="sr-only">{t("sharedServices.hidden")}</span>,
        label: t("sharedServices.hidden"),
        sortValue: (s) => (s.hidden ? 1 : 0),
        cell: (s) => (s.hidden ? <Badge variant="outline">{t("sharedServices.hidden")}</Badge> : null),
      },
    ],
    [t, manage, kindLabel],
  )
  const table = useDataTable({ data: visible, columns, getRowId: (s) => s.id, pageSize: 25, defaultSort: { id: "kind", direction: "asc" } })
  const kinds = useMemo(() => [...new Set(visible.map((s) => s.kind))], [visible])

  const actions = (s: SharedService): DataTableRowAction[] =>
    manage
      ? [
          {
            id: "hide",
            label: s.hidden ? t("sharedServices.unhide") : t("sharedServices.hide"),
            icon: s.hidden ? EyeIcon : CircleOffIcon,
            onSelect: () => void update(s, { hidden: !s.hidden }).then((r) => r && setError(r.error)),
          },
        ]
      : []

  return (
    <PageBody
      title={t("nav.sharedServices")}
      description={t("sharedServices.description")}
      actions={
        manage ? (
          <Button variant="primary" onClick={() => void scan()} disabled={scanning}>
            <RefreshCwIcon className={scanning ? "animate-spin" : undefined} />
            {t("sharedServices.scan")}
          </Button>
        ) : null
      }
      bleed
      divider={false}
    >
      <ErrorBand message={error} />
      {services === null ? (
        <LoadingRows />
      ) : services.length === 0 ? (
        <div className="px-6 pb-6">
          <EmptyState icon={DatabaseIcon} title={t("sharedServices.emptyTitle")} description={t("sharedServices.emptyHint")} />
        </div>
      ) : (
        <div className="flex flex-col gap-3 px-6 pb-6">
          <DataTableToolbar>
            <DataTableSearch table={table} placeholder={t("sharedServices.search")} />
            {kinds.length > 1 ? (
              <DataTableFacetFilter
                table={table}
                column="kind"
                title={t("databases.kind")}
                options={kinds.map((k) => ({ value: k, label: kindLabel(k) }))}
              />
            ) : null}
            <label className="ms-auto flex items-center gap-2 text-caption text-muted-foreground">
              <Switch checked={showHidden} onCheckedChange={(v) => setShowHidden(!!v)} />
              {t("sharedServices.showHidden")}
            </label>
          </DataTableToolbar>
          <DataTable
            table={table}
            label={t("nav.sharedServices")}
            rowLabel={(s) => s.name}
            rowActions={manage ? actions : undefined}
            onCellEdit={(s, col, v) =>
              col === "name" ? update(s, { name: String(v ?? "").trim() }) : col === "kind" ? update(s, { kind: String(v) }) : undefined
            }
            empty={t("home.noMatch")}
          />
          <DataTablePagination table={table} />
        </div>
      )}
    </PageBody>
  )
}
