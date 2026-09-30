"use client"

import { Field, FieldDescription, FieldLabel, Input, Skeleton } from "@fadymondy/nasaq/web"
import { useEffect, useMemo, useState } from "react"
import { LockIcon, SearchIcon } from "lucide-react"

import { ProviderMark } from "@/components/auth/oauth"
import { useErrorText } from "@/components/form-error"



import { apiFetch } from "@/lib/api"
import { useI18n } from "@/lib/i18n"

type AppRepo = {
  installation_id: number
  name: string
  name_with_owner: string
  is_private: boolean
  default_branch: string
  url: string
  description?: string
}

/**
 * "From the GitHub App": pick a repository from the team's GitHub App installations (no server involved: deploys of these
 * clone with a fresh installation token). Renders nothing when the team has no installation, or the
 * caller may not list them.
 */
export function AppRepoPicker({ team, value, onPick }: { team: string; value: string; onPick: (url: string, defaultBranch: string | null) => void }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [repos, setRepos] = useState<AppRepo[] | null>(null)
  const [installations, setInstallations] = useState<number | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [q, setQ] = useState("")

  useEffect(() => {
    let live = true
    const base = `/api/teams/${encodeURIComponent(team)}/github/app`
    // The status is a database read; GitHub is asked only when the team has an installation.
    apiFetch<{ installations: unknown[] }>(base)
      .then((st) => {
        if (!live) return null
        const n = st.installations?.length ?? 0
        setInstallations(n)
        return n ? apiFetch<{ repos: AppRepo[]; installations: number }>(`${base}/repos`) : null
      })
      .then((r) => {
        if (!r) return
        if (!live) return
        setRepos(r.repos ?? [])
      })
      .catch((err) => {
        if (!live) return
        setRepos([])
        // 403 (a viewer) or 404: nothing to offer. Anything else is worth saying.
        const status = (err as { status?: number }).status
        setInstallations(status === 403 || status === 404 ? 0 : 1)
        if (status !== 403 && status !== 404) setError(errorText(err))
      })
    return () => {
      live = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [team])

  const list = useMemo(() => {
    const needle = q.trim().toLowerCase()
    return (repos ?? []).filter((r) => !needle || r.name_with_owner.toLowerCase().includes(needle) || r.description?.toLowerCase().includes(needle))
  }, [repos, q])

  if (!installations) return null
  const picked = (repos ?? []).find((r) => r.url === value || `${r.url}.git` === value)?.name_with_owner

  return (
    <Field data-testid="app-repo-picker">
      <FieldLabel>{t("github.app.fromApp")}</FieldLabel>
      <div className="relative">
        <SearchIcon className="pointer-events-none absolute start-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
        <Input
          type="search"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder={t("github.app.search")}
          aria-label={t("github.app.search")}
          className="ps-8"
        />
      </div>
      {error ? <p className="text-xs text-nq-danger-text">{error}</p> : null}
      <div className="max-h-64 overflow-y-auto border border-border" role="listbox" aria-label={t("github.app.fromApp")}>
        {repos === null ? (
          <div className="flex flex-col gap-2 p-3">
            <span className="text-xs text-muted-foreground">{t("github.app.loading")}</span>
            <Skeleton className="h-5 w-2/3" />
            <Skeleton className="h-5 w-1/2" />
          </div>
        ) : list.length === 0 ? (
          <p className="p-3 text-sm text-muted-foreground">{t("github.app.noRepos")}</p>
        ) : (
          list.map((r) => {
            const on = picked === r.name_with_owner
            return (
              <button
                key={r.name_with_owner}
                type="button"
                role="option"
                aria-selected={on}
                onClick={() => onPick(r.url, r.default_branch || null)}
                className={
                  on
                    ? "flex w-full items-center gap-2 border-b border-border bg-secondary px-3 py-2 text-start text-sm last:border-b-0"
                    : "flex w-full items-center gap-2 border-b border-border px-3 py-2 text-start text-sm last:border-b-0 hover:bg-secondary"
                }
              >
                <ProviderMark id="github" className="size-3.5 shrink-0 text-muted-foreground" />
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
      <FieldDescription>{t("github.app.fromAppHint")}</FieldDescription>
    </Field>
  )
}
