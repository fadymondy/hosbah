"use client"

import { buttonVariants } from "@fadymondy/nasaq/web"
import { useEffect, useState } from "react"


import { apiFetch } from "@/lib/api"
import { useI18n } from "@/lib/i18n"
import { cn } from "@/lib/utils"

/*
Sign in with Google and GitHub. The buttons are plain links: the API's /api/auth/oauth/{provider}
answers with a redirect to the provider (state, PKCE, nonce) and the provider's callback comes back
to the API, which sends the browser on into the web app. Only the providers the platform has set up
are shown (GET /api/auth/providers).
*/

export type OAuthProviderId = "google" | "github"
export type OAuthProvider = { id: OAuthProviderId; name: string }

/** The providers this platform offers; null while loading, [] when none (or unreachable). */
export function useOAuthProviders(): OAuthProvider[] | null {
  const [providers, setProviders] = useState<OAuthProvider[] | null>(null)
  useEffect(() => {
    let live = true
    apiFetch<{ providers: OAuthProvider[] }>("/api/auth/providers")
      .then((r) => live && setProviders(r.providers ?? []))
      .catch(() => live && setProviders([]))
    return () => {
      live = false
    }
  }, [])
  return providers
}

/** The provider's own mark, drawn inline (no external image). GitHub's follows the text colour. */
export function ProviderMark({ id, className }: { id: string; className?: string }) {
  if (id === "google") {
    return (
      <svg viewBox="0 0 48 48" aria-hidden="true" className={cn("size-4", className)}>
        <path fill="#EA4335" d="M24 9.5c3.54 0 6.71 1.22 9.21 3.6l6.85-6.85C35.9 2.38 30.47 0 24 0 14.62 0 6.51 5.38 2.56 13.22l7.98 6.19C12.43 13.72 17.74 9.5 24 9.5z" />
        <path fill="#4285F4" d="M46.98 24.55c0-1.57-.15-3.09-.38-4.55H24v9.02h12.94c-.58 2.96-2.26 5.48-4.78 7.18l7.73 6c4.51-4.18 7.09-10.36 7.09-17.65z" />
        <path fill="#FBBC05" d="M10.53 28.59c-.48-1.45-.76-2.99-.76-4.59s.27-3.14.76-4.59l-7.98-6.19C.92 16.46 0 20.12 0 24c0 3.88.92 7.54 2.56 10.78l7.97-6.19z" />
        <path fill="#34A853" d="M24 48c6.48 0 11.93-2.13 15.89-5.81l-7.73-6c-2.15 1.45-4.92 2.3-8.16 2.3-6.26 0-11.57-4.22-13.47-9.91l-7.98 6.19C6.51 42.62 14.62 48 24 48z" />
      </svg>
    )
  }
  if (id === "github") {
    return (
      <svg viewBox="0 0 16 16" aria-hidden="true" fill="currentColor" className={cn("size-4", className)}>
        <path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0 0 16 8c0-4.42-3.58-8-8-8z" />
      </svg>
    )
  }
  return null
}

/** Where a provider button (or a "connect" link) points. */
export function oauthHref(
  provider: string,
  opts: { locale: string; next?: string | null; invitation?: string; link?: boolean },
): string {
  const q = new URLSearchParams({ locale: opts.locale })
  if (opts.link) q.set("link", "1")
  if (opts.next) q.set("next", opts.next)
  if (opts.invitation) q.set("invitation", opts.invitation)
  return `/api/auth/oauth/${encodeURIComponent(provider)}?${q}`
}

/** The message for an ?oauth_error= code the API sent the browser back with. */
export function useOAuthErrorText() {
  const { t } = useI18n()
  return (code: string | null | undefined): string | null =>
    code ? t(`auth.oauth.errors.${code}`, undefined, t("auth.oauth.generic")) : null
}

/**
 * "Continue with Google / GitHub", then an "or" rule above the email form. Renders nothing when the
 * platform offers no provider.
 */
export function OAuthButtons({ next, invitation }: { next?: string | null; invitation?: string }) {
  const { t, locale } = useI18n()
  const providers = useOAuthProviders()
  if (!providers?.length) return null
  return (
    <div className="flex flex-col gap-3" data-testid="oauth-buttons">
      {providers.map((p) => (
        <a
          key={p.id}
          href={oauthHref(p.id, { locale, next, invitation })}
          className={cn(buttonVariants({ variant: "secondary", size: "lg" }), "w-full gap-2")}
        >
          <ProviderMark id={p.id} />
          <span>{t("auth.oauth.continueWith", { provider: p.name })}</span>
        </a>
      ))}
      <div className="flex items-center gap-3 text-xs text-muted-foreground" role="separator">
        <span className="h-px flex-1 bg-border" />
        {t("auth.oauth.or")}
        <span className="h-px flex-1 bg-border" />
      </div>
    </div>
  )
}
