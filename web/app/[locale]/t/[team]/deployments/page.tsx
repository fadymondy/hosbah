"use client"

import Link from "next/link"
import { useCallback, useEffect, useMemo, useState } from "react"
import { GitBranchIcon, GitCommitHorizontalIcon, RocketIcon, TriangleAlertIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { duration, isActive as active, Pipeline } from "@/components/deploy/pipeline"
import { useErrorText } from "@/components/form-error"
import { StatusIcon, type FeedItem } from "@/components/home/deployments-feed"
import { useTimeAgo } from "@/components/home/sparkline"
import { FilterPills, ListBar, SortSelect } from "@/components/lists/list-bar"
import { ServerMark } from "@/components/server-mark"
import { ErrorBand, LoadingRows } from "@/components/states"
import { Button } from "@/components/ui/button"
import { apiFetch } from "@/lib/api"
import { shortSha } from "@/lib/apps"
import { useDocumentTitle, useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"
import { cn } from "@/lib/utils"

type Page = { deployments: FeedItem[]; next_cursor: string | null }
type StatusFilter = "" | "active" | "succeeded" | "failed"

/**
 * Every deployment of the team, newest first, across apps and servers: what shipped, where, who
 * asked, how long it took and — when it failed — at which step and why, with the way to its log.
 * Polls quickly while something is deploying; older pages load on demand.
 */
export default function DeploymentsPage() {
  const { t, locale } = useI18n()
  const { team } = useTeam()
  useDocumentTitle(`${t("nav.deployments")} — ${team.name}`)
  const errorText = useErrorText()
  const ago = useTimeAgo(locale)
  const base = `/${locale}/t/${team.slug}`
  const api = `/api/teams/${team.slug}/deployments`

  const [items, setItems] = useState<FeedItem[] | null>(null)
  const [cursor, setCursor] = useState<string | null>(null)
  const [more, setMore] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [q, setQ] = useState("")
  const [status, setStatus] = useState<StatusFilter>("")
  const [app, setApp] = useState("")
  const [server, setServer] = useState("")

  // The first page, merged over what is loaded so older pages survive a refresh.
  const refresh = useCallback(async () => {
    try {
      const r = await apiFetch<Page>(`${api}?limit=50`)
      setItems((cur) => {
        if (!cur) {
          setCursor(r.next_cursor)
          return r.deployments
        }
        // Keep the older pages already loaded: whatever is past the end of this first page.
        const fresh = new Set(r.deployments.map((d) => d.id))
        const edge = r.deployments.at(-1)?.created_at ?? ""
        return [...r.deployments, ...cur.filter((d) => !fresh.has(d.id) && d.created_at < edge)]
      })
      setError(null)
    } catch (err) {
      setError(errorText(err))
      setItems((x) => x ?? [])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [api])

  const anyActive = (items ?? []).some(active)
  useEffect(() => {
    void refresh()
  }, [refresh])
  useEffect(() => {
    const id = setInterval(refresh, anyActive ? 5_000 : 30_000)
    return () => clearInterval(id)
  }, [refresh, anyActive])

  async function loadMore() {
    if (!cursor) return
    setMore(true)
    try {
      const r = await apiFetch<Page>(`${api}?limit=50&before=${encodeURIComponent(cursor)}`)
      setItems((cur) => {
        const seen = new Set((cur ?? []).map((d) => d.id))
        return [...(cur ?? []), ...r.deployments.filter((d) => !seen.has(d.id))]
      })
      setCursor(r.next_cursor)
    } catch (err) {
      setError(errorText(err))
    } finally {
      setMore(false)
    }
  }

  // ?q= / ?app= (from a link) prefill the filters.
  useEffect(() => {
    const p = new URLSearchParams(window.location.search)
    if (p.get("q")) setQ(p.get("q")!)
    if (p.get("app")) setApp(p.get("app")!)
  }, [])

  const apps = useMemo(() => [...new Set((items ?? []).map((d) => d.app.name))].sort(), [items])
  const servers = useMemo(() => {
    const m = new Map<string, string>()
    for (const d of items ?? []) if (d.server?.id) m.set(d.server.id, d.server.name || d.server.id)
    return [...m.entries()].sort((x, y) => x[1].localeCompare(y[1]))
  }, [items])

  const is = (d: FeedItem, s: StatusFilter) =>
    !s || (s === "active" ? active(d) : s === "failed" ? d.status === "failed" || d.status === "cancelled" : d.status === s)
  const count = (s: StatusFilter) => (items ?? []).filter((d) => is(d, s)).length

  const shown = useMemo(() => {
    const needle = q.trim().toLowerCase()
    return (items ?? []).filter(
      (d) =>
        is(d, status) &&
        (!app || d.app.name === app) &&
        (!server || d.server?.id === server) &&
        (!needle ||
          [d.app.name, d.app.title, d.branch, d.commit_sha, d.commit_message, d.server?.name, d.triggered_by?.email, `#${d.number}`]
            .filter(Boolean)
            .some((v) => (v as string).toLowerCase().includes(needle))),
    )
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [items, q, status, app, server])

  const deployNew = (
    <Button nativeButton={false} render={<Link href={`${base}/apps/new`} />}>
      <RocketIcon />
      {t("home2.deploy")}
    </Button>
  )

  return (
    <PageBody title={t("nav.deployments")} description={t("deploys2.lede")} actions={deployNew} bleed>
      <ErrorBand message={error} />
      {items === null ? (
        <LoadingRows rows={6} />
      ) : items.length === 0 ? (
        <div className="flex flex-col items-start gap-3 px-6 py-10">
          <RocketIcon className="size-6 text-grid-muted" />
          <p className="text-base font-medium text-grid-fg">{t("deploys2.emptyTitle")}</p>
          <p className="max-w-lg text-sm text-grid-muted">{t("deploys2.emptyBody")}</p>
          {deployNew}
        </div>
      ) : (
        <>
          <ListBar q={q} onQ={setQ} placeholder={t("deploys2.search")}>
            <FilterPills
              label={t("lists.status")}
              value={status}
              onChange={setStatus}
              options={[
                ["", t("home.filterAll"), count("")],
                ["active", t("deploys2.active"), count("active")],
                ["succeeded", t("apps.deploy.statuses.succeeded"), count("succeeded")],
                ["failed", t("apps.deploy.statuses.failed"), count("failed")],
              ]}
            />
            {apps.length > 1 ? (
              <SortSelect label={t("nav.apps")} value={app} onChange={setApp} options={[["", t("deploys2.allApps")], ...apps.map((a) => [a, a] as [string, string])]} />
            ) : null}
            {servers.length > 1 ? (
              <SortSelect label={t("nav.servers")} value={server} onChange={setServer} options={[["", t("apps2.allServers")], ...servers]} />
            ) : null}
          </ListBar>

          {shown.length === 0 ? (
            <p className="border-b border-line px-6 py-8 text-center text-sm text-grid-muted">{t("deploys2.noMatch")}</p>
          ) : (
            <ul className="divide-y divide-line border-b border-line">
              {shown.map((d) => {
                const log = `${base}/apps/${encodeURIComponent(d.app.name)}/deployments?d=${d.id}`
                const took = duration(d.started_at ?? d.created_at, d.finished_at ?? (active(d) ? null : d.created_at))
                const failed = d.status === "failed"
                return (
                  <li key={d.id} className={cn("px-4 py-3 sm:px-6", failed && "bg-grid-danger/[0.03]")}>
                    <div className="flex flex-wrap items-start gap-x-3 gap-y-2">
                      <span className="mt-0.5">
                        <StatusIcon status={d.status} />
                      </span>
                      <div className="min-w-0 flex-1 space-y-1.5">
                        <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                          <Link href={`${base}/apps/${encodeURIComponent(d.app.name)}`} className="flex min-w-0 items-center gap-2 hover:underline">
                            <ServerMark server={{ color: d.app.color ?? null, icon: d.app.icon ?? "globe", logo_url: d.app.logo_url ?? null }} size={6} />
                            <span dir="auto" className="truncate font-medium text-grid-fg">
                              {d.app.title || d.app.name}
                            </span>
                          </Link>
                          <Link href={log} dir="ltr" className="text-xs text-grid-muted hover:text-grid-fg hover:underline">
                            #{d.number}
                          </Link>
                          {d.kind && d.kind !== "deploy" ? (
                            <span className="border border-line px-1.5 text-[11px] text-grid-muted">{t(`apps.deploy.kinds.${d.kind}`)}</span>
                          ) : null}
                          <span className="text-xs text-grid-muted">{t(`apps.deploy.statuses.${d.status}`, undefined, d.status)}</span>
                        </div>
                        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-grid-muted">
                          {d.commit_sha ? (
                            <span className="flex min-w-0 items-center gap-1">
                              <GitCommitHorizontalIcon className="size-3.5 shrink-0" />
                              <code dir="ltr" className="text-grid-fg">
                                {shortSha(d.commit_sha)}
                              </code>
                              {d.commit_message ? (
                                <span dir="auto" className="max-w-[28rem] truncate">
                                  {d.commit_message}
                                </span>
                              ) : null}
                            </span>
                          ) : null}
                          {d.branch ? (
                            <span className="flex items-center gap-1">
                              <GitBranchIcon className="size-3.5" />
                              <span dir="ltr">{d.branch}</span>
                            </span>
                          ) : null}
                          {d.server?.id ? (
                            <Link href={`${base}/servers/${d.server.id}`} dir="ltr" className="hover:text-grid-fg hover:underline">
                              {d.server.name || "—"}
                            </Link>
                          ) : null}
                        </div>
                        {d.status !== "succeeded" ? <Pipeline d={d} /> : null}
                        {failed ? (
                          <div className="flex flex-col gap-1 border-s-2 border-grid-danger ps-3 text-xs">
                            <p className="flex items-center gap-1.5 font-medium text-grid-danger">
                              <TriangleAlertIcon className="size-3.5 shrink-0" />
                              {d.step
                                ? t("deploys2.failedAt", { step: t(`apps.deploy.steps.${d.step}`, undefined, d.step) })
                                : t("deploys2.failed")}
                            </p>
                            {d.error ? (
                              <p dir="ltr" className="line-clamp-3 font-mono break-all text-grid-body">
                                {d.error}
                              </p>
                            ) : null}
                            <p className="text-grid-muted">{t(`deploys2.hint.${d.step ?? "none"}`, undefined, t("deploys2.hint.none"))}</p>
                          </div>
                        ) : null}
                      </div>
                      <div className="flex shrink-0 flex-col items-end gap-1 text-xs text-grid-muted max-sm:w-full max-sm:flex-row max-sm:items-center max-sm:justify-between">
                        <span title={new Date(d.created_at).toLocaleString(locale)}>{ago(d.created_at)}</span>
                        {took ? <span dir="ltr" className="tabular-nums">{took}</span> : null}
                        <span className="max-w-[14rem] truncate">
                          {d.triggered_by?.email ? (
                            <span dir="ltr">{d.triggered_by.email}</span>
                          ) : (
                            t(`home.trigger.${d.trigger ?? "manual"}`, undefined, d.trigger ?? "")
                          )}
                        </span>
                        <Link href={log} className={cn("font-medium hover:underline", failed ? "text-grid-danger" : "text-grid-action")}>
                          {failed ? t("deploys2.openFailedLog") : t("deploys2.openLog")}
                        </Link>
                      </div>
                    </div>
                  </li>
                )
              })}
            </ul>
          )}
          <div className="flex items-center justify-between gap-3 px-6 py-3 text-xs text-grid-muted">
            <span>{t("lists.count", { shown: shown.length, total: items.length })}</span>
            {cursor ? (
              <Button variant="outline" size="sm" onClick={loadMore} disabled={more}>
                {more ? t("common.loading") : t("deploys2.older")}
              </Button>
            ) : null}
          </div>
        </>
      )}
    </PageBody>
  )
}
