"use client"

import { useCallback, useEffect, useState } from "react"
import { usePathname, useRouter, useSearchParams } from "next/navigation"
import { KeyRoundIcon } from "lucide-react"
import { toast } from "sonner"

import { ProviderMark, oauthHref, useOAuthErrorText } from "@/components/auth/oauth"
import { ConfirmButton } from "@/components/confirm-button"
import { useErrorText } from "@/components/form-error"
import { ErrorBand, LoadingRows, Notice, SectionTitle } from "@/components/states"
import { buttonVariants } from "@/components/ui/button"
import { ApiError, apiFetch } from "@/lib/api"
import { useFormatDate, useI18n } from "@/lib/i18n"
import { cn } from "@/lib/utils"

type Identity = { id: string; provider: string; email: string; created_at: string; last_used_at: string | null }
type Identities = { identities: Identity[]; has_password: boolean; providers: string[] }

const NAMES: Record<string, string> = { google: "Google", github: "GitHub" }

/**
 * Connected accounts: the Google / GitHub identities that sign into this account. Connecting is a
 * round trip through the provider (…/oauth/{provider}?link=1), which comes back here with
 * ?linked=<provider> or ?oauth_error=<code>. The last way into an account cannot be disconnected.
 */
export function ConnectedAccounts() {
  const { t, locale } = useI18n()
  const fmt = useFormatDate()
  const errorText = useErrorText()
  const oauthErrorText = useOAuthErrorText()
  const params = useSearchParams()
  const router = useRouter()
  const pathname = usePathname()
  const [data, setData] = useState<Identities | null>(null)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      setData(await apiFetch<Identities>("/api/me/identities"))
    } catch (err) {
      setError(errorText(err))
      setData({ identities: [], has_password: true, providers: [] })
    }
  }, [errorText])

  useEffect(() => {
    void load()
  }, [load])

  // The result of a round trip (or of a GitHub App setup that could not tell which team it was for).
  useEffect(() => {
    const linked = params.get("linked")
    const oauthError = params.get("oauth_error")
    const githubError = params.get("github_error")
    if (!linked && !oauthError && !githubError) return
    if (linked) toast.success(t("account.identities.linkedToast", { provider: NAMES[linked] ?? linked }))
    if (oauthError) setError(oauthErrorText(oauthError))
    if (githubError) setError(t("account.identities.githubSetupFailed"))
    router.replace(pathname)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  async function unlink(ident: Identity) {
    try {
      await apiFetch(`/api/me/identities/${encodeURIComponent(ident.id)}`, { method: "DELETE" })
    } catch (err) {
      if (err instanceof ApiError && err.reason === "last_sign_in_method") throw new Error(t("account.identities.lastMethod"))
      throw new Error(errorText(err))
    }
    toast.success(t("account.identities.unlinkedToast", { provider: NAMES[ident.provider] ?? ident.provider }))
    await load()
  }

  const linked = new Set(data?.identities.map((i) => i.provider))
  const connectable = (data?.providers ?? []).filter((p) => !linked.has(p))

  return (
    <section aria-labelledby="connected-accounts">
      <SectionTitle id="connected-accounts" title={t("account.identities.title")} hint={t("account.identities.hint")} className="mt-6 border-t" />
      <ErrorBand message={error} />
      {data === null ? (
        <LoadingRows rows={2} />
      ) : (
        <>
          {!data.has_password ? (
            <Notice icon={<KeyRoundIcon />} data-testid="identities-no-password">
              {t("account.identities.noPassword")}
            </Notice>
          ) : null}
          {data.identities.length === 0 ? (
            <p className="border-b border-line px-6 py-4 text-sm text-grid-muted">
              {data.providers.length ? t("account.identities.none") : t("account.identities.noProviders")}
            </p>
          ) : (
            <ul className="divide-y divide-line border-b border-line">
              {data.identities.map((ident) => {
                const name = NAMES[ident.provider] ?? ident.provider
                return (
                  <li key={ident.id} className="flex flex-wrap items-center gap-3 px-6 py-4">
                    <span className="flex size-8 shrink-0 items-center justify-center border border-line bg-background text-grid-fg">
                      <ProviderMark id={ident.provider} />
                    </span>
                    <div className="min-w-0 flex-1">
                      <p className="text-sm font-medium text-grid-fg">
                        {name}{" "}
                        <span dir="ltr" className="font-normal text-grid-muted">
                          {ident.email}
                        </span>
                      </p>
                      <p className="text-xs text-grid-muted">
                        {t("account.identities.since", { date: fmt(ident.created_at) })}
                        {ident.last_used_at ? ` · ${t("account.identities.lastUsed", { date: fmt(ident.last_used_at) })}` : ""}
                      </p>
                    </div>
                    <ConfirmButton
                      label={t("account.identities.disconnect")}
                      title={t("account.identities.disconnectTitle", { provider: name })}
                      description={t("account.identities.disconnectBody", { provider: name })}
                      confirmLabel={t("account.identities.disconnect")}
                      onConfirm={() => unlink(ident)}
                    />
                  </li>
                )
              })}
            </ul>
          )}
          {connectable.length ? (
            <div className="flex flex-wrap gap-2 border-b border-line px-6 py-4">
              {connectable.map((p) => (
                <a key={p} href={oauthHref(p, { locale, link: true })} className={cn(buttonVariants({ variant: "outline" }), "gap-2")}>
                  <ProviderMark id={p} />
                  {t("account.identities.connect", { provider: NAMES[p] ?? p })}
                </a>
              ))}
            </div>
          ) : null}
        </>
      )}
    </section>
  )
}
