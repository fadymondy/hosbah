"use client"

import { useCallback, useEffect, useState } from "react"
import { PlusIcon, XIcon, ZapIcon } from "lucide-react"

import { useErrorText } from "@/components/form-error"
import { useLoadedSite } from "@/components/site/context"
import { ErrorBand, LoadingRows, SectionTitle, SettingsRow } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { apiFetch } from "@/lib/api"
import { useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

type CacheSettings = {
  enabled: boolean
  ttl_seconds: number
  bypass_paths: string[]
  bypass_cookies: string[]
  status: string
  error?: string
  hits: number
  misses: number
  last_purged_at?: string
}

const TTL_PRESETS = [30, 60, 120, 300, 600, 1800, 3600, 86400]

function statusVariant(status: string): "default" | "secondary" | "outline" | "destructive" {
  if (status === "error") return "destructive"
  if (status === "running") return "default"
  if (status === "pending") return "secondary"
  return "outline"
}

/**
 * Edge cache (MH-576 D8): a per-app toggle for an agent-managed Varnish container in front of the
 * app's own upstream, on its own server — TTL, bypass paths/cookies, and a purge action. Off by
 * default; turning it on applies at once.
 */
export function SiteCache() {
  const { t } = useI18n()
  const { manage } = useTeam()
  const { path } = useLoadedSite()
  const errorText = useErrorText()
  const base = `${path}/cache`

  const [settings, setSettings] = useState<CacheSettings | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [purging, setPurging] = useState(false)
  const [pathInput, setPathInput] = useState("")
  const [cookieInput, setCookieInput] = useState("")

  const load = useCallback(() => {
    apiFetch<CacheSettings>(base)
      .then((s) => {
        setSettings(s)
        setError(null)
      })
      .catch((err) => setError(errorText(err)))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [base])
  useEffect(load, [load])

  async function save(next: Partial<CacheSettings>) {
    if (!settings) return
    setBusy(true)
    setError(null)
    setNotice(null)
    try {
      const body = {
        enabled: next.enabled ?? settings.enabled,
        ttl_seconds: next.ttl_seconds ?? settings.ttl_seconds ?? 60,
        bypass_paths: next.bypass_paths ?? settings.bypass_paths,
        bypass_cookies: next.bypass_cookies ?? settings.bypass_cookies,
      }
      const s = await apiFetch<CacheSettings>(base, { method: "PUT", body })
      setSettings(s)
      setNotice(t("site.cache.saved"))
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  async function purge() {
    setPurging(true)
    setError(null)
    setNotice(null)
    try {
      const s = await apiFetch<CacheSettings>(`${base}/purge`, { method: "POST" })
      setSettings(s)
      setNotice(t("site.cache.purged"))
    } catch (err) {
      setError(errorText(err))
    } finally {
      setPurging(false)
    }
  }

  function addPath() {
    const v = pathInput.trim()
    if (!v || !settings) return
    save({ bypass_paths: [...settings.bypass_paths, v] })
    setPathInput("")
  }

  function removePath(v: string) {
    if (!settings) return
    save({ bypass_paths: settings.bypass_paths.filter((p) => p !== v) })
  }

  function addCookie() {
    const v = cookieInput.trim()
    if (!v || !settings) return
    save({ bypass_cookies: [...settings.bypass_cookies, v] })
    setCookieInput("")
  }

  function removeCookie(v: string) {
    if (!settings) return
    save({ bypass_cookies: settings.bypass_cookies.filter((c) => c !== v) })
  }

  return (
    <div data-testid="cache">
      <section aria-labelledby="cache-settings-h">
        <SectionTitle id="cache-settings-h" title={t("site.cache.title")} hint={t("site.cache.hint")} />
        {settings === null ? (
          <LoadingRows rows={2} />
        ) : (
          <>
            <SettingsRow label={t("site.cache.enable")} htmlFor="cache-enable" hint={!manage ? t("common.readOnly") : t("site.cache.enableHint")}>
              <div className="flex items-center gap-3">
                <Checkbox
                  id="cache-enable"
                  checked={settings.enabled}
                  disabled={!manage || busy}
                  onCheckedChange={(v) => save({ enabled: !!v })}
                />
                <Badge variant={statusVariant(settings.status)}>{t(`site.cache.statuses.${settings.status}`, undefined, settings.status)}</Badge>
              </div>
              {settings.error ? <p className="mt-1 text-xs text-grid-danger">{settings.error}</p> : null}
            </SettingsRow>

            {settings.enabled ? (
              <>
                <SettingsRow label={t("site.cache.ttl")} htmlFor="cache-ttl" hint={t("site.cache.ttlHint")}>
                  <Select
                    value={String(settings.ttl_seconds)}
                    onValueChange={(v) => save({ ttl_seconds: Number(v) })}
                    disabled={!manage || busy}
                  >
                    <SelectTrigger id="cache-ttl" className="w-40">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {TTL_PRESETS.map((seconds) => (
                        <SelectItem key={seconds} value={String(seconds)}>
                          {t("site.cache.ttlSeconds", { seconds })}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </SettingsRow>

                <SettingsRow label={t("site.cache.bypassPaths")} hint={t("site.cache.bypassPathsHint")}>
                  <div className="flex flex-wrap gap-2">
                    {settings.bypass_paths.map((p) => (
                      <Badge key={p} variant="outline" className="gap-1">
                        <span dir="ltr">{p}</span>
                        {manage ? (
                          <button type="button" aria-label={t("site.cache.remove")} onClick={() => removePath(p)} disabled={busy}>
                            <XIcon className="size-3" />
                          </button>
                        ) : null}
                      </Badge>
                    ))}
                  </div>
                  {manage ? (
                    <div className="mt-2 flex max-w-sm gap-2">
                      <Input
                        dir="ltr"
                        value={pathInput}
                        placeholder={t("site.cache.bypassPathsPlaceholder")}
                        onChange={(e) => setPathInput(e.target.value)}
                        onKeyDown={(e) => e.key === "Enter" && addPath()}
                      />
                      <Button type="button" variant="outline" size="icon" disabled={busy || !pathInput.trim()} onClick={addPath}>
                        <PlusIcon />
                      </Button>
                    </div>
                  ) : null}
                </SettingsRow>

                <SettingsRow label={t("site.cache.bypassCookies")} hint={t("site.cache.bypassCookiesHint")}>
                  <div className="flex flex-wrap gap-2">
                    {settings.bypass_cookies.map((c) => (
                      <Badge key={c} variant="outline" className="gap-1">
                        <span dir="ltr">{c}</span>
                        {manage ? (
                          <button type="button" aria-label={t("site.cache.remove")} onClick={() => removeCookie(c)} disabled={busy}>
                            <XIcon className="size-3" />
                          </button>
                        ) : null}
                      </Badge>
                    ))}
                  </div>
                  {manage ? (
                    <div className="mt-2 flex max-w-sm gap-2">
                      <Input
                        dir="ltr"
                        value={cookieInput}
                        placeholder={t("site.cache.bypassCookiesPlaceholder")}
                        onChange={(e) => setCookieInput(e.target.value)}
                        onKeyDown={(e) => e.key === "Enter" && addCookie()}
                      />
                      <Button type="button" variant="outline" size="icon" disabled={busy || !cookieInput.trim()} onClick={addCookie}>
                        <PlusIcon />
                      </Button>
                    </div>
                  ) : null}
                </SettingsRow>

                <SettingsRow label={t("site.cache.stats")} hint={t("site.cache.statsHint")}>
                  <div className="flex items-center gap-4 text-sm">
                    <span dir="ltr">
                      <ZapIcon className="me-1 inline size-3.5 text-grid-brand" />
                      {settings.hits.toLocaleString()} / {settings.misses.toLocaleString()}
                    </span>
                    <span className="text-xs text-grid-muted">
                      {t("site.cache.lastPurged")}: {settings.last_purged_at ? new Date(settings.last_purged_at).toLocaleString() : t("site.cache.never")}
                    </span>
                  </div>
                  {manage ? (
                    <div className="mt-2">
                      <Button type="button" variant="outline" disabled={purging} onClick={purge}>
                        {purging ? t("common.working") : t("site.cache.purge")}
                      </Button>
                      <p className="mt-1 text-xs text-grid-muted">{t("site.cache.purgeHint")}</p>
                    </div>
                  ) : null}
                </SettingsRow>
              </>
            ) : (
              <p className="border-b border-line px-6 py-4 text-sm text-grid-muted">{t("site.cache.disabledHint")}</p>
            )}
          </>
        )}
        {notice ? <p className="border-b border-line px-6 py-2 text-xs text-grid-brand">{notice}</p> : null}
        <ErrorBand message={error} />
      </section>
    </div>
  )
}
