"use client"

import { Badge, Button, Checkbox, EmptyState, StatCard, StatGrid } from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useState } from "react"
import { ActivityIcon, AlertTriangleIcon, ArrowLeftIcon, BugIcon, ChevronDownIcon, ChevronUpIcon, GaugeIcon, ListTreeIcon, TimerIcon, WaypointsIcon } from "lucide-react"

import { CopyField } from "@/components/copy-field"
import { useErrorText } from "@/components/form-error"
import { useLoadedSite } from "@/components/site/context"
import { LoadMore, useInfiniteLimit } from "@/components/lists/infinite"
import { NasaqTable } from "@/components/lists/nasaq-table"
import { RowMenu, RowWithActions, type RowAction } from "@/components/lists/row-actions"
import { ErrorBand, LoadingRows, SectionTitle, SettingsRow } from "@/components/states"





import { apiFetch } from "@/lib/api"
import { useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

type ApmSettings = {
  enabled: boolean
  has_key: boolean
  key?: string
  snippet?: string
  snippet_url?: string
}
type Endpoint = { method: string; route: string; samples: number; p50_ms: number; p95_ms: number; p99_ms: number; errors: number }
type TraceSummary = { trace_id: string; duration_ms: number; error: boolean; http_status?: number; start_time: string }
type Span = {
  span_id: string
  parent_span_id?: string
  name: string
  kind: string
  service: string
  http_method?: string
  http_route?: string
  http_status?: number
  duration_ms: number
  error: boolean
  start_time: string
}
type Issue = { id: string; type: string; message: string; first_seen: string; last_seen: string; count: number; status: string }

const WINDOWS = ["1h", "24h", "7d"] as const

/**
 * APM: traces and exceptions (MH-575 H7): a per-app toggle and rotatable secret ingest key
 * (settings, auto-injected into the app's own env on its next deploy), slow-endpoint summaries
 * built from spans the app's own OTel SDK exported, a trace waterfall drill-down, and grouped
 * exceptions with status actions.
 */
export function SiteApm() {
  const { t } = useI18n()
  const { manage } = useTeam()
  const { path } = useLoadedSite()
  const errorText = useErrorText()
  const base = `${path}/apm`

  const [settings, setSettings] = useState<ApmSettings | null>(null)
  const [settingsError, setSettingsError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [rotateBusy, setRotateBusy] = useState(false)

  const loadSettings = useCallback(() => {
    apiFetch<ApmSettings>(`${base}/settings`)
      .then(setSettings)
      .catch((err) => setSettingsError(errorText(err)))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [base])
  useEffect(loadSettings, [loadSettings])

  async function toggle(enabled: boolean) {
    setBusy(true)
    setSettingsError(null)
    try {
      const s = await apiFetch<ApmSettings>(`${base}/settings`, { method: "PUT", body: { enabled } })
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
      const s = await apiFetch<ApmSettings>(`${base}/settings/rotate`, { method: "POST" })
      setSettings(s)
    } catch (err) {
      setSettingsError(errorText(err))
    } finally {
      setRotateBusy(false)
    }
  }

  return (
    <div data-testid="apm" data-stack>
      <section aria-labelledby="apm-settings-h">
        <SectionTitle id="apm-settings-h" title={t("site.apm.title")} hint={t("site.apm.hint")} />
        {settings === null ? (
          <LoadingRows rows={2} />
        ) : (
          <SettingsRow label={t("site.apm.enable")} htmlFor="apm-enable" hint={!manage ? t("common.readOnly") : t("site.apm.enableHint")}>
            <Checkbox id="apm-enable" checked={settings.enabled} disabled={!manage || busy} onCheckedChange={(v) => toggle(!!v)} />
          </SettingsRow>
        )}
        <ErrorBand message={settingsError} />
        {settings?.snippet ? (
          <SettingsRow label={t("site.apm.snippet")} hint={manage ? t("site.apm.rotateHint") : undefined}>
            <CopyField value={settings.snippet} block />
            <p className="flex items-start gap-2 text-xs text-nq-fg-body">
              <AlertTriangleIcon className="mt-0.5 size-3.5 shrink-0 text-nq-brand" />
              {t("site.apm.once")}
            </p>
            <p className="text-xs text-muted-foreground">{t("site.apm.autoInjectedNote")}</p>
            {manage ? (
              <div>
                <Button variant="secondary" disabled={rotateBusy} onClick={rotate}>
                  {rotateBusy ? t("common.working") : t("site.apm.rotate")}
                </Button>
              </div>
            ) : null}
          </SettingsRow>
        ) : settings?.has_key ? (
          <SettingsRow label={t("site.apm.snippet")} hint={t("site.apm.snippetHiddenHint")}>
            <p className="text-sm text-muted-foreground">{t("site.apm.snippetHidden")}</p>
            <p className="text-xs text-muted-foreground">{t("site.apm.autoInjectedNote")}</p>
            {manage ? (
              <div>
                <Button variant="secondary" disabled={rotateBusy} onClick={rotate}>
                  {rotateBusy ? t("common.working") : t("site.apm.rotate")}
                </Button>
              </div>
            ) : null}
          </SettingsRow>
        ) : null}
      </section>

      {settings?.has_key ? <ApmEndpoints base={base} /> : null}
      {settings?.has_key ? <ApmErrors base={base} /> : null}
    </div>
  )
}

type TFunc = (key: string, vars?: Record<string, string | number>, fallback?: string) => string

function ApmEndpoints({ base }: { base: string }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [window_, setWindow] = useState<(typeof WINDOWS)[number]>("24h")
  const [endpoints, setEndpoints] = useState<Endpoint[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [selected, setSelected] = useState<{ method: string; route: string } | null>(null)

  const load = useCallback(() => {
    apiFetch<{ window: string; endpoints: Endpoint[] }>(`${base}/endpoints?window=${window_}`)
      .then((r) => {
        setEndpoints(r.endpoints ?? [])
        setError(null)
      })
      .catch((err) => setError(errorText(err)))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [base, window_])

  useEffect(() => {
    setEndpoints(null)
    load()
    const id = setInterval(load, 30_000)
    return () => clearInterval(id)
  }, [load])

  const maxP95 = Math.max(1, ...(endpoints ?? []).map((e) => e.p95_ms))

  return (
    <section aria-labelledby="apm-endpoints-h">
      <SectionTitle
        id="apm-endpoints-h"
        title={t("site.apm.endpointsTitle")}
        hint={t("site.apm.endpointsHint")}
        action={
          <div className="flex flex-wrap items-center gap-1" role="group" aria-label={t("site.apm.range")}>
            {WINDOWS.map((w) => (
              <Button key={w} size="sm" variant={window_ === w ? "primary" : "secondary"} onClick={() => setWindow(w)}>
                {t(`site.apm.windows.${w}`)}
              </Button>
            ))}
          </div>
        }
      />
      <ErrorBand message={error} />
      {selected ? (
        <ApmTraces base={base} method={selected.method} route={selected.route} onBack={() => setSelected(null)} />
      ) : endpoints === null ? (
        <LoadingRows rows={4} />
      ) : endpoints.length === 0 ? (
        <EmptyState className="nq-empty" icon={WaypointsIcon} title={t("site.apm.emptyEndpointsTitle")} description={t("site.apm.emptyEndpointsHint")} />
      ) : (
        <>
          <EndpointTotals endpoints={endpoints} />
          <NasaqTable
            data={endpoints}
            rowId={(e) => `${e.method} ${e.route}`}
            rowLabel={(e) => `${e.method} ${e.route}`}
            label={t("site.apm.endpointsTitle")}
            search={endpoints.length > 8 ? t("site.apm.route") : undefined}
            defaultSort={{ id: "samples", direction: "desc" }}
            onRowClick={(e) => setSelected({ method: e.method, route: e.route })}
            rowActions={(e) => [{ id: "traces", label: t("site.apm.viewTraces"), icon: ListTreeIcon, onSelect: () => setSelected({ method: e.method, route: e.route }) }]}
            columns={[
              { id: "method", header: t("site.apm.method"), cell: (e) => <span dir="ltr" className="font-mono text-xs">{e.method}</span>, sortValue: (e) => e.method },
              {
                id: "route",
                header: t("site.apm.route"),
                cell: (e) => (
                  <span dir="ltr" className="block max-w-md truncate font-mono text-xs">
                    {e.route}
                  </span>
                ),
                sortValue: (e) => e.route,
                searchValue: (e) => e.route,
              },
              { id: "samples", header: t("site.apm.samplesHeader"), align: "end", cell: (e) => <Badge variant="outline">{e.samples.toLocaleString()}</Badge>, sortValue: (e) => e.samples },
              { id: "p50", header: "p50", align: "end", cell: (e) => <span dir="ltr" className="text-xs">{Math.round(e.p50_ms)} ms</span>, sortValue: (e) => e.p50_ms },
              {
                id: "p95",
                header: "p95",
                align: "end",
                cell: (e) => (
                  <span dir="ltr" className="flex items-center justify-end gap-2 text-xs">
                    <span className="h-1.5 w-16 overflow-hidden rounded-full bg-border" aria-hidden>
                      <span className="block h-full rounded-full bg-nq-brand" style={{ width: `${Math.min(100, (e.p95_ms / maxP95) * 100)}%` }} />
                    </span>
                    {Math.round(e.p95_ms)} ms
                  </span>
                ),
                sortValue: (e) => e.p95_ms,
              },
              { id: "p99", header: "p99", align: "end", cell: (e) => <span dir="ltr" className="text-xs">{Math.round(e.p99_ms)} ms</span>, sortValue: (e) => e.p99_ms },
              {
                id: "errors",
                header: t("site.apm.errors"),
                align: "end",
                cell: (e) => (e.errors > 0 ? <Badge variant="danger">{e.errors}</Badge> : <span className="text-xs text-muted-foreground">0</span>),
                sortValue: (e) => e.errors,
              },
            ]}
          />
        </>
      )}
    </section>
  )
}

/** The window at a glance: requests, errors, and the slowest p95 and p99 across the endpoints. */
function EndpointTotals({ endpoints }: { endpoints: Endpoint[] }) {
  const { t } = useI18n()
  const samples = endpoints.reduce((n, e) => n + e.samples, 0)
  const errors = endpoints.reduce((n, e) => n + e.errors, 0)
  const p95 = Math.max(0, ...endpoints.map((e) => e.p95_ms))
  const p99 = Math.max(0, ...endpoints.map((e) => e.p99_ms))
  return (
    <StatGrid className="nq-stats">
      <StatCard icon={<ActivityIcon />} label={t("site.apm.samplesHeader")} value={samples} />
      <StatCard
        icon={<BugIcon />}
        label={t("site.apm.errors")}
        value={
          <span dir="ltr">
            {errors.toLocaleString()}
            {samples > 0 ? <span className="ms-2 text-xs font-normal text-muted-foreground">{((errors / samples) * 100).toFixed(1)}%</span> : null}
          </span>
        }
      />
      <StatCard icon={<GaugeIcon />} label="p95" value={<span dir="ltr">{Math.round(p95)} ms</span>} />
      <StatCard icon={<TimerIcon />} label="p99" value={<span dir="ltr">{Math.round(p99)} ms</span>} />
    </StatGrid>
  )
}

function ApmTraces({ base, method, route, onBack }: { base: string; method: string; route: string; onBack: () => void }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [traces, setTraces] = useState<TraceSummary[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [traceID, setTraceID] = useState<string | null>(null)

  useEffect(() => {
    setTraces(null)
    apiFetch<{ traces: TraceSummary[] }>(`${base}/traces?method=${encodeURIComponent(method)}&route=${encodeURIComponent(route)}`)
      .then((r) => setTraces(r.traces ?? []))
      .catch((err) => setError(errorText(err)))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [base, method, route])

  if (traceID) {
    return <ApmWaterfall base={base} traceID={traceID} onBack={() => setTraceID(null)} />
  }

  return (
    <div>
      <div className="nq-band py-3">
        <Button variant="ghost" size="sm" onClick={onBack}>
          <ArrowLeftIcon className="rtl:rotate-180" />
          {t("site.apm.backToEndpoints")}
        </Button>
        <p dir="ltr" className="mt-1 font-mono text-xs text-muted-foreground">
          {method} {route}
        </p>
      </div>
      <ErrorBand message={error} />
      {traces === null ? (
        <LoadingRows rows={4} />
      ) : traces.length === 0 ? (
        <p className="nq-band py-4 text-sm text-muted-foreground">{t("site.apm.noTraces")}</p>
      ) : (
        <NasaqTable
          data={traces}
          rowId={(tr) => tr.trace_id}
          rowLabel={(tr) => tr.trace_id}
          label={t("site.apm.traceId")}
          defaultSort={{ id: "started", direction: "desc" }}
          onRowClick={(tr) => setTraceID(tr.trace_id)}
          rowActions={(tr) => [{ id: "open", label: t("site.apm.viewTrace"), icon: WaypointsIcon, onSelect: () => setTraceID(tr.trace_id) }]}
          columns={[
            {
              id: "trace",
              header: t("site.apm.traceId"),
              cell: (tr) => (
                <span dir="ltr" className="block max-w-xs truncate font-mono text-xs">
                  {tr.trace_id}
                </span>
              ),
              searchValue: (tr) => tr.trace_id,
            },
            { id: "duration", header: t("site.apm.duration"), align: "end", cell: (tr) => <span dir="ltr" className="text-xs">{Math.round(tr.duration_ms)} ms</span>, sortValue: (tr) => tr.duration_ms },
            {
              id: "status",
              header: t("site.apm.status"),
              align: "end",
              cell: (tr) =>
                tr.error ? <Badge variant="danger">{tr.http_status ?? t("site.apm.error")}</Badge> : <Badge variant="outline">{tr.http_status ?? "—"}</Badge>,
              sortValue: (tr) => tr.http_status ?? 0,
            },
            {
              id: "started",
              header: t("site.apm.startedAt"),
              align: "end",
              cell: (tr) => (
                <span dir="ltr" className="text-xs text-muted-foreground">
                  {new Date(tr.start_time).toLocaleString()}
                </span>
              ),
              sortValue: (tr) => tr.start_time,
            },
          ]}
        />
      )}
    </div>
  )
}

function ApmWaterfall({ base, traceID, onBack }: { base: string; traceID: string; onBack: () => void }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [spans, setSpans] = useState<Span[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    setSpans(null)
    apiFetch<{ spans: Span[] }>(`${base}/traces/${encodeURIComponent(traceID)}`)
      .then((r) => setSpans(r.spans ?? []))
      .catch((err) => setError(errorText(err)))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [base, traceID])

  const starts = (spans ?? []).map((s) => new Date(s.start_time).getTime())
  const min = starts.length ? Math.min(...starts) : 0
  const totalMs = spans && spans.length ? Math.max(1, ...spans.map((s) => new Date(s.start_time).getTime() - min + s.duration_ms)) : 1

  return (
    <div>
      <div className="nq-band py-3">
        <Button variant="ghost" size="sm" onClick={onBack}>
          <ArrowLeftIcon className="rtl:rotate-180" />
          {t("site.apm.backToTraces")}
        </Button>
        <p dir="ltr" className="mt-1 font-mono text-xs text-muted-foreground">
          {traceID}
        </p>
      </div>
      <ErrorBand message={error} />
      {spans === null ? (
        <LoadingRows rows={3} />
      ) : (
        <div className="space-y-2 nq-band py-4" dir="ltr">
          {spans.map((s) => {
            const offset = ((new Date(s.start_time).getTime() - min) / totalMs) * 100
            const width = Math.max(1, (s.duration_ms / totalMs) * 100)
            return (
              <div key={s.span_id} className="flex items-center gap-3">
                <div className="w-48 shrink-0 truncate text-xs">
                  <span className="font-mono">{s.name}</span>
                  <span className="ms-2 text-muted-foreground">{s.kind}</span>
                </div>
                <div className="relative h-4 flex-1 rounded bg-border">
                  <div
                    className={`absolute h-4 rounded ${s.error ? "bg-nq-danger" : "bg-nq-brand"}`}
                    style={{ left: `${offset}%`, width: `${width}%` }}
                    title={`${Math.round(s.duration_ms)} ms`}
                  />
                </div>
                <div className="w-16 shrink-0 text-end text-xs text-muted-foreground">{Math.round(s.duration_ms)} ms</div>
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}

const ISSUE_STATUSES = ["open", "resolved", "ignored"] as const

function ApmErrors({ base }: { base: string }) {
  const { t } = useI18n()
  const { manage } = useTeam()
  const errorText = useErrorText()
  const [issues, setIssues] = useState<Issue[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [expanded, setExpanded] = useState<string | null>(null)
  const [stacktraces, setStacktraces] = useState<Record<string, string>>({})
  const [busy, setBusy] = useState<string | null>(null)
  const { limit, hasMore, more } = useInfiniteLimit(issues?.length ?? 0)

  const load = useCallback(() => {
    apiFetch<{ issues: Issue[] }>(`${base}/issues`)
      .then((r) => {
        setIssues(r.issues ?? [])
        setError(null)
      })
      .catch((err) => setError(errorText(err)))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [base])

  useEffect(() => {
    setIssues(null)
    load()
    const id = setInterval(load, 30_000)
    return () => clearInterval(id)
  }, [load])

  async function toggleExpand(issue: Issue) {
    if (expanded === issue.id) {
      setExpanded(null)
      return
    }
    setExpanded(issue.id)
    if (!stacktraces[issue.id]) {
      try {
        const detail = await apiFetch<Issue & { latest_stacktrace?: string }>(`${base}/issues/${issue.id}`)
        setStacktraces((prev) => ({ ...prev, [issue.id]: detail.latest_stacktrace ?? "" }))
      } catch (err) {
        setStacktraces((prev) => ({ ...prev, [issue.id]: errorText(err) }))
      }
    }
  }

  async function setStatus(issue: Issue, status: (typeof ISSUE_STATUSES)[number]) {
    setBusy(issue.id)
    try {
      const updated = await apiFetch<Issue>(`${base}/issues/${issue.id}/status`, { method: "PUT", body: { status } })
      setIssues((prev) => (prev ? prev.map((i) => (i.id === issue.id ? updated : i)) : prev))
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(null)
    }
  }

  return (
    <section aria-labelledby="apm-errors-h">
      <SectionTitle id="apm-errors-h" title={t("site.apm.errorsTitle")} hint={t("site.apm.errorsHint")} />
      <ErrorBand message={error} />
      {issues === null ? (
        <LoadingRows rows={3} />
      ) : issues.length === 0 ? (
        <EmptyState className="nq-empty" icon={BugIcon} title={t("site.apm.emptyErrorsTitle")} description={t("site.apm.emptyErrorsHint")} />
      ) : (
        <div className="divide-y divide-border nq-box">
          {issues.slice(0, limit).map((issue) => (
            <IssueRow
              key={issue.id}
              t={t}
              issue={issue}
              manage={manage}
              expanded={expanded === issue.id}
              stacktrace={stacktraces[issue.id]}
              busy={busy === issue.id}
              onToggle={() => toggleExpand(issue)}
              onStatus={(s) => setStatus(issue, s)}
            />
          ))}
          <LoadMore hasMore={hasMore} onMore={more} />
        </div>
      )}
    </section>
  )
}

function issueBadgeVariant(status: string): "danger" | "neutral" | "outline" {
  if (status === "open") return "danger"
  if (status === "resolved") return "neutral"
  return "outline"
}

function IssueRow({
  t,
  issue,
  manage,
  expanded,
  stacktrace,
  busy,
  onToggle,
  onStatus,
}: {
  t: TFunc
  issue: Issue
  manage: boolean
  expanded: boolean
  stacktrace?: string
  busy: boolean
  onToggle: () => void
  onStatus: (status: (typeof ISSUE_STATUSES)[number]) => void
}) {
  const actions: RowAction[] = [
    { id: "toggle", label: t(expanded ? "site.apm.hideStacktrace" : "site.apm.showStacktrace"), icon: expanded ? ChevronUpIcon : ChevronDownIcon, onSelect: onToggle },
    ...(manage
      ? ISSUE_STATUSES.filter((s) => s !== issue.status).map((s) => ({
          id: s,
          label: t(`site.apm.setStatus.${s}`),
          group: "status",
          disabled: busy,
          onSelect: () => onStatus(s),
        }))
      : []),
  ]
  return (
    <RowWithActions actions={actions} render={<div className="flex items-start gap-2 px-6 py-4" />}>
      <div className="min-w-0 flex-1">
        <button type="button" className="flex w-full items-start gap-3 text-start" onClick={onToggle}>
          {expanded ? (
            <ChevronUpIcon className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
          ) : (
            <ChevronDownIcon className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
          )}
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-2">
              <Badge variant={issueBadgeVariant(issue.status)}>{t(`site.apm.statuses.${issue.status}`)}</Badge>
              <span dir="ltr" className="truncate font-mono text-sm font-medium">
                {issue.type}
              </span>
              <Badge variant="outline">{t("site.apm.count", { count: issue.count })}</Badge>
            </div>
            <p dir="ltr" className="mt-1 truncate text-sm text-muted-foreground">
              {issue.message}
            </p>
            <p className="mt-1 text-xs text-muted-foreground">
              {t("site.apm.lastSeen")} {new Date(issue.last_seen).toLocaleString()}
            </p>
          </div>
        </button>
        {expanded ? (
          <div className="mt-3 ms-7 space-y-3">
            {stacktrace === undefined ? (
              <LoadingRows rows={1} />
            ) : stacktrace ? (
              <pre dir="ltr" className="overflow-x-auto rounded border border-border bg-secondary p-3 text-start font-mono text-xs">
                {stacktrace}
              </pre>
            ) : (
              <p className="text-xs text-muted-foreground">{t("site.apm.noStacktrace")}</p>
            )}
          </div>
        ) : null}
      </div>
      <RowMenu actions={actions} label={issue.type} />
    </RowWithActions>
  )
}
