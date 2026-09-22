"use client"

import { useCallback, useEffect, useState } from "react"
import { usePathname, useRouter, useSearchParams } from "next/navigation"
import { ExternalLinkIcon, InfoIcon } from "lucide-react"
import { toast } from "sonner"

import { ProviderMark } from "@/components/auth/oauth"
import { useErrorText } from "@/components/form-error"
import { ErrorBand, LoadingRows, Notice, SectionTitle } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { buttonVariants } from "@/components/ui/button"
import { apiFetch } from "@/lib/api"
import { useI18n } from "@/lib/i18n"
import { cn } from "@/lib/utils"

type Installation = {
  id: string
  installation_id: number
  account_login: string
  account_type: "User" | "Organization" | "Enterprise"
  repository_selection: "all" | "selected"
  suspended_at: string | null
  manage_url: string
}
type Status = { configured: boolean; slug: string; can_manage: boolean; installations: Installation[] }

/**
 * The team's GitHub App: the GitHub accounts it is installed on, and (owner or admin) the install
 * link. Installing is a round trip through github.com that comes back to the API's setup URL, which
 * sends the browser here with ?github=connected|requested or ?github_error=<code>.
 */
export function GitHubAppSection({ team }: { team: string }) {
  const { t, locale } = useI18n()
  const errorText = useErrorText()
  const params = useSearchParams()
  const router = useRouter()
  const pathname = usePathname()
  const [status, setStatus] = useState<Status | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      setStatus(await apiFetch<Status>(`/api/teams/${encodeURIComponent(team)}/github/app`))
    } catch (err) {
      setError(errorText(err))
    }
  }, [team, errorText])

  useEffect(() => {
    void load()
  }, [load])

  useEffect(() => {
    const ok = params.get("github")
    const bad = params.get("github_error")
    if (!ok && !bad) return
    if (ok === "connected") toast.success(t("github.app.connectedToast"))
    if (ok === "requested") setNotice(t("github.app.requested"))
    if (bad) setError(t(`github.app.errors.${bad}`, undefined, t("github.app.errors.internal")))
    router.replace(pathname)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const installHref = `/api/teams/${encodeURIComponent(team)}/github/install?locale=${locale}`

  return (
    <section aria-labelledby="github-app">
      <SectionTitle
        id="github-app"
        title={t("github.app.title")}
        hint={t("github.app.hint")}
        className="mt-6 border-t"
        action={
          status?.configured && status.can_manage ? (
            <a href={installHref} className={cn(buttonVariants({ variant: status.installations.length ? "outline" : "default" }), "gap-2")}>
              <ProviderMark id="github" />
              {status.installations.length ? t("github.app.addAccount") : t("github.app.install")}
            </a>
          ) : undefined
        }
      />
      <ErrorBand message={error} />
      {notice ? <Notice icon={<InfoIcon />}>{notice}</Notice> : null}
      {status === null ? (
        error ? null : <LoadingRows rows={1} />
      ) : !status.configured ? (
        <p className="border-b border-line px-6 py-4 text-sm text-grid-muted">{t("github.app.notConfigured")}</p>
      ) : status.installations.length === 0 ? (
        <p className="border-b border-line px-6 py-4 text-sm text-grid-muted">
          {t("github.app.none")} {status.can_manage ? null : t("github.app.askAdmin")}
        </p>
      ) : (
        <ul className="divide-y divide-line border-b border-line" data-testid="github-installations">
          {status.installations.map((i) => (
            <li key={i.id} className="flex flex-wrap items-center gap-3 px-6 py-4">
              <span className="flex size-8 shrink-0 items-center justify-center border border-line bg-background text-grid-fg">
                <ProviderMark id="github" />
              </span>
              <div className="min-w-0 flex-1">
                <p className="flex flex-wrap items-center gap-2 text-sm font-medium text-grid-fg">
                  <span dir="ltr">{i.account_login}</span>
                  <Badge variant="outline">{t(`github.app.types.${i.account_type}`, undefined, i.account_type)}</Badge>
                  {i.suspended_at ? <Badge variant="destructive">{t("github.app.suspended")}</Badge> : null}
                </p>
                <p className="text-xs text-grid-muted">
                  {i.repository_selection === "all" ? t("github.app.allRepos") : t("github.app.selectedRepos")}
                </p>
              </div>
              {status.can_manage ? (
                <a
                  href={i.manage_url}
                  target="_blank"
                  rel="noopener noreferrer"
                  className={cn(buttonVariants({ variant: "ghost", size: "sm" }), "gap-1")}
                >
                  {t("github.app.manage")}
                  <ExternalLinkIcon className="rtl:-scale-x-100" />
                </a>
              ) : null}
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
