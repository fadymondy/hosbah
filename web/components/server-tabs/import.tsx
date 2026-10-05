"use client"

import { Badge, Button, EmptyState, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, SettingsSection, StatCard, StatGrid, toast, type DataTableColumn } from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useState } from "react"
import { AlertCircleIcon, CheckCircle2Icon, CircleSlashIcon, PlayIcon, RadarIcon, RefreshCwIcon } from "lucide-react"

import { NasaqTable } from "@/components/lists/nasaq-table"
import { useErrorText } from "@/components/form-error"
import { ErrorBand, LoadingRows } from "@/components/states"





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

const OUTCOME_TONE: Record<ImportItem["outcome"], "success" | "neutral" | "outline" | "danger"> = {
  imported_app: "success",
  imported_data_service: "success",
  imported_shared_service: "success",
  linked_guest: "success",
  updated: "neutral",
  missing: "outline",
  removed: "outline",
  skipped: "outline",
  ignored: "outline",
  error: "danger",
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
    <SettingsSection
      id="import"
      className="scroll-mt-4"
      data-testid="import"
      title={t("servers.import.title")}
      description={t("servers.import.hint")}
      action={
          manage ? (
            <Button variant="primary" size="sm" onClick={() => void runNow()} disabled={running}>
              {running ? <RefreshCwIcon className="animate-spin" /> : <PlayIcon />}
              {t("servers.import.runNow")}
            </Button>
          ) : undefined
      }
    >
      <ErrorBand message={error} />

      {run === undefined ? (
        <LoadingRows />
      ) : run === null ? (
        <p className="flex items-center gap-2 rounded-lg border border-border bg-card px-4 py-3 text-sm text-muted-foreground">
          <RadarIcon className="size-4" />
          {t("servers.import.never")}
        </p>
      ) : (
        <StatGrid>
          <StatCard icon={<RadarIcon />} label={t("servers.import.lastRun")} value={<span dir="ltr" className="text-base">{fmt(run.started_at)}</span>} />
          <StatCard icon={<CheckCircle2Icon />} label={t("servers.import.created")} value={run.created} />
          <StatCard icon={<RefreshCwIcon />} label={t("servers.import.updated")} value={run.updated} />
          <StatCard
            icon={run.status === "ok" ? <CheckCircle2Icon /> : <AlertCircleIcon />}
            label={t("servers.import.status")}
            value={
              run.status === "ok" ? (
                <Badge variant="success">{t("servers.import.ok")}</Badge>
              ) : (
                <span className="text-base text-nq-danger-text">{run.error ?? t("servers.import.errored")}</span>
              )
            }
          />
        </StatGrid>
      )}

      {run !== undefined && items.length === 0 ? (
        <EmptyState className="mt-3 rounded-lg border border-dashed border-border" icon={RadarIcon} title={t("servers.import.emptyTitle")} description={t("servers.import.emptyHint")} />
      ) : items.length ? (
        <NasaqTable
          data={items}
          rowId={(it) => it.origin_key}
          label={t("servers.import.title")}
          rowLabel={(it) => it.name}
          search={t("servers.import.columns.name")}
          empty={t("home.noMatch")}
          facets={[
            {
              column: "outcome",
              title: t("servers.import.columns.outcome"),
              options: [...new Set(items.map((it) => it.outcome))].map((v) => ({ value: v, label: t(`servers.import.outcomes.${v}`) })),
            },
          ]}
          columns={[
            {
              id: "name",
              header: t("servers.import.columns.name"),
              label: t("servers.import.columns.name"),
              sortValue: (it) => it.name,
              searchValue: (it) => `${it.name} ${it.origin_kind} ${it.detected_kind ?? ""}`,
              cell: (it) => (
                <span className="block min-w-0">
                  <span dir="ltr" className="block truncate font-medium text-foreground" title={it.name}>
                    {it.name}
                  </span>
                  <span className="text-caption text-muted-foreground">
                    {t(`servers.discovered.kinds.${it.origin_kind}`, undefined, it.origin_kind)}
                    {it.detected_kind ? ` · ${it.detected_kind}` : ""}
                  </span>
                </span>
              ),
            },
            {
              id: "outcome",
              header: t("servers.import.columns.outcome"),
              label: t("servers.import.columns.outcome"),
              sortValue: (it) => it.outcome,
              filterValue: (it) => it.outcome,
              cell: (it) => {
                const Icon = OUTCOME_ICON[it.outcome]
                return (
                  <Badge variant={OUTCOME_TONE[it.outcome]} className="gap-1">
                    <Icon className="size-3" />
                    {t(`servers.import.outcomes.${it.outcome}`)}
                  </Badge>
                )
              },
            },
            {
              id: "detail",
              header: t("servers.import.columns.detail"),
              label: t("servers.import.columns.detail"),
              className: "max-w-xs max-md:hidden",
              headerClassName: "max-md:hidden",
              cell: (it) => (
                <span className="block truncate text-caption text-muted-foreground" title={it.error ?? it.reason ?? ""}>
                  {it.error ?? it.reason ?? "—"}
                </span>
              ),
            },
            {
              id: "decision",
              header: t("servers.import.columns.decision"),
              label: t("servers.import.columns.decision"),
              cell: (it) =>
                !manage ? (
                  <span className="text-caption text-muted-foreground">{t(`servers.import.decisions.${it.decision ?? "auto"}`)}</span>
                ) : (
                  <span onClick={(e) => e.stopPropagation()}>
                    <Select value={it.decision ?? ""} onValueChange={(v) => v && void decide(it, v as Decision)} disabled={saving === it.origin_key}>
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
                  </span>
                ),
            },
          ] satisfies DataTableColumn<(typeof items)[number]>[]}
        />
      ) : null}
    </SettingsSection>
  )
}
