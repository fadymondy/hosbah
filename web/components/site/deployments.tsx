"use client"

import { useCallback, useEffect, useMemo, useState } from "react"
import { ChevronDownIcon, GitBranchIcon, GitCommitHorizontalIcon, RocketIcon, RotateCcwIcon, RotateCwIcon, ScrollTextIcon, SquareIcon } from "lucide-react"

import { DeploymentTrackBadge } from "@/components/app-status"
import { ConfirmButton } from "@/components/confirm-button"
import { DeployRun } from "@/components/deploy/deploy-run"
import { useErrorText } from "@/components/form-error"
import { StatusIcon, type FeedItem } from "@/components/home/deployments-feed"
import { LogDialog, type LogTarget } from "@/components/home/log-dialog"
import { useTimeAgo } from "@/components/home/sparkline"
import { CardListToolbar, useInfinite } from "@/components/lists/card-list"
import { useLoadedSite } from "@/components/site/context"
import { SectionTitle } from "@/components/states"
import { Button } from "@/components/ui/button"

import { apiFetch } from "@/lib/api"
import { isActive, shortSha, type App, type Deployment } from "@/lib/apps"
import { useI18n } from "@/lib/i18n"
import { canOperate } from "@/lib/servers"
import { useTeam } from "@/lib/team"
import { cn } from "@/lib/utils"

type StatusFilter = "active" | "succeeded" | "failed"

// The per-app list has no cursor; older deployments come from a bigger page, up to the API's maximum.
const PAGE = 30
const MAX = 200

function feedItem(app: App, d: Deployment): FeedItem {
  return {
    ...d,
    app: { id: app.id, name: app.name, title: app.title, color: app.color, icon: app.icon, logo_url: app.logo_url },
    branch: d.ref,
  }
}

/** Deploy management: every deployment as a card (the team deployments page's style), rollback and restart. */
export function SiteDeployments() {
  const { t, locale } = useI18n()
  const { team } = useTeam()
  const ago = useTimeAgo(locale)
  const errorText = useErrorText()
  const operate = canOperate(team.role)
  const { detail, app, deployments: recent, pushDeployment, setError, path } = useLoadedSite()
  const [older, setOlder] = useState<Deployment[]>([])
  const [limit, setLimit] = useState(PAGE)
  const [exhausted, setExhausted] = useState(false)
  const [loadingMore, setLoadingMore] = useState(false)
  const [busy, setBusy] = useState(false)
  const [q, setQ] = useState("")
  const [chosen, setChosen] = useState<Record<string, string[]>>({})
  const [sort, setSort] = useState("recent")
  // Cards start folded except the newest; a click unfolds the steps, the log button opens the log dialog.
  const [open, setOpen] = useState<Set<string>>(new Set())
  const [logOf, setLogOf] = useState<LogTarget | null>(null)
  const toggle = (id: string) =>
    setOpen((o) => {
      const n = new Set(o)
      if (!n.delete(id)) n.add(id)
      return n
    })

  // The stream keeps the recent ones current; pages fetched on scroll add what is older.
  const deps = useMemo(() => {
    const seen = new Set(recent.map((d) => d.id))
    return [...recent, ...older.filter((d) => !seen.has(d.id))].sort((a, b) => b.created_at.localeCompare(a.created_at))
  }, [recent, older])

  // The newest deployment starts unfolded; ?d=<id> (from the team feed or search) unfolds that one.
  const newest = deps[0]?.id
  useEffect(() => {
    if (newest) setOpen((o) => (o.size ? o : new Set([newest])))
  }, [newest])
  useEffect(() => {
    const want = new URLSearchParams(window.location.search).get("d")
    if (want && deps.some((d) => d.id === want)) {
      setOpen(new Set([want]))
      requestAnimationFrame(() => document.querySelector(`[data-deployment="${CSS.escape(want)}"]`)?.scrollIntoView({ block: "start" }))
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [deps.length > 0])

  const loadMore = useCallback(async () => {
    if (exhausted || loadingMore) return
    const next = Math.min(limit + PAGE, MAX)
    setLoadingMore(true)
    try {
      const r = await apiFetch<{ deployments: Deployment[] }>(`${path}/deployments?limit=${next}`)
      setOlder(r.deployments ?? [])
      setLimit(next)
      if ((r.deployments ?? []).length < next || next >= MAX) setExhausted(true)
    } catch (err) {
      setError(errorText(err))
      setExhausted(true)
    } finally {
      setLoadingMore(false)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path, limit, exhausted, loadingMore])

  const is = (d: Deployment, s: StatusFilter) =>
    s === "active" ? isActive(d) : s === "failed" ? d.status === "failed" || d.status === "cancelled" : d.status === s
  const count = (s: StatusFilter) => deps.filter((d) => is(d, s)).length

  const shown = useMemo(() => {
    const needle = q.trim().toLowerCase()
    return deps.filter(
      (d) =>
        (!(chosen.status ?? []).length || chosen.status.some((x) => is(d, x as StatusFilter))) &&
        (!(chosen.kind ?? []).length || chosen.kind.includes(d.kind)) &&
        (!needle ||
          [`#${d.number}`, d.status, d.kind, d.trigger, d.commit_sha, d.commit_message, d.ref]
            .filter(Boolean)
            .some((v) => (v as string).toLowerCase().includes(needle))),
    )
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [deps, q, chosen])
  const ordered = useMemo(() => (sort === "oldest" ? [...shown].reverse() : shown), [shown, sort])
  const { visible, more: moreShown, sentinel } = useInfinite(ordered, JSON.stringify([q, chosen, sort]))
  // Past the last loaded deployment: fetch older ones as the list scrolls to its end.
  useEffect(() => {
    if (!moreShown && !exhausted && !loadingMore && deps.length >= PAGE) void loadMore()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [moreShown, visible.length])

  const kinds = useMemo(() => [...new Set(deps.map((d) => d.kind))].sort(), [deps])
  const live = detail.live_release
  const prev = detail.previous_release

  function started(d: Deployment) {
    pushDeployment(d)
    setOpen(new Set([d.id]))
  }

  return (
    <section aria-labelledby="deps-h">
      <SectionTitle
        id="deps-h"
        title={t("site.nav.deployments")}
        hint={t("site.deploymentsHint")}
        action={
          operate ? (
            <span className="flex flex-wrap items-center gap-2">
              {prev ? (
                <ConfirmButton
                  icon={<RotateCcwIcon />}
                  label={t("apps.actions.rollbackTo", { version: prev.version })}
                  title={t("apps.actions.rollbackTitle", { version: prev.version })}
                  description={t("apps.actions.rollbackHint", { version: prev.version, name: app.name })}
                  confirmLabel={t("apps.actions.rollback")}
                  onConfirm={async () => {
                    try {
                      const r = await apiFetch<{ deployment: Deployment }>(`${path}/rollback`, { method: "POST", body: { release: prev.id } })
                      started(r.deployment)
                    } catch (err) {
                      throw new Error(errorText(err))
                    }
                  }}
                />
              ) : null}
              <Button
                size="sm"
                variant="secondary"
                disabled={busy || !live}
                title={live ? undefined : t("apps.actions.noLive")}
                onClick={async () => {
                  setBusy(true)
                  setError(null)
                  try {
                    started(await apiFetch<Deployment>(`${path}/restart`, { method: "POST" }))
                  } catch (err) {
                    setError(errorText(err))
                  } finally {
                    setBusy(false)
                  }
                }}
              >
                <RotateCwIcon />
                {t("apps.actions.restart")}
              </Button>
            </span>
          ) : null
        }
      />
      {deps.length === 0 ? (
        <div className="flex flex-col items-start gap-3 rounded-2xl border border-dashed border-border px-6 py-10">
          <RocketIcon className="size-6 text-muted-foreground" />
          <p className="text-sm text-muted-foreground">{t("apps.deploy.empty")}</p>
        </div>
      ) : (
        <div className="flex flex-col gap-4" data-testid="deployments">
          <CardListToolbar
            q={q}
            onQ={setQ}
            placeholder={t("site.searchDeployments")}
            chosen={chosen}
            onChosen={setChosen}
            facets={[
              {
                id: "status",
                title: t("lists.status"),
                options: (["active", "succeeded", "failed"] as const).map((v) => ({
                  value: v,
                  label: v === "active" ? t("deploys2.active") : t(`apps.deploy.statuses.${v}`),
                  count: count(v),
                })),
              },
              ...(kinds.length > 1
                ? [{ id: "kind", title: t("apps.deploy.kind"), options: kinds.map((k) => ({ value: k, label: t(`apps.deploy.kinds.${k}`, undefined, k) })) }]
                : []),
            ]}
            sort={sort}
            onSort={setSort}
            sorts={[
              ["recent", t("lists.sortActivity")],
              ["oldest", t("lists.sortOldest")],
            ]}
          />
          {ordered.length === 0 ? (
            <p className="rounded-2xl border border-dashed border-border px-4 py-8 text-center text-body-sm text-muted-foreground">
              {t("deploys2.noMatch")}
            </p>
          ) : (
            <div className="flex flex-col gap-4">
              {visible.map((d) => {
                const failed = d.status === "failed"
                const isOpen = open.has(d.id)
                return (
                  <div key={d.id} data-deployment={d.id} className="overflow-hidden rounded-2xl border border-border bg-card">
                    <div className="flex items-center gap-3 px-4 py-3">
                      <button
                        type="button"
                        onClick={() => toggle(d.id)}
                        aria-expanded={isOpen}
                        className="flex min-w-0 flex-1 items-center gap-3 text-start outline-none focus-visible:outline-2 focus-visible:outline-nq-focus"
                      >
                        <span className="flex min-w-0 flex-1 flex-col gap-1">
                          <span className="flex min-w-0 flex-wrap items-center gap-2 text-body-sm font-medium text-foreground">
                            <StatusIcon status={d.status} />
                            <span dir="ltr">#{d.number}</span>
                            <span>{t(`apps.deploy.kinds.${d.kind}`, undefined, d.kind)}</span>
                            <span className="text-caption font-normal text-muted-foreground">{t(`apps.deploy.statuses.${d.status}`, undefined, d.status)}</span>
                            <DeploymentTrackBadge track={d.track} reason={d.track_reason} />
                          </span>
                          <span className="flex flex-wrap items-center gap-x-3 gap-y-1 text-caption text-muted-foreground">
                            {d.commit_sha ? (
                              <span className="flex min-w-0 items-center gap-1">
                                <GitCommitHorizontalIcon className="size-3.5 shrink-0" />
                                <code dir="ltr" className="rounded-sm bg-secondary px-1 font-mono text-code">
                                  {shortSha(d.commit_sha)}
                                </code>
                                {d.commit_message ? (
                                  <span dir="auto" className="max-w-[28rem] truncate">
                                    {d.commit_message}
                                  </span>
                                ) : null}
                              </span>
                            ) : null}
                            {d.ref ? (
                              <span className="flex items-center gap-1">
                                <GitBranchIcon className="size-3.5" />
                                <span dir="ltr">{d.ref}</span>
                              </span>
                            ) : null}
                            <span>{t(`apps.deploy.triggers.${d.trigger}`, undefined, d.trigger)}</span>
                            <span title={new Date(d.created_at).toLocaleString(locale)}>{ago(d.created_at)}</span>
                          </span>
                          <QueueNote d={d} />
                        </span>
                        <ChevronDownIcon aria-hidden className={cn("size-4 shrink-0 text-muted-foreground transition-transform", isOpen && "rotate-180")} />
                      </button>
                      {operate && isActive(d) && !d.cancel_requested ? <CancelButton path={`${path}/deployments/${d.id}/cancel`} onError={setError} /> : null}
                      <Button
                        size="sm"
                        variant={failed ? "destructive" : "outline"}
                        onClick={() => setLogOf({ app: app.name, appTitle: app.title || app.name, id: d.id, number: d.number })}
                      >
                        <ScrollTextIcon aria-hidden />
                        {failed ? t("deploys2.openFailedLog") : t("deploys2.openLog")}
                      </Button>
                    </div>
                    {isOpen ? (
                      <div className="border-t border-border p-4">
                        <DeployRun team={team.slug} d={feedItem(app, d)} />
                      </div>
                    ) : null}
                  </div>
                )
              })}
            </div>
          )}
          {moreShown || !exhausted ? <div ref={sentinel} className="h-8" aria-hidden /> : null}
          <p className="text-caption text-muted-foreground">
            {loadingMore ? t("common.loading") : t("lists.count", { shown: visible.length, total: deps.length })}
          </p>
        </div>
      )}
      <LogDialog team={team.slug} target={logOf} onClose={() => setLogOf(null)} />
    </section>
  )
}

function CancelButton({ path, onError }: { path: string; onError: (m: string | null) => void }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [busy, setBusy] = useState(false)
  return (
    <Button
      size="sm"
      variant="secondary"
      disabled={busy}
      onClick={async () => {
        setBusy(true)
        try {
          await apiFetch(path, { method: "POST" })
        } catch (err) {
          onError(errorText(err))
        } finally {
          setBusy(false)
        }
      }}
    >
      <SquareIcon />
      {t("apps.deploy.cancel")}
    </Button>
  )
}

/** Build-queue state: a push waiting out the debounce, the pushes it coalesces, or why one was skipped. */
function QueueNote({ d }: { d: Deployment }) {
  const { t, locale } = useI18n()
  const notes: string[] = []
  if (d.status === "queued" && d.not_before && new Date(d.not_before).getTime() > Date.now())
    notes.push(t("apps.deploy.waiting", { time: new Date(d.not_before).toLocaleTimeString(locale, { hour: "2-digit", minute: "2-digit" }) }))
  if ((d.coalesced ?? 0) > 0) notes.push(t("apps.deploy.coalesced", { count: d.coalesced ?? 0 }))
  if (d.status === "skipped") notes.push(t("apps.deploy.skippedWhy", { reason: (d.error ?? "").replace(/^skipped: /, "") }))
  if (!notes.length) return null
  return (
    <span dir="auto" className="text-caption text-muted-foreground">
      {notes.join(" · ")}
    </span>
  )
}
