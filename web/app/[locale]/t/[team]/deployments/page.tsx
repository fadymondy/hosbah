"use client"

import { useCallback, useEffect, useMemo, useState } from "react"
import { ChevronDownIcon, GitBranchIcon, GitCommitHorizontalIcon, RocketIcon, ScrollTextIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { isActive as active } from "@/components/deploy/pipeline"
import { useErrorText } from "@/components/form-error"
import { StatusIcon, type FeedItem } from "@/components/home/deployments-feed"
import { LogDialog, type LogTarget } from "@/components/home/log-dialog"
import { useTimeAgo } from "@/components/home/sparkline"
import { CardListToolbar, useInfinite } from "@/components/lists/card-list"
import { ServerMark } from "@/components/server-mark"
import { DeployRun } from "@/components/deploy/deploy-run"
import { DeployNowButton } from "@/components/deploy/deploy-now"
import { ErrorBand, LoadingRows } from "@/components/states"
import { Button } from "@/components/ui/button"
import { apiFetch } from "@/lib/api"
import { shortSha } from "@/lib/apps"
import { cn } from "@/lib/utils"
import { useDocumentTitle, useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

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
  const api = `/api/teams/${team.slug}/deployments`

  const [items, setItems] = useState<FeedItem[] | null>(null)
  const [cursor, setCursor] = useState<string | null>(null)
  const [more, setMore] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [q, setQ] = useState("")
  // Rows start folded; a click unfolds the steps, the log button opens the dashboard's log dialog.
  const [open, setOpen] = useState<Set<string>>(new Set())
  const [logOf, setLogOf] = useState<LogTarget | null>(null)
  const toggle = (id: string) =>
    setOpen((o) => {
      const n = new Set(o)
      if (!n.delete(id)) n.add(id)
      return n
    })
  const [chosen, setChosen] = useState<Record<string, string[]>>({})
  const [sort, setSort] = useState("recent")

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
    if (p.get("app")) setChosen({ app: [p.get("app")!] })
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
        (!(chosen.status ?? []).length || chosen.status.some((x) => is(d, x as StatusFilter))) &&
        (!(chosen.app ?? []).length || chosen.app.includes(d.app.name)) &&
        (!(chosen.server ?? []).length || chosen.server.includes(d.server?.id ?? "")) &&
        (!needle ||
          [d.app.name, d.app.title, d.branch, d.commit_sha, d.commit_message, d.server?.name, d.triggered_by?.email, `#${d.number}`]
            .filter(Boolean)
            .some((v) => (v as string).toLowerCase().includes(needle))),
    )
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [items, q, chosen])
  const ordered = useMemo(() => (sort === "oldest" ? [...shown].reverse() : shown), [shown, sort])
  const { visible, more: moreShown, sentinel } = useInfinite(ordered, JSON.stringify([q, chosen, sort]))
  // Past the last loaded page: fetch the next older one as the list scrolls to its end.
  useEffect(() => {
    if (!moreShown && cursor && !more && items?.length) void loadMore()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [moreShown, visible.length])

  const deployNew = (
    <DeployNowButton
      team={team.slug}
      onStarted={(target) => {
        void refresh()
        setLogOf(target)
      }}
    />
  )

  return (
    <PageBody title={t("nav.deployments")} description={t("deploys2.lede")} actions={deployNew} bleed divider={false}>
      <ErrorBand message={error} />
      {items === null ? (
        <LoadingRows rows={6} />
      ) : items.length === 0 ? (
        <div className="flex flex-col items-start gap-3 px-6 py-10">
          <RocketIcon className="size-6 text-muted-foreground" />
          <p className="text-base font-medium text-foreground">{t("deploys2.emptyTitle")}</p>
          <p className="max-w-lg text-sm text-muted-foreground">{t("deploys2.emptyBody")}</p>
          {deployNew}
        </div>
      ) : (
        <div className="flex flex-col gap-4 px-6 pb-6">
          <CardListToolbar
            q={q}
            onQ={setQ}
            placeholder={t("deploys2.search")}
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
              ...(apps.length > 1 ? [{ id: "app", title: t("nav.apps"), options: apps.map((x) => ({ value: x, label: x })) }] : []),
              ...(servers.length > 1
                ? [{ id: "server", title: t("nav.servers"), options: servers.map(([value, label]) => ({ value, label })) }]
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
                const target = { app: d.app.name, appTitle: d.app.title || d.app.name, id: d.id, number: d.number }
                return (
                  <div key={d.id} className="overflow-hidden rounded-2xl border border-border bg-card">
                    <div className="flex items-center gap-3 px-4 py-3">
                      <button
                        type="button"
                        onClick={() => toggle(d.id)}
                        aria-expanded={isOpen}
                        className="flex min-w-0 flex-1 items-center gap-3 text-start outline-none focus-visible:outline-2 focus-visible:outline-nq-focus"
                      >
                        <ServerMark
                          server={{ color: d.app.color ?? null, icon: d.app.icon ?? "globe", logo_url: d.app.logo_url ?? null }}
                          size={8}
                        />
                        <span className="flex min-w-0 flex-1 flex-col gap-1">
                          <span className="flex min-w-0 items-center gap-2 text-body-sm font-medium text-foreground">
                            <StatusIcon status={d.status} />
                            <span dir="auto" className="truncate">
                              {d.app.title || d.app.name}
                            </span>
                            <span dir="ltr" className="text-caption font-normal text-muted-foreground">
                              #{d.number}
                            </span>
                            {d.kind && d.kind !== "deploy" ? (
                              <span className="text-caption font-normal text-muted-foreground">{t(`apps.deploy.kinds.${d.kind}`)}</span>
                            ) : null}
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
                            {d.branch ? (
                              <span className="flex items-center gap-1">
                                <GitBranchIcon className="size-3.5" />
                                <span dir="ltr">{d.branch}</span>
                              </span>
                            ) : null}
                            {d.server?.name ? <span dir="ltr">{d.server.name}</span> : null}
                            <span>
                              {d.triggered_by?.email ? (
                                <bdi dir="ltr">{d.triggered_by.email}</bdi>
                              ) : (
                                t(`home.trigger.${d.trigger ?? "manual"}`, undefined, d.trigger ?? "")
                              )}
                            </span>
                            <span title={new Date(d.created_at).toLocaleString(locale)}>{ago(d.created_at)}</span>
                          </span>
                        </span>
                        <ChevronDownIcon
                          aria-hidden
                          className={cn("size-4 shrink-0 text-muted-foreground transition-transform", isOpen && "rotate-180")}
                        />
                      </button>
                      <Button size="sm" variant={failed ? "destructive" : "outline"} onClick={() => setLogOf(target)}>
                        <ScrollTextIcon aria-hidden />
                        {failed ? t("deploys2.openFailedLog") : t("deploys2.openLog")}
                      </Button>
                    </div>
                    {isOpen ? (
                      <div className="border-t border-border p-4">
                        <DeployRun team={team.slug} d={d} />
                      </div>
                    ) : null}
                  </div>
                )
              })}
            </div>
          )}
          {moreShown || cursor ? <div ref={sentinel} className="h-8" aria-hidden /> : null}
          <p className="text-caption text-muted-foreground">
            {more ? t("common.loading") : t("lists.count", { shown: visible.length, total: items.length })}
          </p>
        </div>
      )}
      <LogDialog team={team.slug} target={logOf} onClose={() => setLogOf(null)} />
    </PageBody>
  )
}
