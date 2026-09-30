"use client"

import { Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, Skeleton } from "@fadymondy/nasaq/web"
import Link from "next/link"
import { useEffect, useMemo, useState } from "react"
import { GithubIcon, LockIcon, SearchIcon } from "lucide-react"

import { useErrorText } from "@/components/form-error"


import { apiFetch } from "@/lib/api"
import { useI18n } from "@/lib/i18n"

type Repo = {
  name: string
  name_with_owner: string
  is_private: boolean
  default_branch?: string | null
  url: string
  updated_at?: string
  description?: string | null
}

/**
 * Pick a repository from the GitHub account connected on the chosen server (gh on that server).
 * Choosing one fills the URL and its default branch; the branch list then comes from GitHub too.
 * When the server has no GitHub connection it says so and links there; typing a URL still works.
 */
export function RepoPicker({
  team,
  serverId,
  connected,
  base,
  value,
  onPick,
  branch,
  onBranch,
}: {
  team: string
  serverId: string
  connected: boolean
  base: string
  value: string
  onPick: (url: string, defaultBranch: string | null) => void
  branch: string
  onBranch: (b: string) => void
}) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const api = `/api/teams/${encodeURIComponent(team)}/servers/${serverId}/github`
  const [repos, setRepos] = useState<Repo[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [q, setQ] = useState("")
  const [branches, setBranches] = useState<string[] | null>(null)

  useEffect(() => {
    if (!connected) return
    setRepos(null)
    setError(null)
    apiFetch<{ repos: Repo[] }>(`${api}/repos?limit=500`)
      .then((r) => setRepos(r.repos ?? []))
      .catch((err) => {
        setRepos([])
        setError(errorText(err))
      })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [api, connected])

  const picked = repos?.find((r) => r.url === value || `${r.url}.git` === value) ?? null

  useEffect(() => {
    setBranches(null)
    if (!picked) return
    const [owner, name] = picked.name_with_owner.split("/")
    apiFetch<{ default: string; branches: string[] }>(`${api}/repos/${encodeURIComponent(owner)}/${encodeURIComponent(name)}/branches`)
      .then((r) => setBranches(r.branches ?? []))
      .catch(() => setBranches(null))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [picked?.name_with_owner])

  const list = useMemo(() => {
    const needle = q.trim().toLowerCase()
    return (repos ?? []).filter((r) => !needle || r.name_with_owner.toLowerCase().includes(needle) || r.description?.toLowerCase().includes(needle))
  }, [repos, q])

  if (!connected) {
    return (
      <p className="flex flex-wrap items-center gap-2 border border-dashed border-border px-3 py-2 text-xs text-muted-foreground">
        <GithubIcon className="size-3.5" />
        {t("github.connectFirst")}
        <Link href={`${base}/servers/${serverId}/github`} className="text-nq-brand underline-offset-4 hover:underline">
          {t("github.openServer")}
        </Link>
      </p>
    )
  }

  return (
    <div className="flex flex-col gap-2">
      <div className="relative">
        <SearchIcon className="pointer-events-none absolute start-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
        <Input
          type="search"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder={t("github.searchRepos")}
          aria-label={t("github.searchRepos")}
          className="ps-8"
        />
      </div>
      {error ? <p className="text-xs text-nq-danger-text">{error}</p> : null}
      <div className="max-h-64 overflow-y-auto border border-border" role="listbox" aria-label={t("github.pickRepo")}>
        {repos === null ? (
          <div className="flex flex-col gap-2 p-3">
            <span className="text-xs text-muted-foreground">{t("github.loadingRepos")}</span>
            <Skeleton className="h-5 w-2/3" />
            <Skeleton className="h-5 w-1/2" />
          </div>
        ) : list.length === 0 ? (
          <p className="p-3 text-sm text-muted-foreground">{t("github.noRepos")}</p>
        ) : (
          list.map((r) => {
            const on = picked?.name_with_owner === r.name_with_owner
            return (
              <button
                key={r.name_with_owner}
                type="button"
                role="option"
                aria-selected={on}
                onClick={() => onPick(r.url, r.default_branch ?? null)}
                className={
                  on
                    ? "flex w-full items-center gap-2 border-b border-border bg-secondary px-3 py-2 text-start text-sm last:border-b-0"
                    : "flex w-full items-center gap-2 border-b border-border px-3 py-2 text-start text-sm last:border-b-0 hover:bg-secondary"
                }
              >
                <GithubIcon className="size-3.5 shrink-0 text-muted-foreground" />
                <span dir="ltr" className="min-w-0 flex-1 truncate font-medium text-foreground">
                  {r.name_with_owner}
                </span>
                {r.is_private ? (
                  <span className="inline-flex shrink-0 items-center gap-1 text-xs text-muted-foreground">
                    <LockIcon className="size-3" />
                    {t("github.private")}
                  </span>
                ) : null}
              </button>
            )
          })
        )}
      </div>
      {picked && branches?.length ? (
        <label className="flex items-center gap-2 text-xs text-muted-foreground">
          {t("github.branch")}
          <Select value={branch} onValueChange={(v) => v && onBranch(v as string)}>
            <SelectTrigger dir="ltr" className="h-8 w-auto gap-2 font-mono text-sm">
              <SelectValue>{() => branch}</SelectValue>
            </SelectTrigger>
            <SelectContent>
              {branches.map((b) => (
                <SelectItem key={b} value={b}>
                  <span dir="ltr">{b}</span>
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </label>
      ) : null}
    </div>
  )
}
