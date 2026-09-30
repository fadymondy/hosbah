"use client"

import { Button, EmptyState } from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useState, type ReactNode } from "react"
import { ChartNoAxesColumnIcon } from "lucide-react"

import { MetricChart, type ChartPoint } from "@/components/servers/metrics-chart"
import { ErrorBand, LoadingRows } from "@/components/states"


import { apiFetch } from "@/lib/api"
import { humanBytes } from "@/lib/databases"
import { useI18n } from "@/lib/i18n"

/*
DB metrics history charts (MH-575 F6): the same KPIs the Insights panel shows live, sampled every
five minutes (internal/dbsvc's sampler, over the existing db.insights agent op) and bucketed
server-side like a server's own metrics chart (internal/api home.go get-server-metrics / H1's
ServerMetrics component). Reuses the exact same MetricChart component and range-picker shape so a
data service's history looks like a server's.
*/

const RANGES = ["1h", "6h", "24h", "7d", "30d"] as const
type RangeKey = (typeof RANGES)[number]
const LIVE_RANGES: RangeKey[] = ["1h", "6h"]

type Point = {
  at: string
  connections: number
  max_connections: number
  cache_hit_ratio: number
  size_bytes: number
  slow_query_count: number
  rate_per_sec: number
}
type Series = { range: string; step_seconds: number; points: Point[] }

export function DbMetricsHistory({ teamSlug, serviceRef, kind }: { teamSlug: string; serviceRef: string; kind: string }) {
  const { t, locale } = useI18n()
  const [range, setRange] = useState<RangeKey>("1h")
  const [series, setSeries] = useState<Series | null>(null)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      const s = await apiFetch<Series>(`/api/teams/${teamSlug}/data-services/${serviceRef}/metrics?range=${range}`)
      setSeries(s)
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [teamSlug, serviceRef, range])

  useEffect(() => {
    setSeries(null)
    void load()
    if (!LIVE_RANGES.includes(range)) return
    const id = setInterval(load, 15_000)
    return () => clearInterval(id)
  }, [load, range])

  const fmtTime = (iso: string) => {
    const d = new Date(iso)
    return range === "1h" || range === "6h"
      ? d.toLocaleTimeString(locale === "ar" ? "ar-EG-u-nu-latn" : "en-GB", { hour: "2-digit", minute: "2-digit" })
      : d.toLocaleString(locale === "ar" ? "ar-EG-u-nu-latn" : "en-GB", { dateStyle: "short", timeStyle: "short" })
  }

  const points = series?.points ?? []
  const empty = series !== null && points.length === 0
  const maxConn = Math.max(1, ...points.map((p) => p.max_connections || 0))

  const at = (i: number) => points[i]?.at
  const line = (values: (i: number) => number | null): ChartPoint[] => points.map((_, i) => ({ at: at(i), value: values(i) }))

  return (
    <section aria-labelledby="db-metrics-h" className="border-b border-border">
      <div className="flex flex-wrap items-center justify-between gap-2 px-6 py-3">
        <div>
          <h3 id="db-metrics-h" className="text-sm font-medium text-foreground">
            {t("databases.metrics.title")}
          </h3>
          <p className="text-xs text-muted-foreground">{t("databases.metrics.hint")}</p>
        </div>
        <div className="flex flex-wrap items-center gap-1" role="group" aria-label={t("databases.metrics.range")}>
          {RANGES.map((r) => (
            <Button key={r} size="sm" variant={range === r ? "primary" : "secondary"} onClick={() => setRange(r)}>
              {t(`databases.metrics.ranges.${r}`)}
            </Button>
          ))}
        </div>
      </div>
      <ErrorBand message={error} />
      {series === null ? (
        <LoadingRows rows={4} />
      ) : empty ? (
        <EmptyState icon={ChartNoAxesColumnIcon} title={t("databases.metrics.emptyTitle")} description={t("databases.metrics.emptyHint")} />
      ) : (
        <div className="grid grid-cols-1 gap-px border-t border-border bg-border lg:grid-cols-2">
          <ChartCard title={t("databases.metrics.connections")}>
            <MetricChart
              ariaLabel={t("databases.metrics.connections")}
              max={maxConn}
              series={[{ key: "conn", label: t("databases.metrics.connections"), color: "var(--nq-brand)", points: line((i) => points[i].connections) }]}
              formatTime={fmtTime}
            />
          </ChartCard>
          <ChartCard title={kind === "mysql" ? t("databases.metrics.rateMysql") : t("databases.metrics.rate")}>
            <MetricChart
              ariaLabel={kind === "mysql" ? t("databases.metrics.rateMysql") : t("databases.metrics.rate")}
              series={[{ key: "rate", label: "rate", color: "var(--nq-accent)", points: line((i) => points[i].rate_per_sec) }]}
              formatValue={(v) => `${v.toFixed(2)}/s`}
              formatTime={fmtTime}
            />
          </ChartCard>
          <ChartCard title={t("databases.metrics.cacheHit")}>
            <MetricChart
              ariaLabel={t("databases.metrics.cacheHit")}
              max={100}
              unit="%"
              series={[{ key: "cache", label: t("databases.metrics.cacheHit"), color: "var(--nq-success)", points: line((i) => points[i].cache_hit_ratio) }]}
              formatTime={fmtTime}
            />
          </ChartCard>
          <ChartCard title={t("databases.metrics.size")}>
            <MetricChart
              ariaLabel={t("databases.metrics.size")}
              series={[{ key: "size", label: t("databases.metrics.size"), color: "var(--nq-brand)", points: line((i) => points[i].size_bytes) }]}
              formatValue={(v) => humanBytes(v)}
              formatTime={fmtTime}
            />
          </ChartCard>
          <ChartCard title={t("databases.metrics.slowQueries")}>
            <MetricChart
              ariaLabel={t("databases.metrics.slowQueries")}
              series={[
                { key: "slow", label: t("databases.metrics.slowQueries"), color: "var(--nq-danger)", points: line((i) => points[i].slow_query_count) },
              ]}
              formatTime={fmtTime}
            />
          </ChartCard>
        </div>
      )}
    </section>
  )
}

function ChartCard({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-3 bg-background px-6 py-4">
      <h4 className="text-sm font-medium text-foreground">{title}</h4>
      {children}
    </div>
  )
}
