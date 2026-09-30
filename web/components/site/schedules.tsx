"use client"

import { Badge, Button, Checkbox, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger, EmptyState, Field, FieldDescription, FieldLabel, Input, Table, TableBody, TableCell, TableHead, TableHeader, TableRow, toast } from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useState, type FormEvent } from "react"
import { CalendarClockIcon, HistoryIcon, PauseIcon, PencilIcon, PlayIcon, PlusIcon, Trash2Icon } from "lucide-react"

import { ConfirmButton } from "@/components/confirm-button"
import { useDialogOpen, type Controlled } from "@/components/dialog-state"
import { FormError, useErrorText } from "@/components/form-error"
import { useTimeAgo } from "@/components/home/sparkline"
import { ListSearch, matches } from "@/components/server-tabs/list-tools"
import { useLoadedSite } from "@/components/site/context"
import { ErrorBand, LoadingRows, SectionTitle, TableBand } from "@/components/states"








import { apiFetch } from "@/lib/api"
import { useFormatDate, useI18n } from "@/lib/i18n"
import { canOperate } from "@/lib/servers"
import { useTeam } from "@/lib/team"
import { cn } from "@/lib/utils"

export type RunStatus = "queued" | "running" | "succeeded" | "failed" | "timeout" | "skipped"
export type Schedule = {
  id: string
  name: string
  cron: string
  timezone: string
  command: string
  timeout_seconds: number
  enabled: boolean
  next_run_at: string | null
  last_status: RunStatus | null
  last_run: { id: string; status: RunStatus; started_at: string | null; duration_ms: number | null } | null
  next_runs: string[]
}
type Run = {
  id: string
  trigger: "schedule" | "manual"
  status: RunStatus
  exit_code: number | null
  started_at: string | null
  finished_at: string | null
  duration_ms: number | null
  error: string | null
  output?: string
}

const TONE: Record<RunStatus, "success" | "neutral" | "danger" | "outline"> = {
  queued: "neutral",
  running: "neutral",
  succeeded: "success",
  failed: "danger",
  timeout: "danger",
  skipped: "outline",
}
const PRESETS = ["* * * * *", "*/5 * * * *", "0 * * * *", "0 3 * * *", "0 9 * * 1-5", "0 0 1 * *"]

/**
 * Schedules: cron jobs run inside the site's live release, with its environment. Each firing is
 * a run with its exit code and output; a job still running when its next time comes is skipped.
 */
export function SiteSchedules() {
  const { t, locale } = useI18n()
  const { team, manage } = useTeam()
  const operate = canOperate(team.role)
  const ago = useTimeAgo(locale)
  const fmt = useFormatDate()
  const errorText = useErrorText()
  const { path } = useLoadedSite()
  const [items, setItems] = useState<Schedule[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState<string | null>(null)
  const [history, setHistory] = useState<Schedule | null>(null)
  const [q, setQ] = useState("")

  const load = useCallback(async () => {
    try {
      const r = await apiFetch<{ schedules: Schedule[] }>(`${path}/schedules`)
      setItems(r.schedules ?? [])
      setError(null)
    } catch (err) {
      setError(errorText(err))
      setItems((x) => x ?? [])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path])

  useEffect(() => {
    void load()
    const id = setInterval(load, 15_000)
    return () => clearInterval(id)
  }, [load])

  async function act(s: Schedule, fn: () => Promise<unknown>, done: string) {
    setBusy(s.id)
    setError(null)
    try {
      await fn()
      toast.success(done)
      await load()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(null)
    }
  }

  const shown = (items ?? []).filter((s) => matches(q, s.name, s.cron, s.command, s.last_status))
  const add = manage ? <ScheduleDialog path={path} onSaved={load} /> : null

  return (
    <>
      <section aria-labelledby="sch-h">
        <SectionTitle id="sch-h" title={t("site.nav.schedules")} hint={t("site.planned.schedules.hint")} action={add} />
        <ErrorBand message={error} />
        {items === null ? (
          <LoadingRows rows={3} />
        ) : items.length === 0 ? (
          <EmptyState className="border-b" icon={CalendarClockIcon} title={t("site.schedules.emptyTitle")} description={t("site.schedules.emptyHint")} actions={<>{add}</>} />
        ) : (
          <>
            <ListSearch value={q} onChange={setQ} placeholder={t("site.schedules.search")} shown={shown.length} total={items.length} />
            <TableBand>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t("site.monitors.name")}</TableHead>
                    <TableHead>{t("site.schedules.when")}</TableHead>
                    <TableHead>{t("site.schedules.last")}</TableHead>
                    <TableHead>{t("site.schedules.next")}</TableHead>
                    <TableHead className="text-end">{t("common.actions")}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {shown.map((s) => (
                    <TableRow key={s.id}>
                      <TableCell>
                        <span dir="auto" className="font-medium">
                          {s.name}
                        </span>
                        <div dir="ltr" className="max-w-72 truncate text-start font-mono text-xs text-muted-foreground" title={s.command}>
                          {s.command}
                        </div>
                      </TableCell>
                      <TableCell>
                        <code dir="ltr" className="font-mono text-xs">
                          {s.cron}
                        </code>
                        <div dir="ltr" className="text-start text-xs text-muted-foreground">
                          {s.timezone}
                        </div>
                      </TableCell>
                      <TableCell>
                        {s.last_run ? (
                          <span className="flex flex-col items-start gap-1">
                            <Badge variant={TONE[s.last_run.status]}>{t(`site.schedules.status.${s.last_run.status}`)}</Badge>
                            <span className="text-xs text-muted-foreground">{s.last_run.started_at ? ago(s.last_run.started_at) : ""}</span>
                          </span>
                        ) : (
                          <span className="text-xs text-muted-foreground">{t("site.schedules.never")}</span>
                        )}
                      </TableCell>
                      <TableCell className="text-xs">
                        {!s.enabled ? <Badge variant="outline">{t("site.monitors.paused")}</Badge> : s.next_run_at ? <span dir="ltr">{fmt(s.next_run_at)}</span> : "—"}
                      </TableCell>
                      <TableCell>
                        <div className="flex flex-wrap justify-end gap-2">
                          <Button size="sm" variant="secondary" onClick={() => setHistory(s)}>
                            <HistoryIcon />
                            {t("site.schedules.runs")}
                          </Button>
                          {operate ? (
                            <Button
                              size="sm"
                              variant="secondary"
                              disabled={busy === s.id}
                              onClick={() => void act(s, () => apiFetch(`${path}/schedules/${s.id}/run`, { method: "POST" }), t("site.schedules.started"))}
                            >
                              <PlayIcon />
                              {t("site.schedules.runNow")}
                            </Button>
                          ) : null}
                          {manage ? (
                            <>
                              <Button
                                size="sm"
                                variant="secondary"
                                disabled={busy === s.id}
                                onClick={() =>
                                  void act(s, () => apiFetch(`${path}/schedules/${s.id}`, { method: "PATCH", body: { enabled: !s.enabled } }), t("site.saved"))
                                }
                              >
                                {s.enabled ? <PauseIcon /> : <PlayIcon />}
                                {s.enabled ? t("site.schedules.pause") : t("site.schedules.resume")}
                              </Button>
                              <ScheduleDialog path={path} schedule={s} onSaved={load} />
                              <DeleteSchedule path={path} schedule={s} onDeleted={load} />
                            </>
                          ) : null}
                        </div>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </TableBand>
          </>
        )}
      </section>
      {history ? <Runs path={path} schedule={history} canRead={operate} onClose={() => setHistory(null)} /> : null}
    </>
  )
}

/** A schedule's run history; picking a run shows its output (for people who may read app logs). */
function Runs({ path, schedule, canRead, onClose }: { path: string; schedule: Schedule; canRead: boolean; onClose: () => void }) {
  const { t } = useI18n()
  const fmt = useFormatDate()
  const errorText = useErrorText()
  const [runs, setRuns] = useState<Run[] | null>(null)
  const [cursor, setCursor] = useState<string | null>(null)
  const [picked, setPicked] = useState<Run | null>(null)
  const [error, setError] = useState<string | null>(null)
  const base = `${path}/schedules/${schedule.id}/runs`

  const load = useCallback(
    async (before?: string) => {
      try {
        const r = await apiFetch<{ runs: Run[]; next_cursor?: string | null }>(`${base}?limit=30${before ? `&before=${encodeURIComponent(before)}` : ""}`)
        setRuns((cur) => (before ? [...(cur ?? []), ...(r.runs ?? [])] : (r.runs ?? [])))
        setCursor(r.next_cursor ?? null)
      } catch (err) {
        setError(errorText(err))
        setRuns((x) => x ?? [])
      }
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [base],
  )
  useEffect(() => {
    setPicked(null)
    void load()
  }, [load])

  async function open(r: Run) {
    setPicked(r)
    if (!canRead) return
    try {
      setPicked(await apiFetch<Run>(`${base}/${r.id}`))
    } catch (err) {
      setError(errorText(err))
    }
  }

  return (
    <section aria-labelledby="runs-h">
      <SectionTitle
        id="runs-h"
        title={t("site.schedules.runsOf", { name: schedule.name })}
        action={
          <Button variant="ghost" size="sm" onClick={onClose}>
            {t("site.schedules.close")}
          </Button>
        }
      />
      <ErrorBand message={error} />
      {runs === null ? (
        <LoadingRows rows={3} />
      ) : runs.length === 0 ? (
        <p className="border-b border-border px-6 py-6 text-sm text-muted-foreground">{t("site.schedules.never")}</p>
      ) : (
        <div className="grid border-b border-border xl:grid-cols-[minmax(0,2fr)_minmax(0,3fr)]">
          <ol className="flex max-h-[28rem] min-w-0 flex-col divide-y divide-border overflow-y-auto border-b border-border xl:border-e xl:border-b-0">
            {runs.map((r) => (
              <li key={r.id}>
                <button
                  type="button"
                  aria-current={picked?.id === r.id}
                  onClick={() => void open(r)}
                  className={cn("flex w-full flex-wrap items-center gap-2 px-6 py-3 text-start text-sm hover:bg-secondary", picked?.id === r.id && "bg-secondary")}
                >
                  <Badge variant={TONE[r.status]}>{t(`site.schedules.status.${r.status}`)}</Badge>
                  <span className="text-xs text-muted-foreground">{t(`site.schedules.trigger.${r.trigger}`)}</span>
                  {r.exit_code != null ? (
                    <span dir="ltr" className="font-mono text-xs text-muted-foreground">
                      exit {r.exit_code}
                    </span>
                  ) : null}
                  <span dir="ltr" className="ms-auto text-xs text-muted-foreground">
                    {r.started_at ? fmt(r.started_at) : "—"}
                    {r.duration_ms != null ? ` · ${(r.duration_ms / 1000).toFixed(1)}s` : ""}
                  </span>
                </button>
              </li>
            ))}
            {cursor ? (
              <li className="flex justify-center px-6 py-3">
                <Button variant="ghost" size="sm" onClick={() => void load(cursor)}>
                  {t("site.monitors.more")}
                </Button>
              </li>
            ) : null}
          </ol>
          <div className="min-w-0">
            {!picked ? (
              <p className="px-6 py-4 text-sm text-muted-foreground">{t("site.schedules.pickRun")}</p>
            ) : !canRead ? (
              <p className="px-6 py-4 text-sm text-muted-foreground">{t("site.schedules.outputHidden")}</p>
            ) : (
              <>
                {picked.error ? (
                  <p dir="auto" className="border-b border-border px-6 py-3 text-sm text-nq-danger-text">
                    {picked.error}
                  </p>
                ) : null}
                <pre dir="ltr" className="max-h-[28rem] overflow-auto px-6 py-4 text-start font-mono text-xs leading-5 whitespace-pre-wrap text-foreground">
                  {picked.output === undefined ? "…" : picked.output || t("site.schedules.noOutput")}
                </pre>
              </>
            )}
          </div>
        </div>
      )}
    </section>
  )
}

/** Delete a schedule (and its runs). Also opened from the overview canvas. */
export function DeleteSchedule({ path, schedule: s, onDeleted, ...controlled }: { path: string; schedule: Schedule; onDeleted: () => Promise<void> | void } & Controlled) {
  const { t } = useI18n()
  const errorText = useErrorText()
  return (
    <ConfirmButton
      {...controlled}
      icon={<Trash2Icon />}
      label={t("common.delete")}
      title={t("site.monitors.deleteTitle", { name: s.name })}
      description={t("site.schedules.deleteBody")}
      confirmLabel={t("common.delete")}
      onConfirm={async () => {
        try {
          await apiFetch(`${path}/schedules/${s.id}`, { method: "DELETE" })
        } catch (err) {
          throw new Error(errorText(err))
        }
        await onDeleted()
      }}
    />
  )
}

/**
 * Create or edit a schedule; the cron expression is checked as you type, with its next fire times.
 * Also opened from the overview canvas.
 */
export function ScheduleDialog({ path, schedule, onSaved, ...controlled }: { path: string; schedule?: Schedule; onSaved: () => Promise<void> | void } & Controlled) {
  const { t } = useI18n()
  const fmt = useFormatDate()
  const errorText = useErrorText()
  const [name, setName] = useState("")
  const [cron, setCron] = useState("")
  const [timezone, setTimezone] = useState("UTC")
  const [command, setCommand] = useState("")
  const [timeout, setTimeoutS] = useState("300")
  const [enabled, setEnabled] = useState(true)
  const [preview, setPreview] = useState<string[] | null>(null)
  const [previewError, setPreviewError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const { open, setOpen, controlled: fromCaller } = useDialogOpen(controlled, reset)

  function reset() {
    setError(null)
    setName(schedule?.name ?? "")
    setCron(schedule?.cron ?? "0 3 * * *")
    setTimezone(schedule?.timezone ?? Intl.DateTimeFormat().resolvedOptions().timeZone ?? "UTC")
    setCommand(schedule?.command ?? "")
    setTimeoutS(String(schedule?.timeout_seconds ?? 300))
    setEnabled(schedule?.enabled ?? true)
  }

  // Ask the API what the expression means, a moment after typing stops.
  useEffect(() => {
    if (!open || !cron.trim()) return
    const id = setTimeout(() => {
      apiFetch<{ next_runs: string[] }>(`${path}/schedules/preview`, { method: "POST", body: { cron: cron.trim(), timezone } })
        .then((r) => {
          setPreview(r.next_runs ?? [])
          setPreviewError(null)
        })
        .catch((err) => {
          setPreview(null)
          setPreviewError(errorText(err))
        })
    }, 350)
    return () => clearTimeout(id)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, cron, timezone, path])

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    const body = { name: name.trim(), cron: cron.trim(), timezone, command: command.trim(), timeout_seconds: Number(timeout) || 300, enabled }
    try {
      await apiFetch(schedule ? `${path}/schedules/${schedule.id}` : `${path}/schedules`, { method: schedule ? "PATCH" : "POST", body })
      setOpen(false)
      await onSaved()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      {fromCaller ? null : (
        <DialogTrigger render={<Button size="sm" variant={schedule ? "secondary" : "primary"} />}>
          {schedule ? <PencilIcon /> : <PlusIcon />}
          {schedule ? t("lists.edit") : t("site.schedules.add")}
        </DialogTrigger>
      )}
      <DialogContent className="max-h-[90dvh] overflow-y-auto">
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{schedule ? schedule.name : t("site.schedules.add")}</DialogTitle>
            <DialogDescription>{t("site.schedules.dialogHint")}</DialogDescription>
          </DialogHeader>
          <div className="flex w-full flex-col gap-5">
            <Field>
              <FieldLabel htmlFor="sch-name">{t("site.monitors.name")}</FieldLabel>
              <Input id="sch-name" dir="auto" required maxLength={80} value={name} onChange={(e) => setName(e.target.value)} />
            </Field>
            <Field>
              <FieldLabel htmlFor="sch-cmd">{t("site.command")}</FieldLabel>
              <Input id="sch-cmd" dir="ltr" required maxLength={1000} placeholder="php artisan schedule:run" value={command} onChange={(e) => setCommand(e.target.value)} className="font-mono" />
              <FieldDescription>{t("site.schedules.commandHint")}</FieldDescription>
            </Field>
            <Field>
              <FieldLabel htmlFor="sch-cron">{t("site.schedules.cron")}</FieldLabel>
              <Input id="sch-cron" dir="ltr" required value={cron} onChange={(e) => setCron(e.target.value)} className="font-mono" />
              <div className="flex flex-wrap gap-1">
                {PRESETS.map((p) => (
                  <button key={p} type="button" dir="ltr" onClick={() => setCron(p)} className="border border-border px-1.5 py-0.5 font-mono text-xs text-muted-foreground hover:bg-secondary hover:text-foreground">
                    {p}
                  </button>
                ))}
              </div>
              {previewError ? (
                <p className="text-xs text-nq-danger-text">{previewError}</p>
              ) : preview?.length ? (
                <p className="text-xs text-muted-foreground">
                  {t("site.schedules.nextRuns")}{" "}
                  <span dir="ltr">{preview.slice(0, 3).map((d) => fmt(d)).join(" · ")}</span>
                </p>
              ) : null}
            </Field>
            <div className="grid gap-4 sm:grid-cols-2">
              <Field>
                <FieldLabel htmlFor="sch-tz">{t("site.schedules.timezone")}</FieldLabel>
                <Input id="sch-tz" dir="ltr" required placeholder="Africa/Cairo" value={timezone} onChange={(e) => setTimezone(e.target.value)} />
              </Field>
              <Field>
                <FieldLabel htmlFor="sch-to">{t("site.schedules.timeout")}</FieldLabel>
                <Input id="sch-to" dir="ltr" type="number" min={10} max={3600} required value={timeout} onChange={(e) => setTimeoutS(e.target.value)} />
              </Field>
            </div>
            <label className="flex items-center gap-2 text-sm">
              <Checkbox checked={enabled} onCheckedChange={(v) => setEnabled(v === true)} />
              {t("site.monitors.enabled")}
            </label>
          </div>
          <FormError message={error} />
          <DialogFooter>
            <Button type="button" variant="secondary" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button variant="primary" type="submit" disabled={busy || !name.trim() || !command.trim() || !!previewError}>
              {busy ? t("common.working") : t("common.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
