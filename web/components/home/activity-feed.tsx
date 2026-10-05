"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { Card, Chip, ChipGroup, Input, Timeline, TimelineItem } from "@fadymondy/nasaq/web"
import { HistoryIcon, LoaderCircleIcon, RocketIcon, SearchIcon } from "lucide-react"

import { DeploymentStatusBadge } from "@/components/app-status"
import type { FeedItem } from "@/components/home/deployments-feed"
import { ActivityRowSkeleton } from "@/components/home/skeletons"
import { LogDialog, type LogTarget } from "@/components/home/log-dialog"
import { apiFetch, type AuditEntry } from "@/lib/api"
import { useI18n } from "@/lib/i18n"
import { useEventStream } from "@/lib/servers"

const PAGE = 25

const DEPLOY_TONE: Record<string, string> = {
  succeeded: "text-nq-success-text",
  failed: "text-nq-danger-text",
  cancelled: "text-muted-foreground",
  running: "text-nq-info",
  queued: "text-nq-warning-text",
}

type Filter = "all" | "deploys" | "failed" | "audit"
type Event = { kind: "deploy"; at: string; d: FeedItem } | { kind: "audit"; at: string; a: AuditEntry }

/** Merges b into a by id, newer copies winning, newest first. */
function mergeById<T extends { id: string | number; created_at: string }>(a: T[], b: T[]): T[] {
  const m = new Map(a.map((x) => [String(x.id), x]))
  for (const x of b) m.set(String(x.id), x)
  return [...m.values()].sort((x, y) => Date.parse(y.created_at) - Date.parse(x.created_at))
}

/**
 * The home page's live activity: deployments and the audit log (owners and admins; others see deployments
 * only) in one timeline. The newest page refreshes every 5 s while the tab is visible and at once on any
 * team event; older pages load as you scroll. Search and the filter chips work on everything loaded.
 */
export function ActivityFeed({ team }: { team: string }) {
  const { t } = useI18n()
  const api = `/api/teams/${team}`
  const [deploys, setDeploys] = useState<FeedItem[] | null>(null)
  const [audit, setAudit] = useState<AuditEntry[]>([])
  const [cursor, setCursor] = useState<string | null>(null)
  const [deploysDone, setDeploysDone] = useState(false)
  const [auditOffset, setAuditOffset] = useState(0)
  const [auditDone, setAuditDone] = useState(false)
  const [loadingMore, setLoadingMore] = useState(false)
  const [filter, setFilter] = useState<Filter>("all")
  const [query, setQuery] = useState("")
  const [log, setLog] = useState<LogTarget | null>(null)
  const sentinel = useRef<HTMLDivElement>(null)

  const refreshHead = useCallback(async () => {
    const [f, l] = await Promise.all([
      apiFetch<{ deployments: FeedItem[]; next_cursor: string | null }>(`${api}/deployments?limit=${PAGE}`).catch(() => null),
      apiFetch<{ entries: AuditEntry[] }>(`${api}/audit?limit=${PAGE}`).catch(() => null),
    ])
    setDeploys((cur) => {
      if (cur === null) {
        setCursor(f?.next_cursor ?? null)
        setDeploysDone(!f?.next_cursor)
      }
      return mergeById(cur ?? [], f?.deployments ?? [])
    })
    setAudit((cur) => {
      if (!l) setAuditDone(true)
      else if (cur.length === 0) {
        setAuditOffset(l.entries.length)
        setAuditDone(l.entries.length < PAGE)
      }
      return mergeById(cur, l?.entries ?? [])
    })
  }, [api])

  useEffect(() => {
    setDeploys(null)
    setAudit([])
    void refreshHead()
    const id = setInterval(() => {
      if (document.visibilityState === "visible") void refreshHead()
    }, 5_000)
    return () => clearInterval(id)
  }, [refreshHead])

  useEventStream(`${api}/notifications/stream`, { notifications: () => void refreshHead() })

  const loadMore = useCallback(async () => {
    if (loadingMore || deploys === null || (deploysDone && auditDone)) return
    setLoadingMore(true)
    try {
      const [f, l] = await Promise.all([
        !deploysDone && cursor
          ? apiFetch<{ deployments: FeedItem[]; next_cursor: string | null }>(
              `${api}/deployments?limit=${PAGE}&before=${encodeURIComponent(cursor)}`,
            ).catch(() => null)
          : null,
        !auditDone ? apiFetch<{ entries: AuditEntry[] }>(`${api}/audit?limit=${PAGE}&offset=${auditOffset}`).catch(() => null) : null,
      ])
      if (!deploysDone) {
        setDeploys((cur) => mergeById(cur ?? [], f?.deployments ?? []))
        setCursor(f?.next_cursor ?? null)
        setDeploysDone(!f?.next_cursor)
      }
      if (!auditDone) {
        setAudit((cur) => mergeById(cur, l?.entries ?? []))
        setAuditOffset((o) => o + (l?.entries.length ?? 0))
        setAuditDone(!l || l.entries.length < PAGE)
      }
    } finally {
      setLoadingMore(false)
    }
  }, [api, auditDone, auditOffset, cursor, deploys, deploysDone, loadingMore])

  useEffect(() => {
    const el = sentinel.current
    if (!el) return
    const io = new IntersectionObserver((entries) => entries[0]?.isIntersecting && void loadMore(), { rootMargin: "200px" })
    io.observe(el)
    return () => io.disconnect()
  }, [loadMore])

  const events = useMemo<Event[] | null>(() => {
    if (deploys === null) return null
    const q = query.trim().toLowerCase()
    const list: Event[] = [
      ...(filter === "audit" ? [] : deploys)
        .filter((d) => filter !== "failed" || d.status === "failed")
        .map((d) => ({ kind: "deploy" as const, at: d.created_at, d })),
      // A deploy already has its own row; skip its audit twin.
      ...(filter === "deploys" || filter === "failed" ? [] : audit)
        .filter((a) => !a.action.startsWith("deployment."))
        .map((a) => ({ kind: "audit" as const, at: a.created_at, a })),
    ]
    return list
      .filter((e) => {
        if (!q) return true
        const hay =
          e.kind === "deploy"
            ? [e.d.app.name, e.d.app.title, e.d.commit_message, e.d.branch, e.d.status, e.d.triggered_by?.email]
            : [e.a.action, e.a.subject_type, e.a.causer_email]
        return hay.some((v) => v?.toLowerCase().includes(q))
      })
      .sort((x, y) => Date.parse(y.at) - Date.parse(x.at))
  }, [deploys, audit, filter, query])

  const done = deploysDone && auditDone

  return (
    <Card className="gap-0 rounded-2xl p-0" role="region" aria-label={t("home3.activity")}>
      <div className="flex flex-col gap-3 px-5 pt-4 pb-3">
        <div className="flex flex-col gap-3">
          <ChipGroup value={filter} onValueChange={(v) => setFilter(v as Filter)} aria-label={t("home3.filterActivity")}>
            {(["all", "deploys", "failed", "audit"] as const).map((f) => (
              <Chip key={f} value={f} className="rounded-full">
                {t(`home3.activityFilter.${f}`)}
              </Chip>
            ))}
          </ChipGroup>
          <div className="relative w-full">
            <SearchIcon className="pointer-events-none absolute start-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder={t("home3.searchActivity")}
              aria-label={t("home3.searchActivity")}
              className="rounded-xl ps-9"
            />
          </div>
        </div>
      </div>
      <div className="max-h-[calc(100vh-14rem)] min-h-80 overflow-y-auto px-5 pb-5">
        {events === null ? (
          <div className="flex flex-col gap-5 pt-1">
            {[0, 1, 2, 3].map((i) => (
              <ActivityRowSkeleton key={i} />
            ))}
          </div>
        ) : events.length === 0 && done ? (
          <p className="text-body-sm text-muted-foreground">
            {query || filter !== "all" ? t("home3.noActivityMatch") : t("home.noDeploysYet")}
          </p>
        ) : (
          <Timeline>
            {events.map((e) =>
              e.kind === "deploy" ? (
                <TimelineItem
                  key={`d${e.d.id}`}
                  icon={<RocketIcon className={DEPLOY_TONE[e.d.status] ?? "text-muted-foreground"} />}
                  title={
                    <span className="flex flex-wrap items-center gap-2">
                      <span dir="auto">{t("home3.deployed", { app: e.d.app.title || e.d.app.name, n: e.d.number })}</span>
                      <DeploymentStatusBadge status={e.d.status} />
                    </span>
                  }
                  description={e.d.commit_message ? <span dir="auto">{e.d.commit_message}</span> : undefined}
                  time={e.at}
                >
                  <button
                    type="button"
                    className="text-caption text-nq-brand hover:underline"
                    onClick={() => setLog({ app: e.d.app.name, appTitle: e.d.app.title || e.d.app.name, id: e.d.id, number: e.d.number })}
                  >
                    {t("deploys2.openLog")}
                  </button>
                </TimelineItem>
              ) : (
                <TimelineItem
                  key={`a${e.a.id}`}
                  icon={<HistoryIcon className="text-muted-foreground" />}
                  title={
                    <span>
                      <code dir="ltr" className="font-mono text-body-sm">
                        {e.a.action}
                      </code>{" "}
                      <span className="text-muted-foreground" dir="ltr">
                        {e.a.subject_type}
                      </span>
                    </span>
                  }
                  description={<span dir="ltr">{e.a.causer_email ?? t("audit.system")}</span>}
                  time={e.at}
                />
              ),
            )}
          </Timeline>
        )}
        <div ref={sentinel} className="flex justify-center pt-3 text-caption text-muted-foreground">
          {loadingMore ? (
            <LoaderCircleIcon className="size-4 animate-spin motion-reduce:animate-none" aria-label={t("common.loading")} />
          ) : null}
          {done && events?.length ? t("home3.endOfActivity") : null}
        </div>
      </div>
      <LogDialog team={team} target={log} onClose={() => setLog(null)} />
    </Card>
  )
}
