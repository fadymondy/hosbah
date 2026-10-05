"use client"

import { Badge, Button, EmptyState, StatCard, StatGrid } from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useMemo, useState } from "react"
import { ActivityIcon, ChartNoAxesColumnIcon, ExternalLinkIcon, GaugeIcon, TriangleAlertIcon, UsersIcon } from "lucide-react"

import { useLoadedSite } from "@/components/site/context"
import { NasaqTable } from "@/components/lists/nasaq-table"
import { ErrorBand, LoadingRows, SectionTitle } from "@/components/states"




import { apiFetch } from "@/lib/api"
import { useI18n } from "@/lib/i18n"

type KeyCount = { key: string; count: number }
type Point = {
  at: string
  requests: number
  status_2xx: number
  status_3xx: number
  status_4xx: number
  status_5xx: number
  bytes_out: number
  p50_ms: number
  p95_ms: number
  visitors: number
}
type Totals = { requests: number; visitors: number; error_rate: number; p95_ms: number }
type Series = {
  range: string
  step_seconds: number
  points: Point[]
  totals: Totals
  top_paths: KeyCount[]
  top_referrers: KeyCount[]
}

const RANGES = ["1h", "24h", "7d", "30d"] as const

/**
 * Traffic analytics for a site, from Caddy's access log through the agent: requests over time,
 * status breakdown, top paths and referrers, unique visitors. Works the same for a git-deployed
 * app or an imported workload — the data comes from the proxy, not the app.
 */
export function SiteAnalytics() {
  const { t, locale } = useI18n()
  const { path } = useLoadedSite()
  const [range, setRange] = useState<(typeof RANGES)[number]>("1h")
  const [series, setSeries] = useState<Series | null>(null)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      const s = await apiFetch<Series>(`${path}/analytics?range=${range}`)
      setSeries(s)
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path, range])

  useEffect(() => {
    setSeries(null)
    void load()
    const id = setInterval(load, 30_000)
    return () => clearInterval(id)
  }, [load])

  const empty = series !== null && series.totals.requests === 0

  return (
    <section aria-labelledby="an-h">
      <SectionTitle
        id="an-h"
        title={t("site.nav.analytics")}
        hint={t("site.analytics.hint")}
        action={
          <div className="flex flex-wrap items-center gap-1" role="group" aria-label={t("site.analytics.range")}>
            {RANGES.map((r) => (
              <Button key={r} size="sm" variant={range === r ? "primary" : "secondary"} onClick={() => setRange(r)}>
                {t(`site.analytics.ranges.${r}`)}
              </Button>
            ))}
          </div>
        }
      />
      <ErrorBand message={error} />
      {series === null ? (
        <LoadingRows rows={4} />
      ) : empty ? (
        <EmptyState className="nq-empty" icon={ChartNoAxesColumnIcon} title={t("site.analytics.emptyTitle")} description={t("site.analytics.emptyHint")} />
      ) : (
        <>
          <Tiles t={t} totals={series.totals} points={series.points} />
          <div className="nq-band py-6">
            <RequestsChart points={series.points} />
          </div>
          <StatusBreakdown t={t} points={series.points} />
          <TopTable title={t("site.analytics.topPaths")} rows={series.top_paths} empty={t("site.analytics.noPaths")} />
          <TopTable title={t("site.analytics.topReferrers")} rows={series.top_referrers} empty={t("site.analytics.noReferrers")} external />
        </>
      )}
    </section>
  )
}

type TFunc = (key: string, vars?: Record<string, string | number>, fallback?: string) => string

function Tiles({ t, totals, points }: { t: TFunc; totals: Totals; points: Point[] }) {
  const tiles = [
    { icon: <ActivityIcon />, label: t("site.analytics.requests"), value: totals.requests.toLocaleString(), spark: points.map((p) => p.requests) },
    { icon: <UsersIcon />, label: t("site.analytics.visitors"), value: totals.visitors.toLocaleString() },
    {
      icon: <TriangleAlertIcon />,
      label: t("site.analytics.errorRate"),
      value: `${(totals.error_rate * 100).toFixed(1)}%`,
      spark: points.map((p) => p.status_5xx + p.status_4xx),
    },
    { icon: <GaugeIcon />, label: t("site.analytics.p95"), value: `${Math.round(totals.p95_ms)} ms` },
  ]
  return (
    <StatGrid className="nq-stats">
      {tiles.map((tile) => (
        <StatCard
          key={tile.label}
          icon={tile.icon}
          label={tile.label}
          sparkline={tile.spark && tile.spark.length > 1 ? tile.spark : undefined}
          value={
            <span dir="ltr" className="text-start">
              {tile.value}
            </span>
          }
        />
      ))}
    </StatGrid>
  )
}

/**
 * Requests over time: a hand-rolled SVG area chart (no chart library in this codebase). Always
 * drawn left→right regardless of page direction — time runs the same way in both languages, as in
 * Sparkline. A light gridline marks the midpoint and the request count is shown on hover via the
 * native <title>, since this repo has no tooltip primitive worth adding for one chart.
 */
function RequestsChart({ points }: { points: Point[] }) {
  const w = 960
  const h = 220
  const pad = { top: 10, bottom: 24, start: 0, end: 0 }
  const innerH = h - pad.top - pad.bottom
  const { path, area, bars, max } = useMemo(() => {
    const max = Math.max(1, ...points.map((p) => p.requests))
    const step = points.length > 1 ? w / (points.length - 1) : w
    const coords = points.map((p, i) => [i * step, pad.top + innerH - (p.requests / max) * innerH] as const)
    const path = coords.map(([x, y], i) => `${i ? "L" : "M"}${x.toFixed(1)},${y.toFixed(1)}`).join(" ")
    const area = coords.length > 1 ? `${path} L${coords[coords.length - 1][0]},${h - pad.bottom} L0,${h - pad.bottom} Z` : ""
    return { path, area, bars: coords.map(([x, y], i) => ({ x, y, p: points[i] })), max }
  }, [points, innerH])

  if (points.length === 0) return null
  const first = points[0]?.at
  const last = points[points.length - 1]?.at
  return (
    <div dir="ltr">
      <svg viewBox={`0 0 ${w} ${h}`} preserveAspectRatio="none" className="block h-56 w-full" role="img" aria-label="Requests over time">
        <line x1={0} y1={pad.top + innerH / 2} x2={w} y2={pad.top + innerH / 2} stroke="currentColor" strokeOpacity={0.08} strokeDasharray="4 4" />
        <line x1={0} y1={h - pad.bottom} x2={w} y2={h - pad.bottom} stroke="currentColor" strokeOpacity={0.15} />
        {area ? <path d={area} fill="var(--nq-brand)" opacity={0.14} /> : null}
        {points.length > 1 ? <path d={path} fill="none" stroke="var(--nq-brand)" strokeWidth={1.5} vectorEffect="non-scaling-stroke" /> : null}
        {bars.map(({ x, y, p }, i) => (
          <circle key={i} cx={x} cy={y} r={points.length > 60 ? 0 : 2} fill="var(--nq-brand)" vectorEffect="non-scaling-stroke">
            <title>
              {p.at}: {p.requests}
            </title>
          </circle>
        ))}
      </svg>
      <div className="mt-1 flex justify-between text-xs text-muted-foreground">
        <time dateTime={first}>{first ? new Date(first).toLocaleString() : ""}</time>
        <span>max {max.toLocaleString()}</span>
        <time dateTime={last}>{last ? new Date(last).toLocaleString() : ""}</time>
      </div>
    </div>
  )
}

/** Status-code breakdown across the whole range, as a single stacked bar plus a legend with counts. */
function StatusBreakdown({ t, points }: { t: TFunc; points: Point[] }) {
  const sums = points.reduce(
    (acc, p) => ({
      "2xx": acc["2xx"] + p.status_2xx,
      "3xx": acc["3xx"] + p.status_3xx,
      "4xx": acc["4xx"] + p.status_4xx,
      "5xx": acc["5xx"] + p.status_5xx,
    }),
    { "2xx": 0, "3xx": 0, "4xx": 0, "5xx": 0 },
  )
  const total = Math.max(1, sums["2xx"] + sums["3xx"] + sums["4xx"] + sums["5xx"])
  const classes: (keyof typeof sums)[] = ["2xx", "3xx", "4xx", "5xx"]
  return (
    <section aria-labelledby="an-status-h" className="nq-band py-6">
      <h3 id="an-status-h" className="mb-3 text-sm font-medium text-foreground">
        {t("site.analytics.statusBreakdown")}
      </h3>
      <div dir="ltr" className="flex h-3 w-full overflow-hidden rounded-full bg-border">
        {classes.map((c) =>
          sums[c] > 0 ? (
            <div
              key={c}
              className={c === "2xx" ? "bg-nq-success" : c === "3xx" ? "bg-nq-info" : c === "4xx" ? "bg-nq-warning" : "bg-nq-danger"}
              style={{ width: `${(sums[c] / total) * 100}%` }}
              title={`${c}: ${sums[c]}`}
            />
          ) : null,
        )}
      </div>
      <dl className="mt-3 flex flex-wrap gap-x-6 gap-y-2 text-xs">
        {classes.map((c) => (
          <div key={c} className="flex items-center gap-1.5">
            <span
              aria-hidden
              className={`size-2 rounded-full ${c === "2xx" ? "bg-nq-success" : c === "3xx" ? "bg-nq-info" : c === "4xx" ? "bg-nq-warning" : "bg-nq-danger"}`}
            />
            <dt dir="ltr" className="text-muted-foreground">
              {c}
            </dt>
            <dd dir="ltr" className="font-medium text-foreground">
              {sums[c].toLocaleString()}
            </dd>
          </div>
        ))}
      </dl>
    </section>
  )
}

function TopTable({ title, rows, empty, external }: { title: string; rows: KeyCount[]; empty: string; external?: boolean }) {
  const { t } = useI18n()
  return (
    <section aria-labelledby={`${title}-h`}>
      <SectionTitle id={`${title}-h`} title={title} />
      {rows.length === 0 ? (
        <p className="nq-band py-4 text-sm text-muted-foreground">{empty}</p>
      ) : (
        <NasaqTable
          data={rows}
          rowId={(r) => r.key}
          rowLabel={(r) => r.key}
          label={title}
          defaultSort={{ id: "count", direction: "desc" }}
          columns={[
            {
              id: "path",
              header: t("site.analytics.path"),
              cell: (r) => (
                <span dir="ltr" className="block max-w-md truncate font-mono text-xs">
                  {external ? (
                    <a href={r.key.includes("://") ? r.key : `https://${r.key}`} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 hover:underline">
                      {r.key}
                      <ExternalLinkIcon className="size-3" />
                    </a>
                  ) : (
                    r.key
                  )}
                </span>
              ),
              searchValue: (r) => r.key,
            },
            {
              id: "count",
              header: t("site.analytics.requests"),
              align: "end",
              cell: (r) => <Badge variant="outline">{r.count.toLocaleString()}</Badge>,
              sortValue: (r) => r.count,
            },
          ]}
        />
      )}
    </section>
  )
}
