"use client"

import { Alert, Badge, Button, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useState } from "react"
import { InfoIcon, RefreshCwIcon, XIcon } from "lucide-react"

import { ConfirmButton } from "@/components/confirm-button"
import { DbMetricsHistory } from "@/components/databases/metrics-history"
import { ErrorBand, LoadingRows, TableBand } from "@/components/states"



import { apiFetch } from "@/lib/api"
import { humanBytes } from "@/lib/databases"
import { useI18n } from "@/lib/i18n"

/*
Insights (MH-575 F6): DB metrics and slow queries for the Postgres/MySQL services Hosbah runs, over
internal/dbsvc/insights.go (the "db.insights" / "db.cancel_query" agent ops). Everything here is
agent-owned, fixed SQL — nothing a member types reaches the engine — but the numbers and long-running
query text can still hold values a member typed, so this needs the same owner/admin tier the SQL
console does (its caller only mounts this component when `manage` is true, same as DataConsole).
Postgres and MySQL/MariaDB only; the backend refuses redis/minio with the same "not supported" shape
db.query uses, and an imported service with no credentials answers the same way through preflightQuery
— so this component just renders whatever error apiFetch surfaces for that case, same as DataConsole.
The KPI tiles below are the current reading, refreshed on demand or every 15s; DbMetricsHistory (its
own component, sharing H1's server-metrics chart and range picker) draws the sampled history above them.
*/

type SizeInfo = { name: string; size_bytes: number }
type LongQuery = {
  pid: string
  duration_seconds: number
  state?: string
  query?: string
  database?: string
  user?: string
}
type TopStatement = { query: string; calls: number; total_time_ms: number; mean_time_ms: number; rows: number }
type Insights = {
  kind: string
  connections: number
  max_connections: number
  connections_by_state?: Record<string, number>
  xact_commit?: number
  xact_rollback?: number
  cache_hit_ratio: number
  deadlocks?: number
  temp_bytes?: number
  databases?: SizeInfo[]
  long_running?: LongQuery[]
  locks_waiting: number
  stat_statements_available: boolean
  stat_statements_hint?: string
  top_statements?: TopStatement[]
  threads_connected?: number
  threads_running?: number
  questions?: number
  slow_queries?: number
  uptime_seconds?: number
  slow_query_log?: string
}

type SortKey = "total_time_ms" | "mean_time_ms" | "calls"

function fmtDuration(seconds: number): string {
  if (seconds < 60) return `${seconds.toFixed(1)}s`
  const m = Math.floor(seconds / 60)
  const s = Math.round(seconds % 60)
  return `${m}m${s}s`
}

function fmtUptime(seconds: number): string {
  const d = Math.floor(seconds / 86400)
  const h = Math.floor((seconds % 86400) / 3600)
  if (d > 0) return `${d}d ${h}h`
  const m = Math.floor((seconds % 3600) / 60)
  return `${h}h ${m}m`
}

function totalSize(databases: SizeInfo[] | undefined): number {
  return (databases ?? []).reduce((sum, d) => sum + (d.size_bytes || 0), 0)
}

export function InsightsPanel({ teamSlug, serviceRef, serviceName }: { teamSlug: string; serviceRef: string; serviceName: string }) {
  const { t } = useI18n()
  const [data, setData] = useState<Insights | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [sortKey, setSortKey] = useState<SortKey>("total_time_ms")

  const load = useCallback(async () => {
    setError(null)
    try {
      const res = await apiFetch<Insights>(`/api/teams/${teamSlug}/data-services/${serviceRef}/insights`)
      setData(res)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setLoading(false)
    }
  }, [teamSlug, serviceRef])

  useEffect(() => {
    void load()
    const id = setInterval(load, 15_000)
    return () => clearInterval(id)
  }, [load])

  const cancel = useCallback(
    async (pid: string) => {
      await apiFetch(`/api/teams/${teamSlug}/data-services/${serviceRef}/insights/cancel`, {
        method: "POST",
        body: { id: Number(pid), confirm: serviceName },
      })
      await load()
    },
    [teamSlug, serviceRef, serviceName, load],
  )

  if (loading && !data) return <LoadingRows rows={3} />
  if (error && !data) return <ErrorBand message={error} />
  if (!data) return null

  const isMysql = data.kind === "mysql"
  const connByState = Object.entries(data.connections_by_state ?? {})
  const topStatements = [...(data.top_statements ?? [])].sort((a, b) => b[sortKey] - a[sortKey])
  const longRunning = data.long_running ?? []

  return (
    <div className="flex flex-col">
      <div className="flex flex-wrap items-center justify-end gap-2 border-b border-border px-6 py-3">
        <Button size="sm" variant="secondary" onClick={() => void load()} disabled={loading}>
          <RefreshCwIcon className={loading ? "animate-spin" : undefined} />
          {t("databases.insights.refresh")}
        </Button>
      </div>

      <ErrorBand message={error} />

      <DbMetricsHistory teamSlug={teamSlug} serviceRef={serviceRef} kind={data.kind} />

      {/* KPI tiles */}
      <div className="grid grid-cols-2 gap-px border-b border-border bg-border sm:grid-cols-3 lg:grid-cols-4">
        <Tile
          label={t("databases.insights.connections")}
          value={t("databases.insights.connectionsOf", { used: data.connections, max: data.max_connections })}
          hint={connByState.length ? t("databases.insights.connectionsByState", { list: connByState.map(([k, v]) => `${k}: ${v}`).join(", ") }) : undefined}
        />
        <Tile label={t("databases.insights.cacheHit")} value={`${data.cache_hit_ratio.toFixed(1)}%`} />
        <Tile label={t("databases.insights.totalSize")} value={humanBytes(totalSize(data.databases))} />
        {isMysql ? (
          <Tile label={t("databases.insights.throughputMysql")} value={(data.questions ?? 0).toLocaleString()} hint={t("databases.insights.throughputMysqlHint")} />
        ) : (
          <Tile
            label={t("databases.insights.throughput")}
            value={`${(data.xact_commit ?? 0).toLocaleString()} / ${(data.xact_rollback ?? 0).toLocaleString()}`}
            hint={t("databases.insights.throughputHint")}
          />
        )}
        {isMysql ? (
          <Tile label={t("databases.insights.uptime")} value={fmtUptime(data.uptime_seconds ?? 0)} />
        ) : (
          <Tile label={t("databases.insights.deadlocks")} value={(data.deadlocks ?? 0).toLocaleString()} />
        )}
        <Tile label={t("databases.insights.locksWaiting")} value={data.locks_waiting.toLocaleString()} />
        {isMysql ? (
          <Tile
            label={t("databases.insights.slowQueries")}
            value={(data.slow_queries ?? 0).toLocaleString()}
            hint={data.slow_query_log === "ON" ? t("databases.insights.slowQueryLogOn") : t("databases.insights.slowQueryLogOff")}
          />
        ) : null}
      </div>

      {/* Long-running queries */}
      <div className="border-b border-border px-6 py-3">
        <h3 className="text-sm font-medium text-foreground">{t("databases.insights.longRunningTitle")}</h3>
      </div>
      {longRunning.length === 0 ? (
        <p className="border-b border-border px-6 py-4 text-sm text-muted-foreground">{t("databases.insights.noLongRunning")}</p>
      ) : (
        <TableBand>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("databases.insights.pid")}</TableHead>
                <TableHead>{t("databases.insights.duration")}</TableHead>
                <TableHead>{t("databases.insights.state")}</TableHead>
                <TableHead>{t("databases.insights.database")}</TableHead>
                <TableHead>{t("databases.insights.user")}</TableHead>
                <TableHead>{t("databases.insights.query")}</TableHead>
                <TableHead />
              </TableRow>
            </TableHeader>
            <TableBody>
              {longRunning.map((q) => (
                <TableRow key={q.pid}>
                  <TableCell dir="ltr" className="font-mono text-xs">
                    {q.pid}
                  </TableCell>
                  <TableCell dir="ltr">{fmtDuration(q.duration_seconds)}</TableCell>
                  <TableCell>{q.state ?? "—"}</TableCell>
                  <TableCell dir="ltr">{q.database ?? "—"}</TableCell>
                  <TableCell dir="ltr">{q.user ?? "—"}</TableCell>
                  <TableCell dir="ltr" className="max-w-80 truncate font-mono text-xs" title={q.query}>
                    {q.query ?? "—"}
                  </TableCell>
                  <TableCell className="text-end">
                    <ConfirmButton
                      size="sm"
                      label={t("databases.insights.cancelQuery")}
                      title={t("databases.insights.cancelTitle")}
                      description={t("databases.insights.cancelHint", { pid: q.pid })}
                      confirmLabel={t("databases.insights.cancelQuery")}
                      icon={<XIcon />}
                      onConfirm={() => cancel(q.pid)}
                    />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </TableBand>
      )}

      {/* Top statements */}
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border px-6 py-3">
        <h3 className="text-sm font-medium text-foreground">{t("databases.insights.topStatementsTitle")}</h3>
        <span className="text-xs text-muted-foreground">{t("databases.insights.topStatementsHint")}</span>
      </div>
      {!data.stat_statements_available ? (
        <Alert tone="info" title={t("databases.insights.statStatementsUnavailableTitle")} className="mx-6 my-4">
<div dir="ltr">{data.stat_statements_hint}</div>
</Alert>
      ) : topStatements.length === 0 ? (
        <p className="border-b border-border px-6 py-4 text-sm text-muted-foreground">{t("databases.insights.noLongRunning")}</p>
      ) : (
        <TableBand>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("databases.insights.query")}</TableHead>
                <SortableHead label={t("databases.insights.calls")} active={sortKey === "calls"} onClick={() => setSortKey("calls")} />
                <SortableHead label={t("databases.insights.totalTime")} active={sortKey === "total_time_ms"} onClick={() => setSortKey("total_time_ms")} />
                <SortableHead label={t("databases.insights.meanTime")} active={sortKey === "mean_time_ms"} onClick={() => setSortKey("mean_time_ms")} />
                <TableHead>{t("databases.insights.rows")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {topStatements.map((s, i) => (
                <TableRow key={i}>
                  <TableCell dir="ltr" className="max-w-96 truncate font-mono text-xs" title={s.query}>
                    {s.query}
                  </TableCell>
                  <TableCell dir="ltr">{s.calls.toLocaleString()}</TableCell>
                  <TableCell dir="ltr">{s.total_time_ms.toLocaleString()} ms</TableCell>
                  <TableCell dir="ltr">{s.mean_time_ms.toLocaleString()} ms</TableCell>
                  <TableCell dir="ltr">{s.rows.toLocaleString()}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </TableBand>
      )}
    </div>
  )
}

function Tile({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return (
    <div className="flex min-w-0 flex-col gap-1 bg-background px-6 py-4">
      <span className="text-xs text-muted-foreground">{label}</span>
      <span dir="ltr" className="text-lg font-semibold tabular-nums text-foreground">
        {value}
      </span>
      {hint ? (
        <span dir="ltr" className="truncate text-[11px] text-muted-foreground" title={hint}>
          {hint}
        </span>
      ) : null}
    </div>
  )
}

function SortableHead({ label, active, onClick }: { label: string; active: boolean; onClick: () => void }) {
  return (
    <TableHead>
      <button type="button" onClick={onClick} className="flex items-center gap-1 hover:text-foreground">
        {label}
        {active ? <Badge variant="neutral" className="px-1 py-0 text-[9px]">▼</Badge> : null}
      </button>
    </TableHead>
  )
}
