"use client"

import { Button, Card, CardContent, CardHeader, CardTitle, EmptyState } from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useState, type ReactNode } from "react"
import { ChartNoAxesColumnIcon } from "lucide-react"

import { MetricChart, type ChartPoint } from "@/components/servers/metrics-chart"
import { ErrorBand, LoadingRows, SectionTitle } from "@/components/states"


import { apiFetch } from "@/lib/api"
import { useI18n } from "@/lib/i18n"
import { formatBytes } from "@/lib/servers"

const RANGES = ["1h", "6h", "24h", "7d", "30d"] as const
type RangeKey = (typeof RANGES)[number]
/** Ranges short enough that the server is likely still reporting while the page is open. */
const LIVE_RANGES: RangeKey[] = ["1h", "6h"]

type GPUPoint = { index: number; util: number; mem_used: number; mem_total: number; temp: number }
type Point = {
  at: string
  cpu: number
  mem_used: number
  mem_total: number
  disk_used: number
  disk_total: number
  load1: number
  net_rx: number
  net_tx: number
  gpus: GPUPoint[]
}
type Series = { range: string; step_seconds: number; points: Point[] }

const pct = (used: number, total: number) => (total > 0 ? Math.round((used / total) * 1000) / 10 : null)

/** One server's metrics history: a range picker and charts for CPU, memory, disk, load, network
 * and (when the server has one) GPU. Polls on the shorter ranges so it moves while the server is
 * being watched; a longer range is a still snapshot, refetched only when the range changes. */
export function ServerMetrics({ path }: { path: string }) {
  const { t, locale } = useI18n()
  const [range, setRange] = useState<RangeKey>("1h")
  const [series, setSeries] = useState<Series | null>(null)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      const s = await apiFetch<Series>(`${path}/metrics?range=${range}`)
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
  const gpuCount = Math.max(0, ...points.map((p) => p.gpus?.length ?? 0))

  const at = (i: number) => points[i]?.at
  const line = (values: (i: number) => number | null): ChartPoint[] => points.map((_, i) => ({ at: at(i), value: values(i) }))

  return (
    <section aria-labelledby="metrics-h">
      <SectionTitle
        id="metrics-h"
        title={t("servers.metrics.title")}
        hint={t("servers.metrics.hint")}
        action={
          <div className="flex flex-wrap items-center gap-1" role="group" aria-label={t("servers.metrics.range")}>
            {RANGES.map((r) => (
              <Button key={r} size="sm" variant={range === r ? "primary" : "secondary"} onClick={() => setRange(r)}>
                {t(`servers.metrics.ranges.${r}`)}
              </Button>
            ))}
          </div>
        }
      />
      <ErrorBand message={error} />
      {series === null ? (
        <LoadingRows rows={4} />
      ) : empty ? (
        <EmptyState className="rounded-lg border border-dashed border-border" icon={ChartNoAxesColumnIcon} title={t("servers.metrics.emptyTitle")} description={t("servers.metrics.emptyHint")} />
      ) : (
        <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
          <ChartCard title={t("servers.cpu")}>
            <MetricChart
              ariaLabel={t("servers.cpu")}
              max={100}
              unit="%"
              series={[{ key: "cpu", label: t("servers.cpu"), color: "var(--nq-brand)", points: line((i) => points[i].cpu) }]}
              formatTime={fmtTime}
            />
          </ChartCard>
          <ChartCard title={t("servers.memory")}>
            <MetricChart
              ariaLabel={t("servers.memory")}
              max={100}
              unit="%"
              series={[
                {
                  key: "mem",
                  label: t("servers.memory"),
                  color: "var(--nq-accent)",
                  points: line((i) => pct(points[i].mem_used, points[i].mem_total)),
                },
              ]}
              formatTime={fmtTime}
            />
          </ChartCard>
          <ChartCard title={t("servers.disk")}>
            <MetricChart
              ariaLabel={t("servers.disk")}
              max={100}
              unit="%"
              series={[
                {
                  key: "disk",
                  label: t("servers.disk"),
                  color: "var(--nq-success)",
                  points: line((i) => pct(points[i].disk_used, points[i].disk_total)),
                },
              ]}
              formatTime={fmtTime}
            />
          </ChartCard>
          <ChartCard title={t("servers.load")}>
            <MetricChart
              ariaLabel={t("servers.load")}
              series={[{ key: "load1", label: t("servers.load"), color: "var(--nq-brand)", points: line((i) => points[i].load1) }]}
              formatValue={(v) => v.toFixed(2)}
              formatTime={fmtTime}
            />
          </ChartCard>
          <ChartCard title={t("servers.metrics.network")}>
            <MetricChart
              ariaLabel={t("servers.metrics.network")}
              series={[
                { key: "rx", label: t("servers.metrics.rx"), color: "var(--nq-brand)", points: line((i) => points[i].net_rx) },
                { key: "tx", label: t("servers.metrics.tx"), color: "var(--nq-accent)", points: line((i) => points[i].net_tx) },
              ]}
              formatValue={(v) => `${formatBytes(v)}/s`}
              formatTime={fmtTime}
            />
          </ChartCard>
          {gpuCount > 0
            ? Array.from({ length: gpuCount }, (_, gi) => (
                <ChartCard key={gi} title={t("servers.metrics.gpu", { n: gi })}>
                  <MetricChart
                    ariaLabel={t("servers.metrics.gpu", { n: gi })}
                    max={100}
                    unit="%"
                    series={[
                      {
                        key: `gpu${gi}`,
                        label: t("servers.metrics.gpu", { n: gi }),
                        color: "var(--nq-success)",
                        points: line((i) => points[i].gpus?.find((g) => g.index === gi)?.util ?? null),
                      },
                    ]}
                    formatTime={fmtTime}
                  />
                </ChartCard>
              ))
            : null}
        </div>
      )}
    </section>
  )
}

function ChartCard({ title, children }: { title: string; children: ReactNode }) {
  return (
    <Card className="min-w-0">
      <CardHeader>
        <CardTitle as="h3">{title}</CardTitle>
      </CardHeader>
      <CardContent>{children}</CardContent>
    </Card>
  )
}
