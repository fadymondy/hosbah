"use client"

import { Badge, Button, toast } from "@fadymondy/nasaq/web"
import Link from "next/link"
import { useCallback, useEffect, useState } from "react"
import {
  ExternalLinkIcon,
  FileDiffIcon,
  GitBranchIcon,
  GitCommitHorizontalIcon,
  GitPullRequestIcon,
  GithubIcon,
  HammerIcon,
  RefreshCwIcon,
  RocketIcon,
} from "lucide-react"

import { DeploymentStatusBadge } from "@/components/app-status"
import { ConfirmButton } from "@/components/confirm-button"
import { useErrorText } from "@/components/form-error"
import { useTimeAgo } from "@/components/home/sparkline"
import { ListSearch, matches } from "@/components/server-tabs/list-tools"
import { useLoadedSite } from "@/components/site/context"
import { ErrorBand, LoadingRows, SectionTitle } from "@/components/states"


import { apiFetch, ApiError } from "@/lib/api"
import type { Deployment } from "@/lib/apps"
import { useI18n } from "@/lib/i18n"
import { canOperate } from "@/lib/servers"
import { useTeam } from "@/lib/team"
import { cn } from "@/lib/utils"

type Commit = {
  sha: string
  short_sha: string
  message: string
  author_name?: string | null
  author_login?: string | null
  author_avatar_url?: string | null
  committed_at?: string | null
  url: string
  is_live?: boolean
  deployed?: { number: number; status: Deployment["status"] } | null
}
type Summary = {
  repo: { owner: string; name: string; url: string; branch: string }
  live: { sha: string | null; version: number } | null
  head: { sha: string } | null
  ahead_by: number | null
  behind_by: number | null
}
type Compare = {
  base: string
  head: string
  ahead_by: number
  behind_by: number
  total_commits: number
  files: { filename: string; status: string; additions: number; deletions: number }[]
  truncated: boolean
}
type Pull = { number: number; title: string; author_login?: string | null; head_ref: string; base_ref: string; draft: boolean; url: string; updated_at?: string }

/**
 * The repository behind the site, read through gh on its server: the commits on the deployed
 * branch (which one is live, which were deployed), what changed since the live release, and the
 * open pull requests. Any commit can be deployed from here.
 */
export function SiteRepository() {
  const { t, locale } = useI18n()
  const { team } = useTeam()
  const ago = useTimeAgo(locale)
  const errorText = useErrorText()
  const operate = canOperate(team.role)
  const { app, path, pushDeployment } = useLoadedSite()
  const base = `/${locale}/t/${team.slug}`
  const [summary, setSummary] = useState<Summary | null>(null)
  const [commits, setCommits] = useState<Commit[] | null>(null)
  const [compare, setCompare] = useState<Compare | null>(null)
  const [pulls, setPulls] = useState<Pull[]>([])
  const [error, setError] = useState<string | null>(null)
  const [notConnected, setNotConnected] = useState(false)
  const [loading, setLoading] = useState(true)
  const [q, setQ] = useState("")

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const s = await apiFetch<Summary>(`${path}/repository`)
      setSummary(s)
      setNotConnected(false)
      const [c, p] = await Promise.all([
        apiFetch<{ commits: Commit[] }>(`${path}/repository/commits?limit=50`),
        apiFetch<{ pulls: Pull[] }>(`${path}/repository/pulls`).catch(() => ({ pulls: [] })),
      ])
      setCommits(c.commits ?? [])
      setPulls(p.pulls ?? [])
      // What is on the branch but not live yet; nothing to compare before the first release.
      setCompare(s.live?.sha ? await apiFetch<Compare>(`${path}/repository/compare`).catch(() => null) : null)
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) setNotConnected(true)
      else setError(errorText(err))
      setCommits((c) => c ?? [])
    } finally {
      setLoading(false)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path])

  useEffect(() => {
    void load()
  }, [load])

  async function deploy(c: Commit, track?: "full") {
    try {
      pushDeployment(await apiFetch<Deployment>(`${path}/deployments`, { method: "POST", body: track ? { ref: c.sha, track } : { ref: c.sha } }))
      toast.success(t("site.repo.deploying", { sha: c.short_sha }))
      void load()
    } catch (err) {
      throw new Error(errorText(err))
    }
  }

  const shown = (commits ?? []).filter((c) => matches(q, c.sha, c.message, c.author_name, c.author_login))

  return (
    <>
      <section aria-labelledby="repo-h">
        <SectionTitle
          id="repo-h"
          title={t("site.nav.repository")}
          hint={t("site.planned.repository.hint")}
          action={
            <span className="flex items-center gap-2">
              {summary ? (
                <Button variant="ghost" size="sm" nativeButton={false} render={<a href={summary.repo.url} target="_blank" rel="noreferrer" />}>
                  <GithubIcon />
                  <span dir="ltr">
                    {summary.repo.owner}/{summary.repo.name}
                  </span>
                  <ExternalLinkIcon />
                </Button>
              ) : null}
              <Button variant="ghost" size="icon-sm" aria-label={t("github.refresh")} disabled={loading} onClick={() => void load()}>
                <RefreshCwIcon className={loading ? "animate-spin" : ""} />
              </Button>
            </span>
          }
        />
        <ErrorBand message={error} />
        {notConnected ? (
          <p className="flex flex-wrap items-center gap-2 border-b border-border px-6 py-6 text-sm text-muted-foreground">
            <GithubIcon className="size-4" />
            {t("github.connectFirst")}
            <Link href={`${base}/servers/${app.server_id}/github`} className="text-nq-brand underline-offset-4 hover:underline">
              {t("github.openServer")}
            </Link>
          </p>
        ) : null}
        {summary ? (
          <dl className="grid grid-cols-1 gap-px border-b border-border bg-border sm:grid-cols-3">
            <Fact label={t("lists.branch")} icon={<GitBranchIcon />}>
              <span dir="ltr" className="font-mono text-sm">
                {summary.repo.branch}
              </span>
            </Fact>
            <Fact label={t("apps.liveRelease")} icon={<RocketIcon />}>
              <span dir="ltr" className="font-mono text-sm">
                {summary.live ? `v${summary.live.version} · ${(summary.live.sha ?? "").slice(0, 7) || "—"}` : t("apps.noLive")}
              </span>
            </Fact>
            <Fact label={t("site.repo.pending")} icon={<GitCommitHorizontalIcon />}>
              <span className="text-sm">
                {summary.ahead_by == null
                  ? "—"
                  : summary.ahead_by === 0
                    ? t("site.repo.upToDate")
                    : t("site.repo.ahead", { n: summary.ahead_by })}
                {summary.behind_by ? <span className="ms-2 text-xs text-nq-danger-text">{t("site.repo.behind", { n: summary.behind_by })}</span> : null}
              </span>
            </Fact>
          </dl>
        ) : null}
      </section>

      {!notConnected ? (
        <section aria-labelledby="commits-h">
          <SectionTitle id="commits-h" title={t("site.repo.commits")} hint={t("site.repo.commitsHint")} />
          {commits === null ? (
            <LoadingRows rows={4} />
          ) : commits.length === 0 ? (
            <p className="border-b border-border px-6 py-6 text-sm text-muted-foreground">{t("site.repo.noCommits")}</p>
          ) : (
            <>
              <ListSearch value={q} onChange={setQ} placeholder={t("site.repo.search")} shown={shown.length} total={commits.length} />
              <ol className="flex flex-col divide-y divide-border border-b border-border" data-testid="site-commits">
                {shown.map((c) => (
                  <li key={c.sha} className={cn("flex flex-wrap items-center gap-x-3 gap-y-2 px-6 py-3", c.is_live && "bg-secondary")}>
                    {c.author_avatar_url ? (
                      // eslint-disable-next-line @next/next/no-img-element
                      <img src={c.author_avatar_url} alt="" className="size-7 shrink-0 border border-border" referrerPolicy="no-referrer" />
                    ) : (
                      <span className="flex size-7 shrink-0 items-center justify-center border border-border text-muted-foreground">
                        <GitCommitHorizontalIcon className="size-4" />
                      </span>
                    )}
                    <span className="flex min-w-0 flex-1 flex-col">
                      <span dir="auto" className="truncate text-sm font-medium text-foreground" title={c.message}>
                        {c.message}
                      </span>
                      <span className="flex flex-wrap items-center gap-x-2 text-xs text-muted-foreground">
                        <a dir="ltr" href={c.url} target="_blank" rel="noreferrer" className="font-mono hover:underline">
                          {c.short_sha}
                        </a>
                        <span dir="auto">{c.author_login ? `@${c.author_login}` : c.author_name}</span>
                        {c.committed_at ? <span>{ago(c.committed_at)}</span> : null}
                      </span>
                    </span>
                    {c.is_live ? <Badge variant="brand">{t("site.repo.live")}</Badge> : null}
                    {c.deployed && !c.is_live ? (
                      <span className="flex items-center gap-1.5 text-xs text-muted-foreground">
                        <span dir="ltr" className="font-mono">
                          #{c.deployed.number}
                        </span>
                        <DeploymentStatusBadge status={c.deployed.status} />
                      </span>
                    ) : null}
                    {operate && !c.is_live ? (
                      <span className="flex items-center gap-1">
                        <ConfirmButton
                          icon={<RocketIcon />}
                          label={t("site.repo.deploy")}
                          title={t("site.repo.deployTitle", { sha: c.short_sha })}
                          description={c.message}
                          confirmLabel={t("apps.actions.deploy")}
                          onConfirm={() => deploy(c)}
                        />
                        <ConfirmButton
                          icon={<HammerIcon />}
                          label={t("site.repo.fullRebuild")}
                          title={t("site.repo.fullRebuildTitle", { sha: c.short_sha })}
                          description={t("site.repo.fullRebuildHint")}
                          confirmLabel={t("site.repo.fullRebuild")}
                          onConfirm={() => deploy(c, "full")}
                        />
                      </span>
                    ) : null}
                  </li>
                ))}
              </ol>
            </>
          )}
        </section>
      ) : null}

      {compare && compare.files.length ? (
        <section aria-labelledby="changes-h">
          <SectionTitle
            id="changes-h"
            title={t("site.repo.changes")}
            hint={t("site.repo.changesHint", { commits: compare.total_commits, files: compare.files.length })}
          />
          <ul className="flex max-h-96 flex-col divide-y divide-border overflow-y-auto border-b border-border">
            {compare.files.map((f) => (
              <li key={f.filename} className="flex items-center gap-3 px-6 py-2 text-xs">
                <FileDiffIcon className="size-3.5 shrink-0 text-muted-foreground" />
                <span dir="ltr" className="min-w-0 flex-1 truncate text-start font-mono text-foreground" title={f.filename}>
                  {f.filename}
                </span>
                <span className="text-muted-foreground">{f.status}</span>
                <span dir="ltr" className="shrink-0 font-mono tabular-nums">
                  <span className="text-nq-success">+{f.additions}</span> <span className="text-nq-danger-text">−{f.deletions}</span>
                </span>
              </li>
            ))}
          </ul>
          {compare.truncated ? <p className="border-b border-border px-6 py-2 text-xs text-muted-foreground">{t("site.repo.truncated")}</p> : null}
        </section>
      ) : null}

      {pulls.length ? (
        <section aria-labelledby="pulls-h">
          <SectionTitle id="pulls-h" title={t("site.repo.pulls")} />
          <ul className="flex flex-col divide-y divide-border border-b border-border">
            {pulls.map((p) => (
              <li key={p.number}>
                <a href={p.url} target="_blank" rel="noreferrer" className="flex flex-wrap items-center gap-x-3 gap-y-1 px-6 py-3 hover:bg-secondary">
                  <GitPullRequestIcon className="size-4 shrink-0 text-nq-brand" />
                  <span dir="ltr" className="font-mono text-xs text-muted-foreground">
                    #{p.number}
                  </span>
                  <span dir="auto" className="min-w-0 flex-1 truncate text-sm font-medium text-foreground">
                    {p.title}
                  </span>
                  {p.draft ? <Badge variant="outline">{t("site.repo.draft")}</Badge> : null}
                  <span dir="ltr" className="font-mono text-xs text-muted-foreground">
                    {p.head_ref} → {p.base_ref}
                  </span>
                  {p.updated_at ? <span className="text-xs text-muted-foreground">{ago(p.updated_at)}</span> : null}
                </a>
              </li>
            ))}
          </ul>
        </section>
      ) : null}
    </>
  )
}

function Fact({ label, icon, children }: { label: string; icon: React.ReactNode; children: React.ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-2 bg-background px-6 py-4">
      <dt className="eyebrow inline-flex items-center gap-1.5 [&_svg]:size-3.5">
        {icon}
        {label}
      </dt>
      <dd className="min-w-0">{children}</dd>
    </div>
  )
}
