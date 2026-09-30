"use client"

import { Button, Checkbox, Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, Table, TableBody, TableCell, TableHead, TableHeader, TableRow, Textarea } from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useState, type KeyboardEvent } from "react"
import { PlayIcon, SearchIcon, TableIcon } from "lucide-react"

import { ConfirmButton } from "@/components/confirm-button"
import { FormError, useErrorText } from "@/components/form-error"
import { ErrorBand, LoadingRows, TableBand } from "@/components/states"
import { Alert, AlertDescription } from "@/components/ui/alert"






import { apiFetch } from "@/lib/api"
import { humanBytes, type DatabaseRow } from "@/lib/databases"
import { useI18n } from "@/lib/i18n"

/*
Data (MH-574 F2): a table browser and SQL console over a data service's databases, through
internal/dbsvc/query.go (the "db.query" agent op). Postgres and MySQL/MariaDB only — the backend
refuses redis and minio with a clear message, so the caller only mounts this for those two kinds.
Requires owner/admin (AbilityRevealSecrets: table contents can hold secrets). "Allow writes" needs the
same ability but is opt-in per statement and confirmed, since a write commits instead of rolling back.
Everything inside the grids and the SQL editor stays dir="ltr" regardless of the page's direction.
*/

type ColumnInfo = { name: string; type?: string }
type TableInfo = { schema?: string; name: string; rows_estimate: number; size_bytes: number }
type QueryResult = { columns: ColumnInfo[]; rows: (string | null)[][]; row_count: number; truncated: boolean; duration_ms: number }

const ROWS_PAGE = 100
const CELL_CLAMP = 200

function qualify(t: TableInfo): string {
  return t.schema ? `${t.schema}.${t.name}` : t.name
}

export function DataConsole({
  teamSlug,
  serviceRef,
  databases,
  manage,
}: {
  teamSlug: string
  serviceRef: string
  databases: DatabaseRow[]
  manage: boolean
}) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [database, setDatabase] = useState(databases[0]?.name ?? "")
  const [tables, setTables] = useState<TableInfo[] | null>(null)
  const [tablesError, setTablesError] = useState<string | null>(null)
  const [search, setSearch] = useState("")
  const [selected, setSelected] = useState<TableInfo | null>(null)

  const loadTables = useCallback(
    async (db: string) => {
      if (!db) return
      setTables(null)
      setTablesError(null)
      setSelected(null)
      try {
        const res = await apiFetch<{ tables: TableInfo[] }>(`/api/teams/${teamSlug}/data-services/${serviceRef}/databases/${db}/tables`)
        setTables(res.tables ?? [])
      } catch (err) {
        setTablesError(errorText(err))
      }
      // eslint-disable-next-line react-hooks/exhaustive-deps
    },
    [teamSlug, serviceRef],
  )

  useEffect(() => {
    void loadTables(database)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [database])

  if (databases.length === 0) {
    return <p className="border-b border-border px-6 py-4 text-sm text-muted-foreground">{t("databases.data.noDatabases")}</p>
  }

  const filtered = (tables ?? []).filter((tb) => tb.name.toLowerCase().includes(search.trim().toLowerCase()))

  return (
    <div className="flex flex-col">
      <div className="flex flex-wrap items-center gap-3 border-b border-border px-6 py-3">
        <div className="min-w-48">
          <Select
            value={database}
            onValueChange={(v) => {
              if (v) setDatabase(v)
            }}
          >
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {databases.map((d) => (
                <SelectItem key={d.id} value={d.name}>
                  {d.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        {selected ? (
          <Button size="sm" variant="secondary" onClick={() => setSelected(null)}>
            {t("databases.data.backToTables")}
          </Button>
        ) : (
          <div className="relative min-w-56 max-w-xs flex-1">
            <SearchIcon className="pointer-events-none absolute start-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" aria-hidden />
            <Input value={search} onChange={(e) => setSearch(e.target.value)} placeholder={t("databases.data.searchTables")} dir="ltr" className="ps-8" />
          </div>
        )}
      </div>

      <ErrorBand message={tablesError} />

      {!selected ? (
        tables === null ? (
          <LoadingRows rows={3} />
        ) : filtered.length === 0 ? (
          <p className="border-b border-border px-6 py-4 text-sm text-muted-foreground">{t("databases.data.noTables")}</p>
        ) : (
          <TableBand>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t("databases.data.table")}</TableHead>
                  <TableHead>{t("databases.data.rowsEstimate")}</TableHead>
                  <TableHead>{t("databases.data.size")}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {filtered.map((tb) => (
                  <TableRow key={qualify(tb)} className="cursor-pointer" onClick={() => setSelected(tb)}>
                    <TableCell dir="ltr" className="font-mono text-xs">
                      <span className="flex items-center gap-2">
                        <TableIcon className="size-3.5 text-muted-foreground" aria-hidden />
                        {qualify(tb)}
                      </span>
                    </TableCell>
                    <TableCell dir="ltr">{tb.rows_estimate.toLocaleString()}</TableCell>
                    <TableCell dir="ltr">{humanBytes(tb.size_bytes)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </TableBand>
        )
      ) : (
        <TableView teamSlug={teamSlug} serviceRef={serviceRef} database={database} table={qualify(selected)} />
      )}

      <div className="border-t border-border">
        <SqlConsole teamSlug={teamSlug} serviceRef={serviceRef} database={database} manage={manage} />
      </div>
    </div>
  )
}

function TableView({ teamSlug, serviceRef, database, table }: { teamSlug: string; serviceRef: string; database: string; table: string }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [columns, setColumns] = useState<ColumnInfo[] | null>(null)
  const [result, setResult] = useState<QueryResult | null>(null)
  const [offset, setOffset] = useState(0)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => setOffset(0), [table])

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const encTable = encodeURIComponent(table)
      const [cols, rows] = await Promise.all([
        apiFetch<{ columns: ColumnInfo[] }>(`/api/teams/${teamSlug}/data-services/${serviceRef}/databases/${database}/tables/${encTable}/columns`),
        apiFetch<QueryResult>(
          `/api/teams/${teamSlug}/data-services/${serviceRef}/databases/${database}/tables/${encTable}/rows?limit=${ROWS_PAGE}&offset=${offset}`,
        ),
      ])
      setColumns(cols.columns ?? [])
      setResult(rows)
    } catch (err) {
      setError(errorText(err))
    } finally {
      setLoading(false)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [teamSlug, serviceRef, database, table, offset])

  useEffect(() => {
    void load()
  }, [load])

  return (
    <div>
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-border px-6 py-3">
        <span dir="ltr" className="font-mono text-sm text-foreground">
          {table}
        </span>
        {result ? (
          <span className="text-xs text-muted-foreground" dir="ltr">
            {t("databases.data.rowCount", { count: result.row_count })}
            {result.truncated ? ` — ${t("databases.data.truncated")}` : ""}
            {" · "}
            {t("databases.data.durationMs", { ms: result.duration_ms })}
          </span>
        ) : null}
      </div>
      <ErrorBand message={error} />
      {loading ? (
        <LoadingRows rows={4} />
      ) : (
        <>
          <ResultGrid columns={columns ?? []} rows={result?.rows ?? []} />
          <div className="flex items-center justify-between gap-3 border-b border-border px-6 py-3">
            <Button size="sm" variant="secondary" disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - ROWS_PAGE))}>
              {t("databases.data.prevPage")}
            </Button>
            <Button
              size="sm"
              variant="secondary"
              disabled={!result || result.rows.length < ROWS_PAGE}
              onClick={() => setOffset(offset + ROWS_PAGE)}
            >
              {t("databases.data.nextPage")}
            </Button>
          </div>
        </>
      )}
    </div>
  )
}

function ResultGrid({ columns, rows }: { columns: ColumnInfo[]; rows: (string | null)[][] }) {
  const { t } = useI18n()
  if (columns.length === 0) {
    return <p className="border-b border-border px-6 py-4 text-sm text-muted-foreground">{t("databases.data.noColumns")}</p>
  }
  return (
    <div className="max-h-[28rem] overflow-auto border-b border-border">
      <Table>
        <TableHeader className="sticky top-0 z-10 bg-background">
          <TableRow>
            {columns.map((c) => (
              <TableHead key={c.name} dir="ltr">
                <span className="flex flex-col">
                  <span>{c.name}</span>
                  {c.type ? <span className="text-[10px] font-normal text-muted-foreground">{c.type}</span> : null}
                </span>
              </TableHead>
            ))}
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((row, i) => (
            <TableRow key={i}>
              {row.map((cell, j) => (
                <TableCell key={j} dir="ltr" title={cell && cell.length > CELL_CLAMP ? cell : undefined}>
                  {cell === null ? (
                    <span className="italic text-muted-foreground">{t("databases.data.null")}</span>
                  ) : cell.length > CELL_CLAMP ? (
                    `${cell.slice(0, CELL_CLAMP)}…`
                  ) : (
                    cell
                  )}
                </TableCell>
              ))}
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}

function SqlConsole({
  teamSlug,
  serviceRef,
  database,
  manage,
}: {
  teamSlug: string
  serviceRef: string
  database: string
  manage: boolean
}) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [sql, setSql] = useState("")
  const [allowWrites, setAllowWrites] = useState(false)
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [result, setResult] = useState<QueryResult | null>(null)

  useEffect(() => setAllowWrites(false), [database])

  const execute = useCallback(
    async (write: boolean) => {
      setBusy(true)
      setError(null)
      try {
        const res = await apiFetch<QueryResult>(`/api/teams/${teamSlug}/data-services/${serviceRef}/databases/${database}/query`, {
          method: "POST",
          body: { sql, write },
        })
        setResult(res)
      } catch (err) {
        setError(errorText(err))
      } finally {
        setBusy(false)
      }
      // eslint-disable-next-line react-hooks/exhaustive-deps
    },
    [teamSlug, serviceRef, database, sql],
  )

  const attemptRun = () => {
    if (!sql.trim() || busy) return
    if (allowWrites) setConfirmOpen(true)
    else void execute(false)
  }

  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
      e.preventDefault()
      attemptRun()
    }
  }

  return (
    <div className="px-6 py-5">
      <p className="mb-2 text-sm font-medium text-foreground">{t("databases.data.sqlConsoleTitle")}</p>
      <Textarea
        value={sql}
        onChange={(e) => setSql(e.target.value)}
        onKeyDown={onKeyDown}
        dir="ltr"
        spellCheck={false}
        placeholder={t("databases.data.sqlPlaceholder")}
        className="min-h-32 font-mono text-xs"
      />
      <div className="mt-3 flex flex-wrap items-center gap-3">
        <Button variant="primary" size="sm" onClick={attemptRun} disabled={busy || !sql.trim()}>
          <PlayIcon />
          {busy ? t("common.working") : t("databases.data.runQuery")}
        </Button>
        {manage ? (
          <label className="flex items-center gap-2 text-sm">
            <Checkbox checked={allowWrites} onCheckedChange={(v) => setAllowWrites(Boolean(v))} />
            <span className={allowWrites ? "font-medium text-nq-danger-text" : "text-muted-foreground"}>{t("databases.data.allowWrites")}</span>
          </label>
        ) : null}
        <span className="text-xs text-muted-foreground">{t("databases.data.runHint")}</span>
      </div>
      {allowWrites ? (
        <Alert className="mt-3" variant="destructive">
          <AlertDescription>{t("databases.data.allowWritesHint")}</AlertDescription>
        </Alert>
      ) : null}
      <ConfirmButton
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        label={t("databases.data.runQuery")}
        title={t("databases.data.confirmWriteTitle")}
        description={t("databases.data.confirmWriteHint")}
        confirmLabel={t("databases.data.runQuery")}
        onConfirm={() => execute(true)}
      />
      <FormError message={error} />
      {result ? (
        <div className="-mx-6 mt-4 border-t border-border">
          <div className="flex items-center justify-between gap-3 px-6 py-2 text-xs text-muted-foreground" dir="ltr">
            <span>
              {t("databases.data.rowCount", { count: result.row_count })}
              {result.truncated ? ` — ${t("databases.data.truncated")}` : ""}
            </span>
            <span>{t("databases.data.durationMs", { ms: result.duration_ms })}</span>
          </div>
          <ResultGrid columns={result.columns} rows={result.rows} />
        </div>
      ) : null}
    </div>
  )
}
