"use client"

import { Badge, Button, Checkbox, EmptyState, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useState } from "react"
import { AlertTriangleIcon, ArrowLeftIcon, BugIcon, ChevronDownIcon, ChevronUpIcon, WaypointsIcon } from "lucide-react"

import { CopyField } from "@/components/copy-field"
import { useErrorText } from "@/components/form-error"
import { useLoadedSite } from "@/components/site/context"
import { ErrorBand, LoadingRows, SectionTitle, SettingsRow, TableBand } from "@/components/states"





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
    <div data-testid="apm">
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
        <EmptyState className="border-b" icon={WaypointsIcon} title={t("site.apm.emptyEndpointsTitle")} description={t("site.apm.emptyEndpointsHint")} />
      ) : (
        <TableBand>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("site.apm.method")}</TableHead>
                <TableHead>{t("site.apm.route")}</TableHead>
                <TableHead className="text-end">{t("site.apm.samplesHeader")}</TableHead>
                <TableHead className="text-end">p50</TableHead>
                <TableHead className="text-end">p95</TableHead>
                <TableHead className="text-end">p99</TableHead>
                <TableHead className="text-end">{t("site.apm.errors")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {endpoints.map((e) => (
                <TableRow
                  key={`${e.method} ${e.route}`}
                  className="cursor-pointer"
                  onClick={() => setSelected({ method: e.method, route: e.route })}
                >
                  <TableCell dir="ltr" className="font-mono text-xs">
                    {e.method}
                  </TableCell>
                  <TableCell dir="ltr" className="max-w-md truncate font-mono text-xs">
                    {e.route}
                  </TableCell>
                  <TableCell className="text-end">
                    <Badge variant="outline">{e.samples.toLocaleString()}</Badge>
                  </TableCell>
                  <TableCell dir="ltr" className="text-end text-xs">
                    {Math.round(e.p50_ms)} ms
                  </TableCell>
                  <TableCell dir="ltr" className="text-end text-xs">
                    <div className="flex items-center justify-end gap-2">
                      <span className="h-1.5 w-16 overflow-hidden rounded-full bg-border" aria-hidden>
                        <span
                          className="block h-full rounded-full bg-nq-brand"
                          style={{ width: `${Math.min(100, (e.p95_ms / maxP95) * 100)}%` }}
                        />
                      </span>
                      {Math.round(e.p95_ms)} ms
                    </div>
                  </TableCell>
                  <TableCell dir="ltr" className="text-end text-xs">
                    {Math.round(e.p99_ms)} ms
                  </TableCell>
                  <TableCell className="text-end">
                    {e.errors > 0 ? <Badge variant="danger">{e.errors}</Badge> : <span className="text-xs text-muted-foreground">0</span>}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </TableBand>
      )}
    </section>
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
      <div className="border-b border-border px-6 py-3">
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
        <p className="border-b border-border px-6 py-4 text-sm text-muted-foreground">{t("site.apm.noTraces")}</p>
      ) : (
        <TableBand>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("site.apm.traceId")}</TableHead>
                <TableHead className="text-end">{t("site.apm.duration")}</TableHead>
                <TableHead className="text-end">{t("site.apm.status")}</TableHead>
                <TableHead className="text-end">{t("site.apm.startedAt")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {traces.map((tr) => (
                <TableRow key={tr.trace_id} className="cursor-pointer" onClick={() => setTraceID(tr.trace_id)}>
                  <TableCell dir="ltr" className="max-w-xs truncate font-mono text-xs">
                    {tr.trace_id}
                  </TableCell>
                  <TableCell dir="ltr" className="text-end text-xs">
                    {Math.round(tr.duration_ms)} ms
                  </TableCell>
                  <TableCell className="text-end">
                    {tr.error ? (
                      <Badge variant="danger">{tr.http_status ?? t("site.apm.error")}</Badge>
                    ) : (
                      <Badge variant="outline">{tr.http_status ?? "—"}</Badge>
                    )}
                  </TableCell>
                  <TableCell dir="ltr" className="text-end text-xs text-muted-foreground">
                    {new Date(tr.start_time).toLocaleString()}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </TableBand>
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
      <div className="border-b border-border px-6 py-3">
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
        <div className="space-y-2 border-b border-border px-6 py-4" dir="ltr">
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
        <EmptyState className="border-b" icon={BugIcon} title={t("site.apm.emptyErrorsTitle")} description={t("site.apm.emptyErrorsHint")} />
      ) : (
        <div className="divide-y divide-border border-b border-border">
          {issues.map((issue) => (
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
  return (
    <div className="px-6 py-4">
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
          {manage ? (
            <div className="flex flex-wrap gap-2">
              {ISSUE_STATUSES.filter((s) => s !== issue.status).map((s) => (
                <Button key={s} size="sm" variant="secondary" disabled={busy} onClick={() => onStatus(s)}>
                  {t(`site.apm.setStatus.${s}`)}
                </Button>
              ))}
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  )
}
