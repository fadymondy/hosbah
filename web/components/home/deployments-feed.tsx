"use client"

import Link from "next/link"
import { CheckCircle2Icon, CircleDashedIcon, GitCommitHorizontalIcon, XCircleIcon } from "lucide-react"

import { useTimeAgo } from "@/components/home/sparkline"
import type { DeploymentStatus } from "@/lib/apps"
import { useI18n } from "@/lib/i18n"

/** One row of the team's deployment feed (GET /api/teams/{team}/deployments). */
export type FeedItem = {
  id: string
  number: number
  app: { id: string; name: string }
  server?: { id: string; name: string } | null
  status: DeploymentStatus
  branch?: string | null
  commit_sha?: string | null
  commit_message?: string | null
  trigger?: string | null
  triggered_by?: { email: string } | null
  created_at: string
}

function StatusIcon({ status }: { status: DeploymentStatus }) {
  const { t } = useI18n()
  const label = t(`apps.deploy.statuses.${status}`, undefined, status)
  if (status === "succeeded")
    return <CheckCircle2Icon className="size-4 shrink-0 text-[var(--grid-ok,#3fb950)]" aria-label={label} />
  if (status === "failed" || status === "cancelled")
    return <XCircleIcon className="size-4 shrink-0 text-grid-danger" aria-label={label} />
  return <CircleDashedIcon className="size-4 shrink-0 animate-spin text-grid-brand [animation-duration:3s]" aria-label={label} />
}

/** Laravel Cloud's "Latest deployments": commit, app · branch, message, and who shipped it when. */
export function DeploymentsFeed({ items, base }: { items: FeedItem[]; base: string }) {
  const { t, locale } = useI18n()
  const ago = useTimeAgo(locale)
  return (
    <ul className="divide-y divide-line border border-line bg-grid-card">
      {items.map((d) => (
        <li key={d.id}>
          <Link
            href={`${base}/apps/${d.app.name}`}
            className="grid grid-cols-[auto_4.5rem_minmax(0,1fr)] items-center gap-x-4 gap-y-1 px-4 py-3 text-sm hover:bg-grid-soft md:grid-cols-[auto_4.5rem_minmax(0,11rem)_minmax(0,1fr)_auto]"
          >
            <StatusIcon status={d.status} />
            <span dir="ltr" className="font-mono text-xs text-grid-fg">
              {d.commit_sha ? d.commit_sha.slice(0, 7) : `#${d.number}`}
            </span>
            <span className="flex min-w-0 items-center gap-1.5">
              <span className="flex size-5 shrink-0 items-center justify-center border border-line bg-background text-xs font-medium text-grid-brand uppercase">
                {d.app.name.slice(0, 1)}
              </span>
              <span dir="ltr" className="truncate text-grid-fg">
                {d.app.name}
                {d.branch ? <span className="text-grid-muted"> · {d.branch}</span> : null}
              </span>
            </span>
            <span className="col-span-2 flex min-w-0 items-center gap-1.5 text-grid-muted md:col-span-1">
              <GitCommitHorizontalIcon className="size-3.5 shrink-0" />
              <span dir="auto" className="truncate">
                {d.commit_message || t(`home.trigger.${d.trigger ?? "manual"}`, undefined, d.trigger ?? "")}
              </span>
            </span>
            <span className="col-span-3 text-xs text-grid-muted md:col-span-1 md:text-end">
              {d.branch ? (
                <>
                  {t("home.deployedFrom")}{" "}
                  <code dir="ltr" className="border border-line bg-background px-1 font-mono">
                    {d.branch}
                  </code>{" "}
                </>
              ) : null}
              {ago(d.created_at)}
              {d.triggered_by?.email ? (
                <>
                  {" "}
                  {t("home.by")}{" "}
                  <span dir="ltr" className="text-grid-fg">
                    {d.triggered_by.email}
                  </span>
                </>
              ) : null}
            </span>
          </Link>
        </li>
      ))}
    </ul>
  )
}
