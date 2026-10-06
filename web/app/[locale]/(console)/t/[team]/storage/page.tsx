"use client"

import {
  Alert,
  Badge,
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
import { useRouter } from "next/navigation"
import { useCallback, useEffect, useMemo, useState } from "react"
import { BoxIcon, ChevronRightIcon, ContainerIcon, HardDriveIcon, PackageIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { useErrorText } from "@/components/form-error"
import { useTimeAgo } from "@/components/home/sparkline"
import { CreateDialog } from "@/components/server-tabs/databases"
import { ErrorBand, LoadingRows } from "@/components/states"
import { apiFetch } from "@/lib/api"
import type { DataService } from "@/lib/databases"
import { useDocumentTitle, useI18n } from "@/lib/i18n"
import type { Server } from "@/lib/servers"
import { useTeam } from "@/lib/team"

interface TeamVolume {
  id: string
  name: string
  mount_path: string
  created_at: string
  app_id: string
  app_name: string
  server_id: string
}

interface TeamBucket {
  id: string
  bucket: string
  env_prefix: string
  status: "creating" | "ready" | "failed" | "removing"
  error: string | null
  created_at: string
  service_id: string
  service_name: string
  app_id: string
  app_name: string
}

/**
 * Object storage and persistent volumes across the team: the MinIO services, every app bucket on
 * them and every app volume. Adding a bucket or a volume happens on the app's Storage tab.
 */
export default function StoragePage() {
  const { t, locale } = useI18n()
  const { team, manage } = useTeam()
  useDocumentTitle(`${t("nav.storage")} — ${team.name}`)
  const errorText = useErrorText()
  const ago = useTimeAgo(locale)
  const base = `/${locale}/t/${team.slug}`

  const [minio, setMinio] = useState<DataService[] | null>(null)
  const [volumes, setVolumes] = useState<TeamVolume[]>([])
  const [buckets, setBuckets] = useState<TeamBucket[]>([])
  const [servers, setServers] = useState<Server[]>([])
  const [error, setError] = useState<string | null>(null)
  const router = useRouter()
  const appLink = (app: string) => `${base}/apps/${encodeURIComponent(app)}/storage`

  const load = useCallback(async () => {
    try {
      const [d, st, s] = await Promise.all([
        apiFetch<{ services: DataService[] }>(`/api/teams/${team.slug}/data-services`),
        apiFetch<{ volumes: TeamVolume[]; buckets: TeamBucket[] }>(`/api/teams/${team.slug}/storage`),
        apiFetch<{ servers: Server[] }>(`/api/teams/${team.slug}/servers`).catch(() => ({ servers: [] as Server[] })),
      ])
      setMinio((d.services ?? []).filter((x) => x.kind === "minio"))
      setVolumes(st.volumes ?? [])
      setBuckets(st.buckets ?? [])
      setServers(s.servers ?? [])
      setError(null)
    } catch (err) {
      setError(errorText(err))
      setMinio((x) => x ?? [])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [team.slug])

  useEffect(() => {
    void load()
    const id = setInterval(load, 30_000)
    return () => clearInterval(id)
  }, [load])

  const serverName = useCallback((id: string) => servers.find((s) => s.id === id)?.name ?? "—", [servers])

  // Buckets, volumes and the MinIO services behind them, in one list.
  const items = useMemo<Item[]>(() => {
    const svc = new Map((minio ?? []).map((s) => [s.id, s]))
    return [
      ...(minio ?? []).map<Item>((s) => {
        const st = s.source === "imported" ? (s.imported_state ?? "stopped") : s.status
        return {
          id: `s-${s.id}`,
          kind: "service",
          name: s.name,
          detail: `minio ${s.version}`.trim(),
          serverId: s.server_id,
          target: s.host && s.port ? `${s.host}:${s.port}` : "",
          targetHref: "",
          status: [stateTone(st), t(`databases.statuses.${st}`, undefined, st)],
          statusKey: st,
          createdAt: s.created_at,
          href: `${base}/storage/${s.id}`,
        }
      }),
      ...buckets.map<Item>((b) => ({
        id: `b-${b.id}`,
        kind: "bucket",
        name: b.bucket,
        detail: `${b.env_prefix}_*`,
        serverId: svc.get(b.service_id)?.server_id ?? "",
        via: b.service_name,
        target: b.app_name,
        targetHref: appLink(b.app_name),
        status: [b.status === "ready" ? "success" : b.status === "failed" ? "danger" : "info", t(`storage2.bucketStatus.${b.status}`)],
        statusKey: b.status,
        createdAt: b.created_at,
        href: `${base}/storage/${b.service_id}?bucket=${encodeURIComponent(b.bucket)}`,
      })),
      ...volumes.map<Item>((v) => ({
        id: `v-${v.id}`,
        kind: "volume",
        name: v.name,
        detail: v.mount_path,
        serverId: v.server_id,
        target: v.app_name,
        targetHref: appLink(v.app_name),
        status: ["success", t("storage2.mounted")],
        statusKey: "ready",
        createdAt: v.created_at,
        href: `${base}/storage/volumes/${encodeURIComponent(v.app_name)}/${encodeURIComponent(v.name)}`,
      })),
    ]
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [minio, buckets, volumes, t, base])

  const columns = useMemo<DataTableColumn<Item>[]>(
    () => [
      {
        id: "name",
        header: t("storage2.name"),
        label: t("storage2.name"),
        sortValue: (i) => i.name,
        searchValue: (i) => [i.name, i.detail, i.target, i.via, serverName(i.serverId)].filter(Boolean).join(" "),
        cell: (i) => {
          const Icon = KIND_ICON[i.kind]
          return (
            <span className="flex min-w-0 items-center gap-3">
              <span className="flex size-8 shrink-0 items-center justify-center rounded-control border border-border bg-secondary text-muted-foreground">
                <Icon aria-hidden className="size-4" />
              </span>
              <span className="flex min-w-0 flex-col">
                <bdi dir="ltr" className="truncate text-start font-medium text-foreground">
                  {i.name}
                </bdi>
                <code dir="ltr" className="truncate text-start font-mono text-caption text-muted-foreground">
                  {i.detail}
                </code>
              </span>
            </span>
          )
        },
      },
      {
        id: "kind",
        header: t("storage2.type"),
        label: t("storage2.type"),
        sortValue: (i) => i.kind,
        filterValue: (i) => i.kind,
        className: "max-sm:hidden",
        headerClassName: "max-sm:hidden",
        cell: (i) => <Badge variant="neutral">{t(`storage2.kinds.${i.kind}`)}</Badge>,
      },
      {
        id: "path",
        header: t("domains2.connectedTo"),
        label: t("domains2.connectedTo"),
        sortValue: (i) => i.target,
        filterValue: (i) => i.serverId,
        cell: (i) => {
          const sep = <ChevronRightIcon aria-hidden className="size-3.5 shrink-0 text-muted-foreground rtl:rotate-180" />
          return (
            <span className="inline-flex min-w-0 items-center gap-1.5 text-body-sm">
              <bdi dir="ltr" className="truncate text-muted-foreground">
                {serverName(i.serverId)}
              </bdi>
              {i.via ? (
                <>
                  {sep}
                  <bdi dir="ltr" className="truncate text-muted-foreground">
                    {i.via}
                  </bdi>
                </>
              ) : null}
              {i.target ? (
                <>
                  {sep}
                  <bdi
                    dir="ltr"
                    className={i.kind === "service" ? "truncate font-mono text-foreground" : "truncate font-medium text-foreground"}
                  >
                    {i.target}
                  </bdi>
                </>
              ) : null}
            </span>
          )
        },
      },
      {
        id: "status",
        header: t("databases.status"),
        label: t("databases.status"),
        sortValue: (i) => i.statusKey,
        cell: (i) => <Status tone={i.status[0]}>{i.status[1]}</Status>,
      },
      {
        id: "created",
        header: t("storage2.created"),
        label: t("storage2.created"),
        sortValue: (i) => i.createdAt,
        className: "max-md:hidden",
        headerClassName: "max-md:hidden",
        cell: (i) => <span className="text-caption text-muted-foreground">{ago(i.createdAt)}</span>,
      },
    ],
    [t, ago, serverName],
  )
  const table = useDataTable({
    data: items,
    columns,
    getRowId: (i) => i.id,
    pageSize: 20,
    defaultSort: { id: "created", direction: "desc" },
  })
  const onServers = useMemo(() => [...new Set(items.map((i) => i.serverId).filter(Boolean))], [items])

  const create =
    manage && servers.length > 0 ? (
      <CreateDialog teamSlug={team.slug} serverId={servers[0].id} onCreated={load} kinds={["minio"]} storage />
    ) : null
  const counts = (k: Item["kind"]) => items.filter((i) => i.kind === k).length

  return (
    <PageBody title={t("nav.storage")} description={t("storage2.lede")} actions={create} bleed divider={false}>
      <ErrorBand message={error} />
      {minio === null ? (
        <LoadingRows rows={4} />
      ) : items.length === 0 ? (
        <div className="px-6 pb-6">
          <EmptyState icon={HardDriveIcon} title={t("storage2.emptyTitle")} description={t("storage2.emptyBody")} actions={create} />
        </div>
      ) : (
        <div className="flex flex-col gap-3 px-6 pb-6">
          {counts("service") === 0 ? <Alert tone="info">{t("storage2.noServices")}</Alert> : null}
          <DataTableToolbar>
            <DataTableSearch table={table} placeholder={t("storage2.search")} />
            <DataTableFacetFilter
              table={table}
              column="kind"
              title={t("storage2.type")}
              options={(["service", "bucket", "volume"] as const).map((k) => ({
                value: k,
                label: `${t(`storage2.kinds.${k}`)} (${counts(k)})`,
              }))}
            />
            {onServers.length > 1 ? (
              <DataTableFacetFilter
                table={table}
                column="path"
                title={t("nav.servers")}
                options={onServers.map((id) => ({ value: id, label: serverName(id) }))}
              />
            ) : null}
          </DataTableToolbar>
          <DataTable
            table={table}
            label={t("nav.storage")}
            rowLabel={(i) => i.name}
            onRowClick={(i) => router.push(i.href)}
            empty={t("storage2.noMatch")}
          />
          <DataTablePagination table={table} />
          <p className="text-caption text-muted-foreground">{t("storage2.addHint")}</p>
        </div>
      )}
    </PageBody>
  )
}

const KIND_ICON = { service: ContainerIcon, bucket: BoxIcon, volume: PackageIcon } as const

function stateTone(st: string): StatusTone {
  if (st === "running") return "success"
  if (st === "creating" || st === "provisioning" || st === "queued") return "info"
  if (st === "failed" || st === "missing") return "danger"
  return "neutral"
}

/** One row: a MinIO service, a bucket on it, or an app's persistent volume. */
interface Item {
  id: string
  kind: "service" | "bucket" | "volume"
  name: string
  detail: string
  serverId: string
  via?: string
  target: string
  targetHref: string
  status: [StatusTone, string]
  statusKey: string
  createdAt: string
  href: string
}
