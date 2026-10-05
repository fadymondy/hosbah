"use client"

import { Badge, Button, EmptyState, toast } from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useState } from "react"
import { PlayIcon, ShieldAlertIcon } from "lucide-react"

import { useTimeAgo } from "@/components/home/sparkline"
import { useLoadedSite } from "@/components/site/context"
import { LoadMore, useInfiniteLimit } from "@/components/lists/infinite"
import { NasaqTable } from "@/components/lists/nasaq-table"
import { ErrorBand, LoadingRows, SectionTitle } from "@/components/states"




import { apiFetch } from "@/lib/api"
import { useFormatDate, useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

type Severity = "CRITICAL" | "HIGH" | "MEDIUM" | "LOW" | "UNKNOWN"
const SEVERITIES: Severity[] = ["CRITICAL", "HIGH", "MEDIUM", "LOW", "UNKNOWN"]
const SEVERITY_BADGE: Record<Severity, "danger" | "neutral" | "outline"> = {
  CRITICAL: "danger",
  HIGH: "danger",
  MEDIUM: "neutral",
  LOW: "outline",
  UNKNOWN: "outline",
}

type Finding = {
  id: string
  package: string
  installed_version: string
  fixed_version?: string
  severity: Severity
  title?: string
}
type Scan = {
  id: string
  image: string
  status: "running" | "ok" | "failed"
  trivy_version?: string
  counts: Partial<Record<Severity, number>>
  findings: Finding[]
  truncated: boolean
  error?: string
  triggered_by: "member" | "mcp" | "deploy"
  started_at?: string | null
  finished_at?: string | null
  created_at: string
}

/**
 * CVE scans (MH-580 J5): Trivy `image` scans of this site's current live release, run on demand or
 * automatically right after a deploy. Shows the latest scan's severity counts and top findings, plus
 * a short history.
 */
export function SiteSecurity() {
  const { t, locale } = useI18n()
  const { manage } = useTeam()
  const ago = useTimeAgo(locale)
  const fmt = useFormatDate()
  const { path } = useLoadedSite()
  const [scans, setScans] = useState<Scan[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const history = useInfiniteLimit(Math.max(0, (scans?.length ?? 0) - 1))

  const load = useCallback(async () => {
    try {
      const r = await apiFetch<{ scans: Scan[] }>(`${path}/scans`)
      setScans(r.scans ?? [])
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      setScans((x) => x ?? [])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path])

  useEffect(() => {
    void load()
  }, [load])

  async function scanNow() {
    setBusy(true)
    setError(null)
    try {
      const created = await apiFetch<Scan>(`${path}/scans`, { method: "POST" })
      setScans((cur) => [created, ...(cur ?? [])])
      toast.success(created.status === "ok" ? t("site.security.done") : t("site.security.failed"))
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  const latest = scans?.[0]
  const scanNowButton = manage ? (
    <Button variant="primary" size="sm" disabled={busy} onClick={() => void scanNow()}>
      <PlayIcon />
      {busy ? t("common.working") : t("site.security.scanNow")}
    </Button>
  ) : null

  return (
    <section aria-labelledby="sec-h">
      <SectionTitle id="sec-h" title={t("site.nav.security")} hint={t("site.security.hint")} action={scanNowButton} />
      <ErrorBand message={error} />
      {scans === null ? (
        <LoadingRows rows={3} />
      ) : scans.length === 0 ? (
        <EmptyState className="nq-empty" icon={ShieldAlertIcon} title={t("site.security.emptyTitle")} description={t("site.security.emptyHint")} actions={<>{scanNowButton}</>} />
      ) : (
        <>
          {latest ? (
            <div className="flex flex-col gap-3 nq-band py-4">
              <div className="flex flex-wrap items-center gap-2">
                <Badge variant={latest.status === "ok" ? "neutral" : latest.status === "failed" ? "danger" : "outline"}>
                  {t(`site.security.status.${latest.status}`)}
                </Badge>
                <span className="text-xs text-muted-foreground">{ago(latest.created_at)}</span>
                <span dir="ltr" className="max-w-md truncate text-xs text-muted-foreground">
                  {latest.image}
                </span>
              </div>
              {latest.status === "ok" ? (
                <div className="flex flex-wrap gap-2">
                  {SEVERITIES.map((sev) =>
                    latest.counts[sev] ? (
                      <Badge key={sev} variant={SEVERITY_BADGE[sev]}>
                        {t(`site.security.severity.${sev}`)} {latest.counts[sev]}
                      </Badge>
                    ) : null,
                  )}
                  {SEVERITIES.every((sev) => !latest.counts[sev]) ? (
                    <span className="text-sm text-muted-foreground">{t("site.security.clean")}</span>
                  ) : null}
                </div>
              ) : latest.status === "failed" ? (
                <p dir="auto" className="text-sm text-nq-danger-text">
                  {latest.error}
                </p>
              ) : null}
              {latest.findings.length ? (
                <NasaqTable
                  data={latest.findings}
                  rowId={(f) => f.id + f.package}
                  rowLabel={(f) => `${f.id} ${f.package}`}
                  label={t("site.security.findings")}
                  search={t("site.security.search")}
                  defaultSort={{ id: "severity", direction: "asc" }}
                  facets={[
                    {
                      column: "severity",
                      title: t("site.security.severityCol"),
                      options: SEVERITIES.filter((sev) => latest.counts[sev]).map((sev) => ({ value: sev, label: t(`site.security.severity.${sev}`) })),
                    },
                  ]}
                  columns={[
                    {
                      id: "severity",
                      header: t("site.security.severityCol"),
                      label: t("site.security.severityCol"),
                      cell: (f) => <Badge variant={SEVERITY_BADGE[f.severity]}>{t(`site.security.severity.${f.severity}`)}</Badge>,
                      sortValue: (f) => SEVERITIES.indexOf(f.severity),
                      filterValue: (f) => f.severity,
                    },
                    {
                      id: "package",
                      header: t("site.security.package"),
                      cell: (f) => (
                        <span dir="ltr" className="font-mono text-xs">
                          {f.package}
                        </span>
                      ),
                      sortValue: (f) => f.package,
                      searchValue: (f) => `${f.package} ${f.id} ${f.title ?? ""}`,
                    },
                    {
                      id: "installed",
                      header: t("site.security.installed"),
                      cell: (f) => (
                        <span dir="ltr" className="font-mono text-xs">
                          {f.installed_version}
                        </span>
                      ),
                    },
                    {
                      id: "fixed",
                      header: t("site.security.fixed"),
                      cell: (f) => (
                        <span dir="ltr" className="font-mono text-xs">
                          {f.fixed_version || "—"}
                        </span>
                      ),
                    },
                    {
                      id: "cve",
                      header: t("site.security.id"),
                      cell: (f) => (
                        <span dir="ltr" className="font-mono text-xs text-muted-foreground" title={f.title}>
                          {f.id}
                        </span>
                      ),
                    },
                  ]}
                />
              ) : null}
              {latest.truncated ? <p className="text-xs text-muted-foreground">{t("site.security.truncated")}</p> : null}
            </div>
          ) : null}

          {scans.length > 1 ? (
            <div className="nq-box">
              <p className="px-6 pt-4 text-xs font-medium text-muted-foreground">{t("site.security.history")}</p>
              <ol className="flex flex-col divide-y divide-border">
                {scans.slice(1, 1 + history.limit).map((s) => (
                  <li key={s.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-6 py-3 text-sm">
                    <Badge variant={s.status === "ok" ? "neutral" : s.status === "failed" ? "danger" : "outline"}>
                      {t(`site.security.status.${s.status}`)}
                    </Badge>
                    <span className="text-xs text-muted-foreground">{t(`site.security.trigger.${s.triggered_by}`)}</span>
                    {s.status === "ok" ? (
                      <span className="text-xs text-muted-foreground">
                        {SEVERITIES.filter((sev) => s.counts[sev]).map((sev) => `${t(`site.security.severity.${sev}`)} ${s.counts[sev]}`).join(" · ") ||
                          t("site.security.clean")}
                      </span>
                    ) : null}
                    <span dir="ltr" className="ms-auto text-xs text-muted-foreground">
                      {fmt(s.created_at)}
                    </span>
                  </li>
                ))}
              </ol>
              <LoadMore hasMore={history.hasMore} onMore={history.more} />
            </div>
          ) : null}
        </>
      )}
    </section>
  )
}
