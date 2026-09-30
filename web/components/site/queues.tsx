"use client"

import { Badge, Button, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useState } from "react"
import { AlertTriangleIcon, ListOrderedIcon, RefreshCwIcon, RotateCwIcon, Trash2Icon } from "lucide-react"
import { toast } from "sonner"

import { ConfirmButton } from "@/components/confirm-button"
import { useErrorText } from "@/components/form-error"
import { ListSearch, matches } from "@/components/server-tabs/list-tools"
import { useLoadedSite } from "@/components/site/context"
import { ErrorBand, LoadingRows, SectionTitle, TableBand } from "@/components/states"



import { apiFetch, ApiError } from "@/lib/api"
import { useI18n } from "@/lib/i18n"
import { canOperate } from "@/lib/servers"
import { useTeam } from "@/lib/team"

type Overview = {
  driver: "laravel" | "celery" | "bullmq" | "asynq" | "none"
  confidence: "detected" | "likely" | "unknown"
  evidence: string[]
  readable: boolean
  reason: string | null
  horizon: { installed: boolean; status: string | null } | null
  queues: { name: string; pending: number | null; running: number | null; failed: number | null }[]
  failed_total: number | null
}
type Job = { id: string; queue: string; connection: string; name: string; failed_at: string; exception: string | null }

const n = (v: number | null) => (v == null ? "—" : String(v))

/**
 * Queues: read live from the site's release with its framework's own commands. What cannot be
 * read is said to be unreadable rather than guessed. Failed jobs can be retried or forgotten;
 * their payloads never leave the server.
 */
export function SiteQueues() {
  const { t } = useI18n()
  const { team, manage } = useTeam()
  const operate = canOperate(team.role)
  const errorText = useErrorText()
  const { path } = useLoadedSite()
  const [ov, setOv] = useState<Overview | null>(null)
  const [jobs, setJobs] = useState<Job[] | null>(null)
  const [truncated, setTruncated] = useState(false)
  const [note, setNote] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const [busy, setBusy] = useState(false)
  const [q, setQ] = useState("")

  const load = useCallback(async () => {
    if (!operate) return
    setLoading(true)
    setError(null)
    setNote(null)
    try {
      const o = await apiFetch<Overview>(`${path}/queues`)
      setOv(o)
      if (o.readable && o.driver === "laravel") {
        const f = await apiFetch<{ jobs: Job[]; truncated: boolean }>(`${path}/queues/failed?limit=100`)
        setJobs(f.jobs ?? [])
        setTruncated(!!f.truncated)
      } else setJobs([])
    } catch (err) {
      // Nothing live, or the server is away: a state of the site, not a page error.
      if (err instanceof ApiError && (err.status === 409 || err.status === 504)) setNote(errorText(err))
      else setError(errorText(err))
      setJobs((x) => x ?? [])
    } finally {
      setLoading(false)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path, operate])

  useEffect(() => {
    void load()
  }, [load])

  async function act(suffix: string, method: "POST" | "DELETE", done: string) {
    setBusy(true)
    setError(null)
    try {
      await apiFetch(`${path}/queues/failed${suffix}`, { method })
      toast.success(done)
      await load()
    } catch (err) {
      setError(errorText(err))
      throw new Error(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  if (!operate)
    return (
      <section aria-labelledby="qu-h">
        <SectionTitle id="qu-h" title={t("site.nav.queues")} hint={t("site.planned.queues.hint")} />
        <p className="border-b border-border px-6 py-6 text-sm text-muted-foreground">{t("site.queues.needsDeployer")}</p>
      </section>
    )

  const shown = (jobs ?? []).filter((j) => matches(q, j.id, j.name, j.queue, j.connection, j.exception))

  return (
    <>
      <section aria-labelledby="qu-h">
        <SectionTitle
          id="qu-h"
          title={t("site.nav.queues")}
          hint={t("site.planned.queues.hint")}
          action={
            <Button variant="ghost" size="icon-sm" aria-label={t("github.refresh")} disabled={loading} onClick={() => void load()}>
              <RefreshCwIcon className={loading ? "animate-spin" : ""} />
            </Button>
          }
        />
        <ErrorBand message={error} />
        {note ? <p className="border-b border-border px-6 py-4 text-sm text-muted-foreground">{note}</p> : null}
        {ov === null ? (
          note || error ? null : <LoadingRows rows={3} />
        ) : (
          <>
            <div className="flex flex-wrap items-center gap-x-3 gap-y-2 border-b border-border px-6 py-4 text-sm">
              <ListOrderedIcon className="size-4 text-nq-brand" />
              <span className="font-medium">{t(`site.queues.drivers.${ov.driver}`)}</span>
              <Badge variant={ov.confidence === "detected" ? "brand" : "outline"}>{t(`site.queues.confidence.${ov.confidence}`)}</Badge>
              {ov.horizon?.installed ? <Badge variant="neutral">Horizon{ov.horizon.status ? ` · ${ov.horizon.status}` : ""}</Badge> : null}
              {ov.evidence.length ? (
                <span dir="ltr" className="font-mono text-xs text-muted-foreground">
                  {ov.evidence.join(" · ")}
                </span>
              ) : null}
            </div>
            {!ov.readable ? (
              <p className="flex items-start gap-2 border-b border-border px-6 py-6 text-sm text-muted-foreground">
                <AlertTriangleIcon className="mt-0.5 size-4 shrink-0" />
                <span>
                  {ov.driver === "none" ? t("site.queues.none") : t("site.queues.unreadable")}
                  {ov.reason ? <span dir="auto"> {ov.reason}</span> : null}
                </span>
              </p>
            ) : ov.queues.length ? (
              <TableBand>
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>{t("site.queues.queue")}</TableHead>
                      <TableHead>{t("site.queues.pending")}</TableHead>
                      <TableHead>{t("site.queues.running")}</TableHead>
                      <TableHead>{t("site.queues.failed")}</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {ov.queues.map((x) => (
                      <TableRow key={x.name}>
                        <TableCell dir="ltr" className="text-start font-medium">
                          {x.name}
                        </TableCell>
                        <TableCell className="tabular-nums">{n(x.pending)}</TableCell>
                        <TableCell className="tabular-nums">{n(x.running)}</TableCell>
                        <TableCell className={x.failed ? "text-nq-danger-text tabular-nums" : "tabular-nums"}>{n(x.failed)}</TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </TableBand>
            ) : (
              <p className="border-b border-border px-6 py-6 text-sm text-muted-foreground">{t("site.queues.noQueues")}</p>
            )}
            {ov.readable ? <p className="border-b border-border px-6 py-2 text-xs text-muted-foreground">{t("site.queues.dash")}</p> : null}
          </>
        )}
      </section>

      {ov?.readable && ov.driver === "laravel" ? (
        <section aria-labelledby="fj-h">
          <SectionTitle
            id="fj-h"
            title={t("site.queues.failedJobs")}
            hint={t("site.queues.failedHint")}
            action={
              manage && jobs?.length ? (
                <span className="flex flex-wrap gap-2">
                  <ConfirmButton
                    icon={<RotateCwIcon />}
                    label={t("site.queues.retryAll")}
                    title={t("site.queues.retryAll")}
                    description={t("site.queues.retryAllBody")}
                    confirmLabel={t("site.queues.retryAll")}
                    onConfirm={() => act("/retry-all", "POST", t("site.queues.done"))}
                  />
                  <ConfirmButton
                    icon={<Trash2Icon />}
                    label={t("site.queues.flush")}
                    title={t("site.queues.flush")}
                    description={t("site.queues.flushBody")}
                    confirmLabel={t("site.queues.flush")}
                    onConfirm={() => act("", "DELETE", t("site.queues.done"))}
                  />
                </span>
              ) : null
            }
          />
          {jobs === null ? (
            <LoadingRows rows={2} />
          ) : jobs.length === 0 ? (
            <p className="border-b border-border px-6 py-6 text-sm text-muted-foreground">{t("site.queues.noFailed")}</p>
          ) : (
            <>
              <ListSearch value={q} onChange={setQ} placeholder={t("site.queues.search")} shown={shown.length} total={jobs.length} />
              <ol className="flex flex-col divide-y divide-border border-b border-border">
                {shown.map((j) => (
                  <li key={j.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-6 py-3">
                    <span className="flex min-w-0 flex-1 flex-col">
                      <span dir="ltr" className="truncate text-start text-sm font-medium text-foreground" title={j.name}>
                        {j.name || j.id}
                      </span>
                      <span dir="ltr" className="truncate text-start font-mono text-xs text-muted-foreground">
                        {j.queue} · {j.connection} · {j.failed_at} · {j.id}
                      </span>
                      {j.exception ? (
                        <span dir="ltr" className="truncate text-start text-xs text-nq-danger-text" title={j.exception}>
                          {j.exception}
                        </span>
                      ) : null}
                    </span>
                    {manage ? (
                      <>
                        <Button size="sm" variant="secondary" disabled={busy} onClick={() => void act(`/${encodeURIComponent(j.id)}/retry`, "POST", t("site.queues.done")).catch(() => {})}>
                          <RotateCwIcon />
                          {t("site.queues.retry")}
                        </Button>
                        <ConfirmButton
                          icon={<Trash2Icon />}
                          label={t("site.queues.forget")}
                          title={t("site.queues.forgetTitle")}
                          description={j.name || j.id}
                          confirmLabel={t("site.queues.forget")}
                          onConfirm={() => act(`/${encodeURIComponent(j.id)}`, "DELETE", t("site.queues.done"))}
                        />
                      </>
                    ) : null}
                  </li>
                ))}
              </ol>
              {truncated ? <p className="border-b border-border px-6 py-2 text-xs text-muted-foreground">{t("site.queues.truncated")}</p> : null}
            </>
          )}
        </section>
      ) : null}
    </>
  )
}
