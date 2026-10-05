"use client"

import { Badge, Button, Checkbox, EmptyState, StatCard, StatGrid } from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useMemo, useState } from "react"
import { AlertTriangleIcon, GaugeIcon } from "lucide-react"

import { CopyField } from "@/components/copy-field"
import { useErrorText } from "@/components/form-error"
import { useLoadedSite } from "@/components/site/context"
import { NasaqTable } from "@/components/lists/nasaq-table"
import { ErrorBand, LoadingRows, SectionTitle, SettingsRow } from "@/components/states"





import { apiFetch } from "@/lib/api"
import { useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

type Rating = "good" | "needs-improvement" | "poor" | "unknown"
type Metric = { p75: number; samples: number; rating: Rating }
type Metrics = { lcp: Metric; cls: Metric; inp: Metric; fcp: Metric; ttfb: Metric }
type Summary = { window: string; samples: number; metrics: Metrics }
type PathBreakdown = { path: string; samples: number; metrics: Metrics }
type Point = { bucket: string; samples: number; metrics: Metrics }
type RumSettings = {
  enabled: boolean
  has_key: boolean
  key?: string
  snippet_url?: string
  snippet?: string
}

const WINDOWS = ["24h", "7d", "30d"] as const

const METRIC_KEYS = ["lcp", "cls", "inp", "fcp", "ttfb"] as const

/**
 * Real-user monitoring (MH-575 H5): an enable toggle with a paste-once snippet, then the Core Web
 * Vitals the app's own visitors' browsers reported — p75 per metric, a trend chart and a per-path
 * breakdown. Works the same for a git-deployed app or an imported workload; the beacon just needs
 * the snippet on the page.
 */
export function SiteWebVitals() {
  const { t } = useI18n()
  const { manage } = useTeam()
  const { path } = useLoadedSite()
  const errorText = useErrorText()
  const base = `${path}/rum`

  const [settings, setSettings] = useState<RumSettings | null>(null)
  const [settingsError, setSettingsError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [rotateBusy, setRotateBusy] = useState(false)

  const loadSettings = useCallback(() => {
    apiFetch<RumSettings>(`${base}/settings`)
      .then(setSettings)
      .catch((err) => setSettingsError(errorText(err)))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [base])
  useEffect(loadSettings, [loadSettings])

  async function toggle(enabled: boolean) {
    setBusy(true)
    setSettingsError(null)
    try {
      const s = await apiFetch<RumSettings>(`${base}/settings`, { method: "PUT", body: { enabled } })
      setSettings(s)
    } catch (err) {
      setSettingsError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  async function rotate() {
    setRotateBusy(true)
    setSettingsError(null)
    try {
      const s = await apiFetch<RumSettings>(`${base}/settings/rotate`, { method: "POST" })
      setSettings(s)
    } catch (err) {
      setSettingsError(errorText(err))
    } finally {
      setRotateBusy(false)
    }
  }

  return (
    <div data-testid="web-vitals" data-stack>
      <section aria-labelledby="rum-settings-h">
        <SectionTitle
          id="rum-settings-h"
          title={t("site.rum.title")}
          hint={t("site.rum.hint")}
        />
        {settings === null ? (
          <LoadingRows rows={2} />
        ) : (
          <SettingsRow label={t("site.rum.enable")} htmlFor="rum-enable" hint={!manage ? t("common.readOnly") : t("site.rum.enableHint")}>
            <Checkbox
              id="rum-enable"
              checked={settings.enabled}
              disabled={!manage || busy}
              onCheckedChange={(v) => toggle(!!v)}
            />
          </SettingsRow>
        )}
        <ErrorBand message={settingsError} />
        {settings?.snippet ? (
          <SettingsRow label={t("site.rum.snippet")} hint={manage ? t("site.rum.rotateHint") : undefined}>
            <CopyField value={settings.snippet} block />
            <p className="flex items-start gap-2 text-xs text-nq-fg-body">
              <AlertTriangleIcon className="mt-0.5 size-3.5 shrink-0 text-nq-brand" />
              {t("site.rum.once")}
            </p>
            {manage ? (
              <div>
                <Button variant="secondary" disabled={rotateBusy} onClick={rotate}>
                  {rotateBusy ? t("common.working") : t("site.rum.rotate")}
                </Button>
              </div>
            ) : null}
          </SettingsRow>
        ) : settings?.has_key ? (
          <SettingsRow label={t("site.rum.snippet")} hint={t("site.rum.snippetHiddenHint")}>
            <p className="text-sm text-muted-foreground">{t("site.rum.snippetHidden")}</p>
            {manage ? (
              <div>
                <Button variant="secondary" disabled={rotateBusy} onClick={rotate}>
                  {rotateBusy ? t("common.working") : t("site.rum.rotate")}
                </Button>
              </div>
            ) : null}
          </SettingsRow>
        ) : null}
      </section>

      {settings?.has_key ? <WebVitalsData base={base} /> : null}
    </div>
  )
}

function ratingTone(r: Rating) {
  if (r === "good") return "text-nq-success-text"
  if (r === "needs-improvement") return "text-nq-warning-text"
  if (r === "poor") return "text-nq-danger-text"
  return "text-muted-foreground"
}

function ratingDot(r: Rating) {
  if (r === "good") return "bg-nq-success"
  if (r === "needs-improvement") return "bg-nq-warning"
  if (r === "poor") return "bg-nq-danger"
  return "bg-nq-fg-muted"
}

function WebVitalsData({ base }: { base: string }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [window_, setWindow] = useState<(typeof WINDOWS)[number]>("24h")
  const [summary, setSummary] = useState<Summary | null>(null)
  const [paths, setPaths] = useState<PathBreakdown[] | null>(null)
  const [series, setSeries] = useState<Point[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      const [s, p, ts] = await Promise.all([
        apiFetch<Summary>(`${base}/summary?window=${window_}`),
        apiFetch<{ paths: PathBreakdown[] }>(`${base}/paths?window=${window_}`),
        apiFetch<{ points: Point[] }>(`${base}/timeseries?window=${window_}`),
      ])
      setSummary(s)
      setPaths(p.paths)
      setSeries(ts.points)
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? errorText(err) : String(err))
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [base, window_])

  useEffect(() => {
    setSummary(null)
    setPaths(null)
    setSeries(null)
    void load()
    const id = setInterval(load, 30_000)
    return () => clearInterval(id)
  }, [load])

  const empty = summary !== null && summary.samples === 0

  return (
    <section aria-labelledby="rum-vitals-h">
      <SectionTitle
        id="rum-vitals-h"
        title={t("site.rum.vitalsTitle")}
        hint={t("site.rum.vitalsHint")}
        action={
          <div className="flex flex-wrap items-center gap-1" role="group" aria-label={t("site.rum.range")}>
            {WINDOWS.map((w) => (
              <Button key={w} size="sm" variant={window_ === w ? "primary" : "secondary"} onClick={() => setWindow(w)}>
                {t(`site.rum.windows.${w}`)}
              </Button>
            ))}
          </div>
        }
      />
      <ErrorBand message={error} />
      {summary === null ? (
        <LoadingRows rows={4} />
      ) : empty ? (
        <EmptyState className="nq-empty" icon={GaugeIcon} title={t("site.rum.emptyTitle")} description={t("site.rum.emptyHint")} />
      ) : (
        <>
          <MetricCards t={t} metrics={summary.metrics} />
          <div className="nq-band py-6">
            <TrendChart points={series ?? []} />
          </div>
          <PathsTable t={t} rows={paths ?? []} />
        </>
      )}
    </section>
  )
}

type TFunc = (key: string, vars?: Record<string, string | number>, fallback?: string) => string

function MetricCards({ t, metrics }: { t: TFunc; metrics: Metrics }) {
  const units: Record<(typeof METRIC_KEYS)[number], string> = { lcp: "ms", cls: "", inp: "ms", fcp: "ms", ttfb: "ms" }
  return (
    <StatGrid className="nq-stats">
      {METRIC_KEYS.map((k) => {
        const m = metrics[k]
        const value = m.samples === 0 ? "—" : k === "cls" ? m.p75.toFixed(2) : `${Math.round(m.p75)} ${units[k]}`
        return (
          <StatCard
            key={k}
            icon={<span aria-hidden className={`size-2.5 rounded-full ${ratingDot(m.rating)}`} />}
            label={t(`site.rum.metrics.${k}`)}
            value={
              <span className="flex flex-col gap-1">
                <span dir="ltr" className={`text-start ${m.samples === 0 ? "text-muted-foreground" : ""}`}>
                  {value}
                </span>
                <span className="flex items-center gap-2 text-xs font-normal">
                  <span className={ratingTone(m.rating)}>{t(`site.rum.rating.${m.rating}`)}</span>
                  <span className="text-muted-foreground">{t("site.rum.samples", { count: m.samples })}</span>
                </span>
              </span>
            }
          />
        )
      })}
    </StatGrid>
  )
}

/**
 * LCP p75 over time: a hand-rolled SVG line chart (same approach as the analytics tab's
 * RequestsChart — no chart library in this codebase). Always left→right regardless of page
 * direction.
 */
function TrendChart({ points }: { points: Point[] }) {
  const w = 960
  const h = 220
  const pad = { top: 10, bottom: 24 }
  const innerH = h - pad.top - pad.bottom
  const { path, area, bars, max } = useMemo(() => {
    const values = points.map((p) => p.metrics.lcp.p75)
    const max = Math.max(1, ...values)
    const step = points.length > 1 ? w / (points.length - 1) : w
    const coords = points.map((p, i) => [i * step, pad.top + innerH - (p.metrics.lcp.p75 / max) * innerH] as const)
    const path = coords.map(([x, y], i) => `${i ? "L" : "M"}${x.toFixed(1)},${y.toFixed(1)}`).join(" ")
    const area = coords.length > 1 ? `${path} L${coords[coords.length - 1][0]},${h - pad.bottom} L0,${h - pad.bottom} Z` : ""
    return { path, area, bars: coords.map(([x, y], i) => ({ x, y, p: points[i] })), max }
  }, [points, innerH])

  if (points.length === 0) return null
  const first = points[0]?.bucket
  const last = points[points.length - 1]?.bucket
  return (
    <div dir="ltr">
      <svg viewBox={`0 0 ${w} ${h}`} preserveAspectRatio="none" className="block h-56 w-full" role="img" aria-label="LCP p75 over time">
        <line x1={0} y1={pad.top + innerH / 2} x2={w} y2={pad.top + innerH / 2} stroke="currentColor" strokeOpacity={0.08} strokeDasharray="4 4" />
        <line x1={0} y1={h - pad.bottom} x2={w} y2={h - pad.bottom} stroke="currentColor" strokeOpacity={0.15} />
        {area ? <path d={area} fill="var(--nq-brand)" opacity={0.14} /> : null}
        {points.length > 1 ? <path d={path} fill="none" stroke="var(--nq-brand)" strokeWidth={1.5} vectorEffect="non-scaling-stroke" /> : null}
        {bars.map(({ x, y, p }, i) => (
          <circle key={i} cx={x} cy={y} r={points.length > 60 ? 0 : 2} fill="var(--nq-brand)" vectorEffect="non-scaling-stroke">
            <title>
              {p.bucket}: {Math.round(p.metrics.lcp.p75)} ms
            </title>
          </circle>
        ))}
      </svg>
      <div className="mt-1 flex justify-between text-xs text-muted-foreground">
        <time dateTime={first}>{first ? new Date(first).toLocaleString() : ""}</time>
        <span>max {Math.round(max).toLocaleString()} ms</span>
        <time dateTime={last}>{last ? new Date(last).toLocaleString() : ""}</time>
      </div>
    </div>
  )
}

function PathsTable({ t, rows }: { t: TFunc; rows: PathBreakdown[] }) {
  return (
    <section aria-labelledby="rum-paths-h">
      <SectionTitle id="rum-paths-h" title={t("site.rum.paths")} />
      {rows.length === 0 ? (
        <p className="nq-band py-4 text-sm text-muted-foreground">{t("site.rum.noPaths")}</p>
      ) : (
        <NasaqTable
          data={rows}
          rowId={(r) => r.path}
          rowLabel={(r) => r.path}
          label={t("site.rum.paths")}
          defaultSort={{ id: "samples", direction: "desc" }}
          columns={[
            {
              id: "path",
              header: t("site.rum.path"),
              cell: (r) => (
                <span dir="ltr" className="block max-w-md truncate font-mono text-xs">
                  {r.path}
                </span>
              ),
              searchValue: (r) => r.path,
            },
            {
              id: "samples",
              header: t("site.rum.samplesHeader"),
              align: "end",
              cell: (r) => <Badge variant="outline">{r.samples.toLocaleString()}</Badge>,
              sortValue: (r) => r.samples,
            },
            {
              id: "lcp",
              header: "LCP",
              align: "end",
              cell: (r) => (
                <span dir="ltr" className={`text-xs ${ratingTone(r.metrics.lcp.rating)}`}>
                  {r.metrics.lcp.samples ? `${Math.round(r.metrics.lcp.p75)} ms` : "—"}
                </span>
              ),
              sortValue: (r) => r.metrics.lcp.p75,
            },
            {
              id: "cls",
              header: "CLS",
              align: "end",
              cell: (r) => (
                <span dir="ltr" className={`text-xs ${ratingTone(r.metrics.cls.rating)}`}>
                  {r.metrics.cls.samples ? r.metrics.cls.p75.toFixed(2) : "—"}
                </span>
              ),
              sortValue: (r) => r.metrics.cls.p75,
            },
            {
              id: "inp",
              header: "INP",
              align: "end",
              cell: (r) => (
                <span dir="ltr" className={`text-xs ${ratingTone(r.metrics.inp.rating)}`}>
                  {r.metrics.inp.samples ? `${Math.round(r.metrics.inp.p75)} ms` : "—"}
                </span>
              ),
              sortValue: (r) => r.metrics.inp.p75,
            },
          ]}
        />
      )}
    </section>
  )
}
