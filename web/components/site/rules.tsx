"use client"

import { Badge, Button, Checkbox, Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useState } from "react"
import { PlusIcon, XIcon } from "lucide-react"

import { useErrorText } from "@/components/form-error"
import { useLoadedSiteOptional } from "@/components/site/context"
import { ErrorBand, LoadingRows, SectionTitle, SettingsRow } from "@/components/states"





import { apiFetch } from "@/lib/api"
import { useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

type RedirectRule = { from: string; to: string; code?: number; preserve_query?: boolean }
type HeaderRule = { action: "set" | "add" | "remove"; name: string; value?: string }
type BasicAuthUser = { username: string }
type BasicAuthRule = { enabled: boolean; path_prefix?: string; exempt_paths?: string[]; users: BasicAuthUser[] }
type AccessRules = { ip_allow?: string[]; ip_deny?: string[]; block_bad_bots?: boolean }
type HTTPRules = {
  redirects: RedirectRule[]
  headers: HeaderRule[]
  basic_auth: BasicAuthRule | null
  access: AccessRules | null
  status: string
  error?: string
}

const REDIRECT_CODES = [301, 302, 307, 308]

const HEADER_PRESETS: { key: string; name: string; value: string }[] = [
  { key: "hsts", name: "Strict-Transport-Security", value: "max-age=63072000; includeSubDomains; preload" },
  { key: "nosniff", name: "X-Content-Type-Options", value: "nosniff" },
  { key: "frame", name: "X-Frame-Options", value: "DENY" },
  { key: "cors", name: "Access-Control-Allow-Origin", value: "*" },
]

function statusVariant(status: string): "success" | "neutral" | "outline" | "danger" {
  if (status === "error") return "danger"
  if (status === "pending") return "neutral"
  return "outline"
}

function emptyRules(): HTTPRules {
  return { redirects: [], headers: [], basic_auth: null, access: null, status: "ok" }
}

/**
 * Redirects, response headers and access rules (MH-578 E4-E6) rendered into this app's own Caddy
 * route by the agent. Rate limiting is not offered: the agent's plain Caddy build has no rate-limit
 * module, and this project does not rebuild Caddy to add one (see internal/deploy/httprules.go).
 *
 * Reused for proxy hosts (MH-578 C-rules): pass `basePath` to point at
 * `/api/teams/{team}/proxy-hosts/{proxy}/rules` instead of an app's own `{path}/rules`. Without it,
 * this falls back to the app rendered by `useLoadedSite()`.
 */
export function SiteRules({ basePath }: { basePath?: string } = {}) {
  const { t } = useI18n()
  const { manage } = useTeam()
  const site = useLoadedSiteOptional()
  const errorText = useErrorText()
  const base = basePath ?? `${site?.path}/rules`

  const [rules, setRules] = useState<HTTPRules | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const [redirectFrom, setRedirectFrom] = useState("")
  const [redirectTo, setRedirectTo] = useState("")
  const [redirectCode, setRedirectCode] = useState("302")

  const [headerAction, setHeaderAction] = useState<"set" | "add" | "remove">("set")
  const [headerName, setHeaderName] = useState("")
  const [headerValue, setHeaderValue] = useState("")

  const [authUser, setAuthUser] = useState("")
  const [authPass, setAuthPass] = useState("")
  const [authPrefix, setAuthPrefix] = useState("")

  const [allowInput, setAllowInput] = useState("")
  const [denyInput, setDenyInput] = useState("")

  const load = useCallback(() => {
    apiFetch<HTTPRules>(base)
      .then((r) => {
        setRules(r)
        setError(null)
      })
      .catch((err) => setError(errorText(err)))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [base])
  useEffect(load, [load])

  async function save(next: HTTPRules, okMsg: string) {
    setBusy(true)
    setError(null)
    setNotice(null)
    try {
      const body = {
        redirects: next.redirects,
        headers: next.headers,
        basic_auth: next.basic_auth,
        access: next.access,
      }
      const r = await apiFetch<HTTPRules>(base, { method: "PUT", body })
      setRules(r)
      setNotice(okMsg)
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  const current = rules ?? emptyRules()

  function addRedirect() {
    const from = redirectFrom.trim()
    const to = redirectTo.trim()
    if (!from || !to) return
    save(
      { ...current, redirects: [...current.redirects, { from, to, code: Number(redirectCode), preserve_query: false }] },
      t("site.rules.saved"),
    )
    setRedirectFrom("")
    setRedirectTo("")
  }

  function removeRedirect(i: number) {
    save({ ...current, redirects: current.redirects.filter((_, idx) => idx !== i) }, t("site.rules.saved"))
  }

  function addHeader(name?: string, value?: string, action?: "set" | "add" | "remove") {
    const n = (name ?? headerName).trim()
    if (!n) return
    save(
      { ...current, headers: [...current.headers, { action: action ?? headerAction, name: n, value: value ?? headerValue.trim() }] },
      t("site.rules.saved"),
    )
    setHeaderName("")
    setHeaderValue("")
  }

  function removeHeader(i: number) {
    save({ ...current, headers: current.headers.filter((_, idx) => idx !== i) }, t("site.rules.saved"))
  }

  function saveAuth(next: Partial<BasicAuthRule>) {
    const ba: BasicAuthRule = {
      enabled: next.enabled ?? current.basic_auth?.enabled ?? false,
      path_prefix: next.path_prefix ?? current.basic_auth?.path_prefix ?? "",
      exempt_paths: next.exempt_paths ?? current.basic_auth?.exempt_paths ?? [],
      users: next.users ?? current.basic_auth?.users ?? [],
    }
    save({ ...current, basic_auth: ba }, t("site.rules.saved"))
  }

  function addAuthUser() {
    const u = authUser.trim()
    const p = authPass
    if (!u || !p) return
    const users = [...(current.basic_auth?.users ?? []).filter((x) => x.username !== u), { username: u }]
    save(
      {
        ...current,
        basic_auth: {
          enabled: current.basic_auth?.enabled ?? true,
          path_prefix: authPrefix.trim(),
          exempt_paths: current.basic_auth?.exempt_paths ?? [],
          users,
        },
      },
      t("site.rules.saved"),
    )
    setAuthUser("")
    setAuthPass("")
  }

  function removeAuthUser(username: string) {
    if (!current.basic_auth) return
    saveAuth({ users: current.basic_auth.users.filter((u) => u.username !== username) })
  }

  function saveAccess(next: Partial<AccessRules>) {
    const access: AccessRules = {
      ip_allow: next.ip_allow ?? current.access?.ip_allow ?? [],
      ip_deny: next.ip_deny ?? current.access?.ip_deny ?? [],
      block_bad_bots: next.block_bad_bots ?? current.access?.block_bad_bots ?? false,
    }
    save({ ...current, access }, t("site.rules.saved"))
  }

  function addAllow() {
    const v = allowInput.trim()
    if (!v) return
    saveAccess({ ip_allow: [...(current.access?.ip_allow ?? []), v] })
    setAllowInput("")
  }
  function addDeny() {
    const v = denyInput.trim()
    if (!v) return
    saveAccess({ ip_deny: [...(current.access?.ip_deny ?? []), v] })
    setDenyInput("")
  }

  return (
    <div data-testid="rules">
      <section aria-labelledby="rules-redirects-h">
        <SectionTitle id="rules-redirects-h" title={t("site.rules.redirects.title")} hint={t("site.rules.redirects.hint")} />
        {rules === null ? (
          <LoadingRows rows={2} />
        ) : (
          <>
            <SettingsRow label={t("site.rules.redirects.list")}>
              <div className="flex flex-col gap-2">
                {current.redirects.map((r, i) => (
                  <Badge key={i} variant="outline" className="w-fit gap-2 py-1.5">
                    <span dir="ltr">
                      {r.from} → {r.to} ({r.code ?? 302})
                    </span>
                    {manage ? (
                      <button type="button" aria-label={t("site.rules.remove")} onClick={() => removeRedirect(i)} disabled={busy}>
                        <XIcon className="size-3" />
                      </button>
                    ) : null}
                  </Badge>
                ))}
                {current.redirects.length === 0 ? <p className="text-xs text-muted-foreground">{t("site.rules.redirects.empty")}</p> : null}
              </div>
              {manage ? (
                <div className="mt-2 flex max-w-xl flex-wrap items-center gap-2">
                  <Input dir="ltr" value={redirectFrom} placeholder={t("site.rules.redirects.fromPlaceholder")} onChange={(e) => setRedirectFrom(e.target.value)} className="w-40" />
                  <Input dir="ltr" value={redirectTo} placeholder={t("site.rules.redirects.toPlaceholder")} onChange={(e) => setRedirectTo(e.target.value)} className="w-56" />
                  <Select value={redirectCode} onValueChange={(v) => v && setRedirectCode(v)}>
                    <SelectTrigger className="w-24">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {REDIRECT_CODES.map((c) => (
                        <SelectItem key={c} value={String(c)}>
                          {c}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                  <Button type="button" variant="secondary" size="icon" disabled={busy || !redirectFrom.trim() || !redirectTo.trim()} onClick={addRedirect}>
                    <PlusIcon />
                  </Button>
                </div>
              ) : null}
            </SettingsRow>
          </>
        )}
      </section>

      {rules !== null ? (
        <section aria-labelledby="rules-headers-h">
          <SectionTitle id="rules-headers-h" title={t("site.rules.headers.title")} hint={t("site.rules.headers.hint")} />
          <SettingsRow label={t("site.rules.headers.list")}>
            <div className="flex flex-col gap-2">
              {current.headers.map((h, i) => (
                <Badge key={i} variant="outline" className="w-fit gap-2 py-1.5">
                  <span dir="ltr">
                    {h.action} {h.name}
                    {h.action !== "remove" ? `: ${h.value}` : ""}
                  </span>
                  {manage ? (
                    <button type="button" aria-label={t("site.rules.remove")} onClick={() => removeHeader(i)} disabled={busy}>
                      <XIcon className="size-3" />
                    </button>
                  ) : null}
                </Badge>
              ))}
              {current.headers.length === 0 ? <p className="text-xs text-muted-foreground">{t("site.rules.headers.empty")}</p> : null}
            </div>
            {manage ? (
              <>
                <div className="mt-2 flex flex-wrap gap-2">
                  {HEADER_PRESETS.map((p) => (
                    <Button key={p.key} type="button" variant="secondary" size="sm" disabled={busy} onClick={() => addHeader(p.name, p.value, "set")}>
                      {t(`site.rules.headers.presets.${p.key}`)}
                    </Button>
                  ))}
                </div>
                <div className="mt-2 flex max-w-xl flex-wrap items-center gap-2">
                  <Select value={headerAction} onValueChange={(v) => v && setHeaderAction(v as "set" | "add" | "remove")}>
                    <SelectTrigger className="w-28">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="set">{t("site.rules.headers.actions.set")}</SelectItem>
                      <SelectItem value="add">{t("site.rules.headers.actions.add")}</SelectItem>
                      <SelectItem value="remove">{t("site.rules.headers.actions.remove")}</SelectItem>
                    </SelectContent>
                  </Select>
                  <Input dir="ltr" value={headerName} placeholder={t("site.rules.headers.namePlaceholder")} onChange={(e) => setHeaderName(e.target.value)} className="w-56" />
                  {headerAction !== "remove" ? (
                    <Input dir="ltr" value={headerValue} placeholder={t("site.rules.headers.valuePlaceholder")} onChange={(e) => setHeaderValue(e.target.value)} className="w-56" />
                  ) : null}
                  <Button type="button" variant="secondary" size="icon" disabled={busy || !headerName.trim()} onClick={() => addHeader()}>
                    <PlusIcon />
                  </Button>
                </div>
              </>
            ) : null}
          </SettingsRow>
        </section>
      ) : null}

      {rules !== null ? (
        <section aria-labelledby="rules-access-h">
          <SectionTitle id="rules-access-h" title={t("site.rules.access.title")} hint={t("site.rules.access.hint")} />

          <SettingsRow label={t("site.rules.access.basicAuth")} htmlFor="rules-auth-enable" hint={t("site.rules.access.basicAuthHint")}>
            <div className="flex items-center gap-3">
              <Checkbox
                id="rules-auth-enable"
                checked={!!current.basic_auth?.enabled}
                disabled={!manage || busy}
                onCheckedChange={(v) => saveAuth({ enabled: !!v })}
              />
              <Badge variant={statusVariant(current.status)}>{t(`site.rules.statuses.${current.status}`, undefined, current.status)}</Badge>
            </div>
            {current.error ? <p className="mt-1 text-xs text-nq-danger-text">{current.error}</p> : null}
            {current.basic_auth?.enabled ? (
              <>
                <div className="mt-2 flex flex-col gap-2">
                  {(current.basic_auth.users ?? []).map((u) => (
                    <Badge key={u.username} variant="outline" className="w-fit gap-2 py-1.5">
                      <span dir="ltr">{u.username}</span>
                      {manage ? (
                        <button type="button" aria-label={t("site.rules.remove")} onClick={() => removeAuthUser(u.username)} disabled={busy}>
                          <XIcon className="size-3" />
                        </button>
                      ) : null}
                    </Badge>
                  ))}
                </div>
                {manage ? (
                  <div className="mt-2 flex max-w-xl flex-wrap items-center gap-2">
                    <Input dir="ltr" value={authUser} placeholder={t("site.rules.access.usernamePlaceholder")} onChange={(e) => setAuthUser(e.target.value)} className="w-40" />
                    <Input dir="ltr" type="password" value={authPass} placeholder={t("site.rules.access.passwordPlaceholder")} onChange={(e) => setAuthPass(e.target.value)} className="w-40" />
                    <Input dir="ltr" value={authPrefix} placeholder={t("site.rules.access.pathPrefixPlaceholder")} onChange={(e) => setAuthPrefix(e.target.value)} className="w-40" />
                    <Button type="button" variant="secondary" size="icon" disabled={busy || !authUser.trim() || !authPass} onClick={addAuthUser}>
                      <PlusIcon />
                    </Button>
                  </div>
                ) : null}
              </>
            ) : null}
          </SettingsRow>

          <SettingsRow label={t("site.rules.access.ipAllow")} hint={t("site.rules.access.ipAllowHint")}>
            <div className="flex flex-wrap gap-2">
              {(current.access?.ip_allow ?? []).map((c) => (
                <Badge key={c} variant="outline" className="gap-1">
                  <span dir="ltr">{c}</span>
                  {manage ? (
                    <button type="button" aria-label={t("site.rules.remove")} onClick={() => saveAccess({ ip_allow: (current.access?.ip_allow ?? []).filter((x) => x !== c) })} disabled={busy}>
                      <XIcon className="size-3" />
                    </button>
                  ) : null}
                </Badge>
              ))}
            </div>
            {manage ? (
              <div className="mt-2 flex max-w-sm gap-2">
                <Input dir="ltr" value={allowInput} placeholder={t("site.rules.access.cidrPlaceholder")} onChange={(e) => setAllowInput(e.target.value)} onKeyDown={(e) => e.key === "Enter" && addAllow()} />
                <Button type="button" variant="secondary" size="icon" disabled={busy || !allowInput.trim()} onClick={addAllow}>
                  <PlusIcon />
                </Button>
              </div>
            ) : null}
          </SettingsRow>

          <SettingsRow label={t("site.rules.access.ipDeny")} hint={t("site.rules.access.ipDenyHint")}>
            <div className="flex flex-wrap gap-2">
              {(current.access?.ip_deny ?? []).map((c) => (
                <Badge key={c} variant="outline" className="gap-1">
                  <span dir="ltr">{c}</span>
                  {manage ? (
                    <button type="button" aria-label={t("site.rules.remove")} onClick={() => saveAccess({ ip_deny: (current.access?.ip_deny ?? []).filter((x) => x !== c) })} disabled={busy}>
                      <XIcon className="size-3" />
                    </button>
                  ) : null}
                </Badge>
              ))}
            </div>
            {manage ? (
              <div className="mt-2 flex max-w-sm gap-2">
                <Input dir="ltr" value={denyInput} placeholder={t("site.rules.access.cidrPlaceholder")} onChange={(e) => setDenyInput(e.target.value)} onKeyDown={(e) => e.key === "Enter" && addDeny()} />
                <Button type="button" variant="secondary" size="icon" disabled={busy || !denyInput.trim()} onClick={addDeny}>
                  <PlusIcon />
                </Button>
              </div>
            ) : null}
          </SettingsRow>

          <SettingsRow label={t("site.rules.access.blockBadBots")} htmlFor="rules-bad-bots" hint={t("site.rules.access.blockBadBotsHint")}>
            <Checkbox
              id="rules-bad-bots"
              checked={!!current.access?.block_bad_bots}
              disabled={!manage || busy}
              onCheckedChange={(v) => saveAccess({ block_bad_bots: !!v })}
            />
          </SettingsRow>

          <p className="border-b border-border px-6 py-3 text-xs text-muted-foreground">{t("site.rules.access.rateLimitNote")}</p>
        </section>
      ) : null}

      {notice ? <p className="border-b border-border px-6 py-2 text-xs text-nq-brand">{notice}</p> : null}
      <ErrorBand message={error} />
    </div>
  )
}
