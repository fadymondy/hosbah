"use client"

import { useCallback, useEffect, useState } from "react"
import { AlertCircleIcon, CheckCircle2Icon, CircleSlashIcon, PlayIcon, RadarIcon, RefreshCwIcon } from "lucide-react"
import { toast } from "sonner"

import { Facts } from "@/components/admin/admin-list"
import { useErrorText } from "@/components/form-error"
import { ErrorBand, LoadingRows, SectionTitle, TableBand } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { apiFetch } from "@/lib/api"
import { useFormatDate, useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

/*
Import (MH-574 A4): what the last sync of a server's discovered workloads found, and per item what
became of it — imported as an app or a database, marked a shared service, updated, skipped,
ignored, or errored — with a sticky per-item decision. See internal/api/import.go and
internal/importer/sync.go for the server side.
*/

type ImportRun = {
  status: "ok" | "error"
  error: string | null
  discovered: number
  created: number
  updated: number
  missing: number
  removed: number
  ignored: number
  started_at: string
  finished_at: string | null
}

type Decision = "app" | "database" | "shared_service" | "ignore"

type ImportItem = {
  origin_kind: string
  origin_key: string
  name: string
  detected_kind: string | null
  target: "app" | "data_service"
  outcome:
    | "imported_app"
    | "imported_data_service"
    | "imported_shared_service"
    | "linked_guest"
    | "updated"
    | "missing"
    | "removed"
    | "skipped"
    | "ignored"
    | "error"
  reason: string | null
  error: string | null
  record_id: string | null
  decision: Decision | null
}

const DECISIONS: Decision[] = ["app", "database", "shared_service", "ignore"]

const OUTCOME_TONE: Record<ImportItem["outcome"], "default" | "secondary" | "outline" | "destructive"> = {
  imported_app: "default",
  imported_data_service: "default",
  imported_shared_service: "default",
  linked_guest: "default",
  updated: "secondary",
  missing: "outline",
  removed: "outline",
  skipped: "outline",
  ignored: "outline",
  error: "destructive",
}

const OUTCOME_ICON: Record<ImportItem["outcome"], typeof CheckCircle2Icon> = {
  imported_app: CheckCircle2Icon,
  imported_data_service: CheckCircle2Icon,
  imported_shared_service: CheckCircle2Icon,
  linked_guest: CheckCircle2Icon,
  updated: CheckCircle2Icon,
  missing: AlertCircleIcon,
  removed: CircleSlashIcon,
  skipped: CircleSlashIcon,
  ignored: CircleSlashIcon,
  error: AlertCircleIcon,
}

/**
 * The Import tab: the last sync's outcome (when it ran, its counts), and every discovered
 * workload with what became of it, a status chip, and a per-row "Import as" choice — sticky, the
 * next sync always honours it.
 */
export function Import({ serverId, manage }: { serverId: string; manage: boolean }) {
  const { t } = useI18n()
  const { team } = useTeam()
  const fmt = useFormatDate()
  const errorText = useErrorText()
  const base = `/api/teams/${team.slug}/servers/${serverId}/import`
  const [run, setRun] = useState<ImportRun | null | undefined>(undefined)
  const [items, setItems] = useState<ImportItem[]>([])
  const [error, setError] = useState<string | null>(null)
  const [running, setRunning] = useState(false)
  const [saving, setSaving] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      const r = await apiFetch<{ run: ImportRun | null; items: ImportItem[] }>(base)
      setRun(r.run ?? null)
      setItems(r.items ?? [])
    } catch (err) {
      setError(errorText(err))
      setRun(null)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [base])

  useEffect(() => {
    void load()
  }, [load])

  async function runNow() {
    setRunning(true)
    setError(null)
    try {
      await apiFetch(`${base}/run`, { method: "POST" })
      toast.success(t("servers.import.queued"))
    } catch (err) {
      setError(errorText(err))
    } finally {
      setRunning(false)
    }
  }

  async function decide(item: ImportItem, decision: Decision) {
    setSaving(item.origin_key)
    setError(null)
    try {
      await apiFetch(`${base}/items/${encodeURIComponent(item.origin_key)}`, { method: "PUT", body: { decision } })
      setItems((cur) => cur.map((x) => (x.origin_key === item.origin_key ? { ...x, decision } : x)))
      toast.success(t("servers.import.decided", { name: item.name, decision: t(`servers.import.decisions.${decision}`) }))
    } catch (err) {
      setError(errorText(err))
    } finally {
      setSaving(null)
    }
  }

  return (
    <section aria-labelledby="import-h" data-testid="import">
      <SectionTitle
        id="import-h"
        title={t("servers.import.title")}
        hint={t("servers.import.hint")}
        action={
          manage ? (
            <Button size="sm" onClick={() => void runNow()} disabled={running}>
              {running ? <RefreshCwIcon className="animate-spin" /> : <PlayIcon />}
              {t("servers.import.runNow")}
            </Button>
          ) : undefined
        }
      />
      <ErrorBand message={error} />

      {run === undefined ? (
        <LoadingRows />
      ) : run === null ? (
        <p className="flex items-center gap-2 border-b border-line px-6 py-4 text-sm text-grid-muted">
          <RadarIcon className="size-4" />
          {t("servers.import.never")}
        </p>
      ) : (
        <Facts
          items={[
            [t("servers.import.lastRun"), <span key="s" dir="ltr">{fmt(run.started_at)}</span>],
            [t("servers.import.created"), <span key="c" dir="ltr" className="font-mono">{run.created}</span>],
            [t("servers.import.updated"), <span key="u" dir="ltr" className="font-mono">{run.updated}</span>],
            [
              t("servers.import.status"),
              run.status === "ok" ? (
                <Badge key="ok" variant="secondary">
                  {t("servers.import.ok")}
                </Badge>
              ) : (
                <span key="err" className="text-grid-danger">
                  {run.error ?? t("servers.import.errored")}
                </span>
              ),
            ],
          ]}
        />
      )}

      {run !== undefined && items.length === 0 ? (
        <Empty className="border-b">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <RadarIcon />
            </EmptyMedia>
            <EmptyTitle>{t("servers.import.emptyTitle")}</EmptyTitle>
            <EmptyDescription>{t("servers.import.emptyHint")}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : items.length ? (
        <TableBand>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("servers.import.columns.name")}</TableHead>
                <TableHead>{t("servers.import.columns.outcome")}</TableHead>
                <TableHead>{t("servers.import.columns.detail")}</TableHead>
                {manage ? <TableHead>{t("servers.import.columns.decision")}</TableHead> : null}
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((it) => {
                const Icon = OUTCOME_ICON[it.outcome]
                return (
                  <TableRow key={it.origin_key}>
                    <TableCell>
                      <span dir="ltr" className="block truncate font-medium text-grid-fg" title={it.name}>
                        {it.name}
                      </span>
                      <span className="text-xs text-grid-muted">
                        {t(`servers.discovered.kinds.${it.origin_kind}`, undefined, it.origin_kind)}
                        {it.detected_kind ? ` · ${it.detected_kind}` : ""}
                      </span>
                    </TableCell>
                    <TableCell>
                      <Badge variant={OUTCOME_TONE[it.outcome]} className="gap-1">
                        <Icon className="size-3" />
                        {t(`servers.import.outcomes.${it.outcome}`)}
                      </Badge>
                    </TableCell>
                    <TableCell className="max-w-xs">
                      <span className="block truncate text-xs text-grid-muted" title={it.error ?? it.reason ?? ""}>
                        {it.error ?? it.reason ?? "—"}
                      </span>
                    </TableCell>
                    {manage ? (
                      <TableCell>
                        <Select
                          value={it.decision ?? ""}
                          onValueChange={(v) => v && void decide(it, v as Decision)}
                          disabled={saving === it.origin_key}
                        >
                          <SelectTrigger className="w-40" aria-label={t("servers.import.columns.decision")}>
                            <SelectValue placeholder={t("servers.import.decisions.auto")}>
                              {(v: string) => (v ? t(`servers.import.decisions.${v}`) : t("servers.import.decisions.auto"))}
                            </SelectValue>
                          </SelectTrigger>
                          <SelectContent>
                            {DECISIONS.map((d) => (
                              <SelectItem key={d} value={d}>
                                {t(`servers.import.decisions.${d}`)}
                              </SelectItem>
                            ))}
                          </SelectContent>
                        </Select>
                      </TableCell>
                    ) : null}
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        </TableBand>
      ) : null}
    </section>
  )
}
