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
type OAuthTeam = { id: string; slug: string; name: string; role: string; servers: number; apps: number; can_connect: boolean; default: boolean }
type Consent = {
  client: { name: string }
  apps: OAuthApp[]
  kind: "integration" | "mcp"
  teams: OAuthTeam[]
  scopes: string[]
  default_scopes: string[]
}

/**
 * The OAuth consent page (MH-1153): a registered client (Mahaam) asks to connect Hosbah apps.
 * The person signs in if they have not, ticks the apps they manage (MH-1194: several, all of one
 * team, e.g. a project's web, api and mcp), and is sent back to the client with a one-time code
 * (or an error). An unknown client or redirect URI is shown here and
 * never redirected to.
 *
 * An MCP connector (claude.ai, ChatGPT, Claude Code…, kind "mcp", self-registered) is granted ONE
 * team instead of apps: the person picks it (pre-selected: the team with the most servers and apps)
 * and the permissions. Nothing here creates a team, except the explicit form shown to a person who
 * has none.
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
        const c = await apiFetch<Consent>(
          `/api/oauth/authorize?${new URLSearchParams({ client_id: clientID, redirect_uri: redirectURI, scope: params?.get("scope") ?? "", resource: params?.get("resource") ?? "" })}`,
        )
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
  if (consent.kind === "mcp") return <ConnectorConsent consent={consent} />

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

/** The consent for an MCP connector: one team, and the permissions. */
function ConnectorConsent({ consent }: { consent: Consent }) {
  const { t } = useI18n()
  const params = useSearchParams()
  const errorText = useErrorText()
  const [teams, setTeams] = useState(consent.teams)
  const [team, setTeam] = useState(() => consent.teams.find((x) => x.default)?.id ?? "")
  const [granted, setGranted] = useState<string[]>(consent.default_scopes)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [teamName, setTeamName] = useState("")
  const name = consent.client.name

  async function answer(allow: boolean) {
    setError(null)
    setBusy(true)
    try {
      const { redirect } = await apiFetch<{ redirect: string }>("/api/oauth/authorize", {
        method: "POST",
        body: {
          response_type: params?.get("response_type") ?? "",
          client_id: params?.get("client_id") ?? "",
          redirect_uri: params?.get("redirect_uri") ?? "",
          state: params?.get("state") ?? "",
          code_challenge: params?.get("code_challenge") ?? "",
          code_challenge_method: params?.get("code_challenge_method") ?? "",
          scope: params?.get("scope") ?? "",
          resource: params?.get("resource") ?? "",
          team: allow ? team : "",
          scopes: allow ? granted : [],
          allow,
        },
      })
      window.location.assign(redirect)
    } catch (e) {
      setError(errorText(e))
      setBusy(false)
    }
  }

  // Only for a person with no team at all, and only when they ask: never created behind their back.
  async function createTeam() {
    setError(null)
    setBusy(true)
    try {
      const created = await apiFetch<{ id: string; slug: string; name: string }>("/api/teams", { method: "POST", body: { name: teamName.trim() } })
      const fresh: OAuthTeam = { id: created.id, slug: created.slug, name: created.name, role: "owner", servers: 0, apps: 0, can_connect: true, default: true }
      setTeams([fresh])
      setTeam(fresh.id)
    } catch (e) {
      setError(errorText(e))
    }
    setBusy(false)
  }

  const toggle = (s: string, on: boolean) => setGranted((cur) => (on ? (cur.includes(s) ? cur : [...cur, s]) : cur.filter((v) => v !== s)))
  const picked = teams.find((x) => x.id === team)
  return (
    <AuthFrame title={t("oauth.mcp.heading", { client: name })} description={t("oauth.mcp.description", { client: name })}>
      <div className="flex flex-col gap-4">
        {teams.length === 0 ? (
          <div className="flex flex-col gap-2" data-testid="oauth-no-team">
            <p className="text-sm text-muted-foreground">{t("oauth.mcp.noTeams")}</p>
            <Field>
              <Input value={teamName} onChange={(e) => setTeamName(e.target.value)} placeholder={t("onboarding.teamName")} aria-label={t("onboarding.teamName")} maxLength={80} />
            </Field>
            <Button variant="secondary" disabled={busy || teamName.trim() === ""} onClick={() => void createTeam()}>
              {t("oauth.mcp.createTeam")}
            </Button>
          </div>
        ) : (
          <fieldset className="flex flex-col gap-2" data-testid="oauth-teams">
            <legend className="mb-1 text-label">{t("oauth.mcp.pickTeam")}</legend>
            <p className="mb-1 text-caption text-muted-foreground">{t("oauth.mcp.pickTeamHint")}</p>
            <div className="flex max-h-[22rem] flex-col gap-2 overflow-y-auto">
              {teams.map((x) => (
                <label
                  key={x.id}
                  htmlFor={`team-${x.id}`}
                  title={x.can_connect ? undefined : t("oauth.mcp.cannotConnect")}
                  className={`flex items-center gap-3 rounded-control border bg-card p-3 ${team === x.id ? "border-primary" : "border-border"} ${x.can_connect ? "cursor-pointer" : "cursor-not-allowed opacity-60"}`}
                >
                  <input
                    type="radio"
                    id={`team-${x.id}`}
                    name="team"
                    value={x.id}
                    checked={team === x.id}
                    disabled={!x.can_connect}
                    onChange={() => setTeam(x.id)}
                    className="size-4 accent-[var(--primary)]"
                  />
                  <div className="flex min-w-0 flex-1 flex-col gap-0.5">
                    <div className="flex items-center gap-2">
                      <span dir="auto" className="truncate text-label">
                        {x.name}
                      </span>
                      {x.default ? <span className="text-caption text-muted-foreground">{t("oauth.mcp.suggested")}</span> : null}
                    </div>
                    <FieldDescription>
                      {t("oauth.mcp.footprint", { servers: x.servers, apps: x.apps })} · {t(`roles.${x.role}`, undefined, x.role)}
                      {x.can_connect ? null : <> · {t("oauth.mcp.cannotConnect")}</>}
                    </FieldDescription>
                  </div>
                </label>
              ))}
            </div>
          </fieldset>
        )}
        <fieldset className="flex flex-col gap-2" data-testid="oauth-scopes">
          <legend className="mb-1 text-label">{t("oauth.mcp.permissions")}</legend>
          {consent.scopes.map((s) => (
            <label key={s} htmlFor={`scope-${s}`} className="flex cursor-pointer items-center gap-3">
              <Checkbox id={`scope-${s}`} checked={granted.includes(s)} onCheckedChange={(v) => toggle(s, v === true)} />
              <span className="font-mono text-sm">{s}</span>
              <span className="text-caption text-muted-foreground">{t(`tokens.scopeHelp.${s.replace(":", "_")}`)}</span>
            </label>
          ))}
        </fieldset>
        <FormError message={error} />
        <div className="flex flex-wrap gap-2">
          <Button variant="primary" disabled={busy || !picked?.can_connect || granted.length === 0} onClick={() => void answer(true)}>
            {picked ? t("oauth.mcp.allowTeam", { team: picked.name }) : t("oauth.allow")}
          </Button>
          <Button variant="secondary" disabled={busy} onClick={() => void answer(false)}>
            {t("oauth.deny")}
          </Button>
        </div>
        <p className="text-caption text-muted-foreground">{t("oauth.mcp.revokeHint")}</p>
      </div>
    </AuthFrame>
  )
}
