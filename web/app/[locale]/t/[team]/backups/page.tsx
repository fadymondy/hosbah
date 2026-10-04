"use client"

import { Button, EmptyState, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@fadymondy/nasaq/web"
import Link from "next/link"
import { useCallback, useEffect, useMemo, useState } from "react"
import { ArchiveIcon, DatabaseIcon, HardDriveDownloadIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { useErrorText } from "@/components/form-error"
import { useTimeAgo } from "@/components/home/sparkline"
import { FilterPills, ListBar, StatusDot, type Tone } from "@/components/lists/list-bar"
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

const TONE: Record<Health, Tone> = { failing: "fail", unprotected: "idle", running: "progress", ok: "ok", unsupported: "idle" }
const ORDER: Record<Health, number> = { failing: 0, unprotected: 1, running: 2, ok: 3, unsupported: 4 }

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
  const base = `/${locale}/t/${team.slug}`

  const [services, setServices] = useState<DataService[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [q, setQ] = useState("")
  const [filter, setFilter] = useState<"" | Health>("")
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

  const shown = useMemo(() => {
    const needle = q.trim().toLowerCase()
    return (services ?? [])
      .filter((s) => (!filter || health(s) === filter) && (!needle || `${s.name} ${s.kind}`.toLowerCase().includes(needle)))
      .sort((a, b) => ORDER[health(a)] - ORDER[health(b)] || a.name.localeCompare(b.name))
  }, [services, q, filter])

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

  return (
    <PageBody title={t("nav.backups")} description={t("backups2.lede")} bleed>
      <ErrorBand message={error} />
      {services === null ? (
        <LoadingRows rows={4} />
      ) : services.length === 0 ? (
        <EmptyState
          className="border-b"
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
      ) : (
        <>
          <ListBar q={q} onQ={setQ} placeholder={t("backups2.search")}>
            <FilterPills
              label={t("databases.status")}
              value={filter}
              onChange={(v) => setFilter(v as "" | Health)}
              options={[
                ["", t("home.filterAll"), services.length],
                ...(["failing", "unprotected", "running", "ok"] as Health[])
                  .filter((h) => counts[h] > 0)
                  .map((h) => [h, t(`backups2.health.${h}`), counts[h]] as [string, string, number]),
              ]}
            />
          </ListBar>
          {shown.length === 0 ? (
            <p className="border-b border-line px-6 py-8 text-center text-sm text-grid-muted">{t("backups2.noMatch")}</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="ps-6">{t("databases.name")}</TableHead>
                  <TableHead>{t("databases.status")}</TableHead>
                  <TableHead className="max-md:hidden">{t("backups2.lastGood")}</TableHead>
                  <TableHead className="max-lg:hidden">{t("backups2.schedule")}</TableHead>
                  <TableHead className="pe-6 text-end">
                    <span className="sr-only">{t("backups2.actions")}</span>
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {shown.map((s) => {
                  const h = health(s)
                  const b = s.backups
                  const canBackup = manage && h !== "unsupported" && h !== "running" && s.status === "running"
                  return (
                    <TableRow key={s.id} className="group align-top">
                      <TableCell className="ps-6">
                        <Link href={detail(s)} className="flex min-w-0 items-center gap-3">
                          <span className="flex size-8 shrink-0 items-center justify-center border border-line bg-grid-soft text-grid-muted">
                            <DatabaseIcon className="size-4" />
                          </span>
                          <span className="min-w-0">
                            <span dir="ltr" className="block truncate font-medium text-grid-fg group-hover:underline">
                              {s.name}
                            </span>
                            <span dir="ltr" className="block truncate text-xs text-grid-muted">
                              {s.kind} {s.version}
                            </span>
                          </span>
                        </Link>
                      </TableCell>
                      <TableCell className="max-w-80">
                        <StatusDot tone={TONE[h]} label={t(`backups2.health.${h}`)} />
                        {h === "failing" && b?.last_error ? (
                          <p dir="ltr" className="mt-1 line-clamp-2 font-mono text-xs text-grid-danger">
                            {b.last_error}
                          </p>
                        ) : h === "failing" && b?.last_at ? (
                          <p className="mt-1 text-xs text-grid-muted">{ago(b.last_at)}</p>
                        ) : null}
                        {b?.last_restore_status === "failed" ? (
                          <p className="mt-1 text-xs text-grid-danger">{t("backups2.restoreFailed")}</p>
                        ) : null}
                      </TableCell>
                      <TableCell className="text-xs max-md:hidden">
                        {b?.last_ok_at ? (
                          <>
                            <span className="block text-grid-body">{ago(b.last_ok_at)}</span>
                            <span className="block text-grid-muted">
                              {b.last_ok_size ? humanBytes(b.last_ok_size) : null}
                              {b.last_ok_size ? " · " : null}
                              {t("backups2.count", { n: b.count })}
                            </span>
                          </>
                        ) : (
                          <span className="text-grid-muted">{h === "unsupported" ? t("backups2.guestHint") : t("dbs2.never")}</span>
                        )}
                      </TableCell>
                      <TableCell className="text-xs max-lg:hidden">
                        {b?.schedule ? (
                          <>
                            <code dir="ltr" className="block text-grid-body">
                              {b.schedule}
                            </code>
                            <span className="block text-grid-muted">
                              {b.schedule_paused
                                ? t("dbs2.schedulePaused")
                                : b.next_run_at
                                  ? t("backups2.next", { when: new Date(b.next_run_at).toLocaleString(locale) })
                                  : null}
                              {b.target ? ` · ${b.target}` : null}
                            </span>
                          </>
                        ) : h === "unsupported" ? (
                          <span className="text-grid-muted">—</span>
                        ) : (
                          <Link href={detail(s)} className="text-grid-action hover:underline">
                            {t("backups2.addSchedule")}
                          </Link>
                        )}
                      </TableCell>
                      <TableCell className="pe-6 text-end">
                        {canBackup ? (
                          <Button size="sm" disabled={busy === s.id} onClick={() => void backupNow(s)}>
                            <HardDriveDownloadIcon />
                            {t("databases.backupNow")}
                          </Button>
                        ) : null}
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          )}
        </>
      )}
    </PageBody>
  )
}
