"use client"

import { Button, Checkbox, Field, FieldDescription, Input } from "@fadymondy/nasaq/web"
import { usePathname, useRouter, useSearchParams } from "next/navigation"
import { Suspense, useEffect, useState } from "react"

import { AuthFrame } from "@/components/auth-frame"
import { FormError, useErrorText } from "@/components/form-error"
import { PageLoading } from "@/components/page-loading"
import { ServerMark } from "@/components/server-mark"
import { apiFetch, ApiError } from "@/lib/api"
import { useDocumentTitle, useI18n } from "@/lib/i18n"

type OAuthApp = { id: string; name: string; title: string; status: string; server_name: string; team_slug: string; team_name: string; logo_url: string | null }
type Consent = { client: { name: string }; apps: OAuthApp[] }

/**
 * The OAuth consent page (MH-1153): a registered client (Mahaam) asks to connect Hosbah apps.
 * The person signs in if they have not, ticks the apps they manage (MH-1194: several, all of one
 * team, e.g. a project's web, api and mcp), and is sent back to the client with a one-time code
 * (or an error). An unknown client or redirect URI is shown here and
 * never redirected to.
 */
export default function OAuthAuthorizePage() {
  return (
    <Suspense fallback={<PageLoading />}>
      <Authorize />
    </Suspense>
  )
}

function Authorize() {
  const { t, locale } = useI18n()
  const router = useRouter()
  const pathname = usePathname() ?? ""
  const params = useSearchParams()
  const errorText = useErrorText()
  const [consent, setConsent] = useState<Consent | null>(null)
  const [fatal, setFatal] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  // The picked apps, "team_slug/app_id" each; all of one team.
  const [picks, setPicks] = useState<string[]>([])
  const [busy, setBusy] = useState(false)
  const [q, setQ] = useState("")
  useDocumentTitle(t("oauth.title"))

  const clientID = params?.get("client_id") ?? ""
  const redirectURI = params?.get("redirect_uri") ?? ""

  useEffect(() => {
    let live = true
    ;(async () => {
      try {
        await apiFetch("/api/me")
      } catch (e) {
        if (e instanceof ApiError && e.status === 401) {
          const here = `${pathname}?${params?.toString() ?? ""}`
          router.replace(`/${locale}/login?next=${encodeURIComponent(here)}`)
          return
        }
      }
      try {
        const c = await apiFetch<Consent>(`/api/oauth/authorize?${new URLSearchParams({ client_id: clientID, redirect_uri: redirectURI })}`)
        if (!live) return
        setConsent(c)
        if (c.apps.length === 1) setPicks([`${c.apps[0].team_slug}/${c.apps[0].id}`])
      } catch (e) {
        if (live) setFatal(errorText(e))
      }
    })()
    return () => {
      live = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [clientID, redirectURI])

  async function answer(allow: boolean) {
    setError(null)
    setBusy(true)
    const team = picks[0]?.split("/")[0] ?? ""
    const apps = picks.map((p) => p.split("/")[1])
    try {
      const { redirect } = await apiFetch<{ redirect: string }>("/api/oauth/authorize", {
        method: "POST",
        body: {
          response_type: params?.get("response_type") ?? "",
          client_id: clientID,
          redirect_uri: redirectURI,
          state: params?.get("state") ?? "",
          code_challenge: params?.get("code_challenge") ?? "",
          code_challenge_method: params?.get("code_challenge_method") ?? "",
          scope: params?.get("scope") ?? "",
          team: allow ? team : "",
          // One app goes as `app`, which every Hosbah API version takes; `apps` only when several.
          ...(allow && apps.length > 1 ? { apps } : { app: allow ? (apps[0] ?? "") : "" }),
          allow,
        },
      })
      window.location.assign(redirect)
    } catch (e) {
      setError(errorText(e))
      setBusy(false)
    }
  }

  if (fatal) {
    return (
      <AuthFrame title={t("oauth.invalidTitle")} description={t("oauth.invalidHint")}>
        <FormError message={fatal} />
      </AuthFrame>
    )
  }
  if (!consent) return <PageLoading />

  const name = consent.client.name
  const pickedTeam = picks[0]?.split("/")[0]
  const toggle = (value: string, on: boolean) =>
    setPicks((cur) => (on ? (cur.includes(value) ? cur : [...cur, value]) : cur.filter((v) => v !== value)))
  // The search matches title, name, server and team; a picked app always stays in the list.
  const needle = q.trim().toLowerCase()
  const shown = consent.apps.filter(
    (a) =>
      !needle ||
      picks.includes(`${a.team_slug}/${a.id}`) ||
      `${a.title} ${a.name} ${a.server_name} ${a.team_name} ${a.team_slug}`.toLowerCase().includes(needle),
  )
  return (
    <AuthFrame title={t("oauth.heading", { client: name })} description={t("oauth.description", { client: name })}>
      <div className="flex flex-col gap-4">
        {consent.apps.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t("oauth.noApps")}</p>
        ) : (
          <fieldset className="flex flex-col gap-2">
            <legend className="mb-1 text-label">{t("oauth.pick")}</legend>
            <p className="mb-1 text-caption text-muted-foreground">{t("oauth.pickHint")}</p>
            {consent.apps.length > 5 ? (
              <Input
                type="search"
                value={q}
                onChange={(e) => setQ(e.target.value)}
                placeholder={t("oauth.search")}
                aria-label={t("oauth.search")}
                autoFocus
              />
            ) : null}
            <div className="flex max-h-[26rem] flex-col gap-2 overflow-y-auto">
              {shown.length === 0 ? <p className="py-2 text-sm text-muted-foreground">{t("oauth.noMatch")}</p> : null}
              {shown.map((a) => {
                const value = `${a.team_slug}/${a.id}`
                const otherTeam = !!pickedTeam && pickedTeam !== a.team_slug
                return (
                  <label
                    key={value}
                    htmlFor={`app-${a.id}`}
                    title={otherTeam ? t("oauth.otherTeam") : undefined}
                    className="block cursor-pointer rounded-control border border-border bg-card p-3 has-[[data-checked]]:border-primary has-[[data-disabled]]:cursor-not-allowed has-[[data-disabled]]:opacity-60"
                  >
                    <Field className="flex-row items-center">
                      <Checkbox
                        id={`app-${a.id}`}
                        checked={picks.includes(value)}
                        disabled={otherTeam}
                        onCheckedChange={(v) => toggle(value, v === true)}
                      />
                      <ServerMark server={{ logo_url: a.logo_url }} size={6} />
                      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
                        <div dir="auto" className="truncate text-label">
                          {a.title}
                        </div>
                        <FieldDescription>
                          <span dir="auto">{a.team_name}</span> · <span dir="auto">{a.server_name}</span>
                        </FieldDescription>
                      </div>
                    </Field>
                  </label>
                )
              })}
            </div>
          </fieldset>
        )}
        <FormError message={error} />
        <div className="flex flex-wrap gap-2">
          <Button variant="primary" disabled={busy || picks.length === 0} onClick={() => void answer(true)}>
            {t("oauth.allow")}
          </Button>
          <Button variant="secondary" disabled={busy} onClick={() => void answer(false)}>
            {t("oauth.deny")}
          </Button>
          {picks.length > 1 ? <span className="self-center text-caption text-muted-foreground">{t("oauth.picked", { n: picks.length })}</span> : null}
        </div>
        <p className="text-caption text-muted-foreground">{t("oauth.revokeHint")}</p>
      </div>
    </AuthFrame>
  )
}
