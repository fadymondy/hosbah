"use client"

import {
  Badge,
  Button,
  Checkbox,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Field,
  FieldDescription,
  FieldLabel,
  Input,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  StatCard,
  StatGrid,
} from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useState, type FormEvent } from "react"
import { CircleSlashIcon, EraserIcon, GaugeIcon, PlusIcon, XIcon, ZapIcon } from "lucide-react"

import { FormError, useErrorText } from "@/components/form-error"
import { useLoadedSite } from "@/components/site/context"
import { ErrorBand, LoadingRows, SectionTitle, SettingsRow } from "@/components/states"
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

function statusVariant(status: string): "success" | "neutral" | "outline" | "danger" {
  if (status === "error") return "danger"
  if (status === "running") return "success"
  if (status === "pending") return "neutral"
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
  const [adding, setAdding] = useState(false)

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

  async function put(next: Partial<CacheSettings>) {
    if (!settings) return
    const body = {
      enabled: next.enabled ?? settings.enabled,
      ttl_seconds: next.ttl_seconds || settings.ttl_seconds || 60,
      bypass_paths: next.bypass_paths ?? settings.bypass_paths,
      bypass_cookies: next.bypass_cookies ?? settings.bypass_cookies,
    }
    const s = await apiFetch<CacheSettings>(base, { method: "PUT", body })
    setSettings(s)
    setNotice(t("site.cache.saved"))
  }

  async function save(next: Partial<CacheSettings>) {
    setBusy(true)
    setError(null)
    setNotice(null)
    try {
      await put(next)
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

  function removePath(v: string) {
    if (!settings) return
    save({ bypass_paths: settings.bypass_paths.filter((p) => p !== v) })
  }

  function removeCookie(v: string) {
    if (!settings) return
    save({ bypass_cookies: settings.bypass_cookies.filter((c) => c !== v) })
  }

  const lookups = settings ? settings.hits + settings.misses : 0
  const chips = (values: string[], remove: (v: string) => void) =>
    values.length === 0 ? (
      <span className="text-xs text-muted-foreground">—</span>
    ) : (
      <div className="flex flex-wrap gap-2">
        {values.map((v) => (
          <Badge key={v} variant="outline" className="gap-1">
            <span dir="ltr">{v}</span>
            {manage ? (
              <button type="button" aria-label={t("site.cache.remove")} onClick={() => remove(v)} disabled={busy}>
                <XIcon className="size-3" />
              </button>
            ) : null}
          </Badge>
        ))}
      </div>
    )

  return (
    <div data-testid="cache" className="flex flex-col gap-3">
      <section aria-labelledby="cache-settings-h">
        <SectionTitle
          id="cache-settings-h"
          title={t("site.cache.title")}
          hint={t("site.cache.hint")}
          action={
            manage && settings?.enabled ? (
              <Button type="button" variant="secondary" size="sm" disabled={purging} onClick={purge} title={t("site.cache.purgeHint")}>
                <EraserIcon />
                {purging ? t("common.working") : t("site.cache.purge")}
              </Button>
            ) : null
          }
        />
        {settings === null ? (
          <LoadingRows rows={2} />
        ) : (
          <>
            {settings.enabled ? (
              <StatGrid className="nq-stats">
                <StatCard
                  icon={<GaugeIcon />}
                  label={t("site.cache.hitRate")}
                  value={<span dir="ltr">{lookups > 0 ? `${((settings.hits / lookups) * 100).toFixed(1)}%` : "—"}</span>}
                />
                <StatCard icon={<ZapIcon />} label={t("site.cache.hits")} value={settings.hits} />
                <StatCard icon={<CircleSlashIcon />} label={t("site.cache.misses")} value={settings.misses} />
                <StatCard
                  icon={<EraserIcon />}
                  label={t("site.cache.lastPurged")}
                  value={<span className="text-base">{settings.last_purged_at ? new Date(settings.last_purged_at).toLocaleString() : t("site.cache.never")}</span>}
                />
              </StatGrid>
            ) : null}
            <SettingsRow label={t("site.cache.enable")} htmlFor="cache-enable" hint={!manage ? t("common.readOnly") : t("site.cache.enableHint")}>
              <div className="flex items-center gap-3">
                <Checkbox id="cache-enable" checked={settings.enabled} disabled={!manage || busy} onCheckedChange={(v) => save({ enabled: !!v })} />
                <Badge variant={statusVariant(settings.status)}>{t(`site.cache.statuses.${settings.status}`, undefined, settings.status)}</Badge>
              </div>
              {settings.error ? <p className="mt-1 text-xs text-nq-danger-text">{settings.error}</p> : null}
            </SettingsRow>

            {settings.enabled ? (
              <SettingsRow label={t("site.cache.ttl")} htmlFor="cache-ttl" hint={t("site.cache.ttlHint")}>
                <Select value={String(settings.ttl_seconds)} onValueChange={(v) => save({ ttl_seconds: Number(v) })} disabled={!manage || busy}>
                  <SelectTrigger id="cache-ttl" className="w-40">
                    <SelectValue>{(v: string) => t("site.cache.ttlSeconds", { seconds: Number(v) })}</SelectValue>
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
            ) : (
              <p className="nq-band py-4 text-sm text-muted-foreground">{t("site.cache.disabledHint")}</p>
            )}
          </>
        )}
        {notice ? <p className="nq-band py-2 text-xs text-nq-brand">{notice}</p> : null}
        <ErrorBand message={error} />
      </section>

      {settings?.enabled ? (
        <section aria-labelledby="cache-bypass-h">
          <SectionTitle
            id="cache-bypass-h"
            title={t("site.cache.bypassTitle")}
            hint={t("site.cache.bypassHint")}
            action={
              manage ? (
                <Button type="button" variant="primary" size="sm" onClick={() => setAdding(true)}>
                  <PlusIcon />
                  {t("site.cache.addBypass")}
                </Button>
              ) : null
            }
          />
          <SettingsRow label={t("site.cache.bypassPaths")} hint={t("site.cache.bypassPathsHint")}>
            {chips(settings.bypass_paths, removePath)}
          </SettingsRow>
          <SettingsRow label={t("site.cache.bypassCookies")} hint={t("site.cache.bypassCookiesHint")}>
            {chips(settings.bypass_cookies, removeCookie)}
          </SettingsRow>
        </section>
      ) : null}

      {adding && settings ? (
        <BypassDialog
          onClose={() => setAdding(false)}
          onSave={(kind, value) =>
            put(kind === "path" ? { bypass_paths: [...settings.bypass_paths, value] } : { bypass_cookies: [...settings.bypass_cookies, value] })
          }
        />
      ) : null}
    </div>
  )
}

/** Add one bypass rule: a path prefix or a cookie name that always skips the cache. */
function BypassDialog({ onClose, onSave }: { onClose: () => void; onSave: (kind: "path" | "cookie", value: string) => Promise<void> }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [kind, setKind] = useState<"path" | "cookie">("path")
  const [value, setValue] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  async function submit(e: FormEvent) {
    e.preventDefault()
    const v = value.trim()
    if (!v) return
    setBusy(true)
    setError(null)
    try {
      await onSave(kind, v)
      onClose()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Dialog open onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="max-h-[90dvh] overflow-y-auto">
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{t("site.cache.addBypass")}</DialogTitle>
            <DialogDescription>{t("site.cache.bypassHint")}</DialogDescription>
          </DialogHeader>
          <div className="flex w-full flex-col gap-5">
            <Field>
              <FieldLabel htmlFor="bypass-kind">{t("site.cache.bypassKind")}</FieldLabel>
              <Select value={kind} onValueChange={(v) => setKind(v === "cookie" ? "cookie" : "path")}>
                <SelectTrigger id="bypass-kind" className="w-full">
                  <SelectValue>{(v: string) => (v === "cookie" ? t("site.cache.bypassCookies") : t("site.cache.bypassPaths"))}</SelectValue>
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="path">{t("site.cache.bypassPaths")}</SelectItem>
                  <SelectItem value="cookie">{t("site.cache.bypassCookies")}</SelectItem>
                </SelectContent>
              </Select>
            </Field>
            <Field>
              <FieldLabel htmlFor="bypass-value">{kind === "cookie" ? t("site.cache.cookieName") : t("site.cache.pathPrefix")}</FieldLabel>
              <Input
                id="bypass-value"
                dir="ltr"
                autoFocus
                value={value}
                placeholder={kind === "cookie" ? t("site.cache.bypassCookiesPlaceholder") : t("site.cache.bypassPathsPlaceholder")}
                onChange={(e) => setValue(e.target.value)}
              />
              <FieldDescription>{kind === "cookie" ? t("site.cache.bypassCookiesHint") : t("site.cache.bypassPathsHint")}</FieldDescription>
            </Field>
          </div>
          <FormError message={error} />
          <DialogFooter>
            <Button type="button" variant="secondary" onClick={onClose}>
              {t("common.cancel")}
            </Button>
            <Button variant="primary" type="submit" disabled={busy || !value.trim()}>
              {busy ? t("common.working") : t("common.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
