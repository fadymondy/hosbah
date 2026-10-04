"use client"

import { EmptyState, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@fadymondy/nasaq/web"
import Link from "next/link"
import { useCallback, useEffect, useMemo, useState } from "react"
import { BoxIcon, HardDriveIcon, PackageIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { useErrorText } from "@/components/form-error"
import { useTimeAgo } from "@/components/home/sparkline"
import { appTone, ListBar, StatusDot } from "@/components/lists/list-bar"
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

function Section({ title, count, children }: { title: string; count?: number; children: React.ReactNode }) {
  return (
    <section className="border-b border-line">
      <h2 className="flex items-center gap-2 border-b border-line px-6 py-3 text-sm font-semibold text-grid-fg">
        {title}
        {count !== undefined ? <span className="text-xs font-normal text-grid-muted">{count}</span> : null}
      </h2>
      {children}
    </section>
  )
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
  const [q, setQ] = useState("")

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

  const serverName = (id: string) => servers.find((s) => s.id === id)?.name ?? "—"
  const needle = q.trim().toLowerCase()
  const has = (...v: (string | null | undefined)[]) => !needle || v.some((x) => x?.toLowerCase().includes(needle))
  const shownBuckets = useMemo(() => buckets.filter((b) => has(b.bucket, b.app_name, b.service_name)), [buckets, needle]) // eslint-disable-line react-hooks/exhaustive-deps
  const shownVolumes = useMemo(() => volumes.filter((v) => has(v.name, v.app_name, v.mount_path)), [volumes, needle]) // eslint-disable-line react-hooks/exhaustive-deps

  const create = manage && servers.length > 0 ? <CreateDialog teamSlug={team.slug} serverId={servers[0].id} onCreated={load} /> : null
  const empty = minio !== null && minio.length === 0 && volumes.length === 0 && buckets.length === 0
  const appLink = (app: string) => `${base}/apps/${encodeURIComponent(app)}/storage`

  return (
    <PageBody title={t("nav.storage")} description={t("storage2.lede")} actions={create} bleed>
      <ErrorBand message={error} />
      {minio === null ? (
        <LoadingRows rows={4} />
      ) : empty ? (
        <EmptyState className="border-b" icon={HardDriveIcon} title={t("storage2.emptyTitle")} description={t("storage2.emptyBody")} actions={create} />
      ) : (
        <>
          <ListBar q={q} onQ={setQ} placeholder={t("storage2.search")} />

          <Section title={t("storage2.services")} count={minio.length}>
            {minio.length === 0 ? (
              <p className="px-6 py-4 text-sm text-grid-muted">{t("storage2.noServices")}</p>
            ) : (
              <ul className="divide-y divide-line">
                {minio.map((s) => {
                  const st = s.source === "imported" ? (s.imported_state ?? "stopped") : s.status
                  const n = buckets.filter((b) => b.service_id === s.id).length
                  return (
                    <li key={s.id} className="flex flex-wrap items-center gap-x-4 gap-y-1 px-6 py-3">
                      <Link href={`${base}/databases/${s.id}`} className="flex min-w-0 items-center gap-3 hover:underline">
                        <span className="flex size-8 shrink-0 items-center justify-center border border-line bg-grid-soft text-grid-muted">
                          <BoxIcon className="size-4" />
                        </span>
                        <span dir="ltr" className="truncate font-medium text-grid-fg">
                          {s.name}
                        </span>
                      </Link>
                      <StatusDot tone={appTone(st)} label={t(`databases.statuses.${st}`, undefined, st)} />
                      <span className="text-xs text-grid-muted">{t("storage2.bucketCount", { n })}</span>
                      <Link href={`${base}/servers/${s.server_id}/databases`} dir="ltr" className="ms-auto text-xs text-grid-muted hover:text-grid-fg hover:underline">
                        {serverName(s.server_id)}
                      </Link>
                    </li>
                  )
                })}
              </ul>
            )}
          </Section>

          <Section title={t("storage2.buckets")} count={buckets.length}>
            {shownBuckets.length === 0 ? (
              <p className="px-6 py-4 text-sm text-grid-muted">{buckets.length ? t("storage2.noMatch") : t("storage2.noBuckets")}</p>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="ps-6">{t("storage2.bucket")}</TableHead>
                    <TableHead>{t("storage2.app")}</TableHead>
                    <TableHead className="max-md:hidden">{t("storage2.service")}</TableHead>
                    <TableHead className="pe-6">{t("databases.status")}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {shownBuckets.map((b) => (
                    <TableRow key={b.id}>
                      <TableCell className="ps-6">
                        <span dir="ltr" className="block font-medium text-grid-fg">
                          {b.bucket}
                        </span>
                        <code dir="ltr" className="text-xs text-grid-muted">
                          {b.env_prefix}_*
                        </code>
                      </TableCell>
                      <TableCell>
                        <Link href={appLink(b.app_name)} dir="ltr" className="text-sm text-grid-body hover:text-grid-fg hover:underline">
                          {b.app_name}
                        </Link>
                      </TableCell>
                      <TableCell className="max-md:hidden">
                        <Link href={`${base}/databases/${b.service_id}`} dir="ltr" className="text-sm text-grid-body hover:underline">
                          {b.service_name}
                        </Link>
                      </TableCell>
                      <TableCell className="pe-6">
                        <StatusDot tone={appTone(b.status === "ready" ? "running" : b.status)} label={t(`storage2.bucketStatus.${b.status}`)} />
                        {b.error ? (
                          <p dir="ltr" className="mt-1 line-clamp-2 font-mono text-xs text-grid-danger">
                            {b.error}
                          </p>
                        ) : null}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </Section>

          <Section title={t("storage2.volumes")} count={volumes.length}>
            {shownVolumes.length === 0 ? (
              <p className="px-6 py-4 text-sm text-grid-muted">{volumes.length ? t("storage2.noMatch") : t("storage2.noVolumes")}</p>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="ps-6">{t("storage2.volume")}</TableHead>
                    <TableHead>{t("storage2.app")}</TableHead>
                    <TableHead className="max-md:hidden">{t("databases.server")}</TableHead>
                    <TableHead className="pe-6 max-sm:hidden">{t("storage2.created")}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {shownVolumes.map((v) => (
                    <TableRow key={v.id}>
                      <TableCell className="ps-6">
                        <span dir="ltr" className="block font-medium text-grid-fg">
                          <PackageIcon className="me-1.5 inline size-3.5 text-grid-muted" />
                          {v.name}
                        </span>
                        <code dir="ltr" className="text-xs text-grid-muted">
                          {v.mount_path}
                        </code>
                      </TableCell>
                      <TableCell>
                        <Link href={appLink(v.app_name)} dir="ltr" className="text-sm text-grid-body hover:text-grid-fg hover:underline">
                          {v.app_name}
                        </Link>
                      </TableCell>
                      <TableCell className="max-md:hidden">
                        <Link href={`${base}/servers/${v.server_id}`} dir="ltr" className="text-sm text-grid-body hover:underline">
                          {serverName(v.server_id)}
                        </Link>
                      </TableCell>
                      <TableCell className="pe-6 text-xs text-grid-muted max-sm:hidden">{ago(v.created_at)}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </Section>
          <p className="px-6 py-3 text-xs text-grid-muted">{t("storage2.addHint")}</p>
        </>
      )}
    </PageBody>
  )
}
