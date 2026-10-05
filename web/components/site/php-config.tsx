"use client"

import { Button, Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, toast } from "@fadymondy/nasaq/web"
import { useEffect, useState } from "react"
import { PlusIcon, Trash2Icon } from "lucide-react"

import { useErrorText } from "@/components/form-error"
import { useLoadedSite } from "@/components/site/context"
import { ErrorBand, LoadingRows, Notice, SectionTitle } from "@/components/states"



import { apiFetch } from "@/lib/api"
import { useI18n } from "@/lib/i18n"

/** Mirrors internal/api PHPConfigOut. */
export type PHPConfig = {
  php_version: string
  php_ini: Record<string, string>
  available_versions: string[]
  available_ini_keys: string[]
  can_save: boolean
}

type Draft = { version: string; ini: Record<string, string> }
const draftOf = (c: PHPConfig): Draft => ({ version: c.php_version, ini: { ...c.php_ini } })
const same = (a: Draft, b: Draft) => a.version === b.version && JSON.stringify(a.ini) === JSON.stringify(b.ini)

/** An ini value may not contain the characters php.ini uses for comments, sections or line breaks. */
const INI_VALUE_RE = /^[a-zA-Z0-9_./: +-]{0,200}$/

/** The per-app PHP version pin and php.ini overrides (MH-576 D2), applied on the next deploy. */
export function SitePHPConfig() {
  const { t } = useI18n()
  const errorText = useErrorText()
  const { path } = useLoadedSite()
  const configPath = `${path}/deploy-config/php`

  const [config, setConfig] = useState<PHPConfig | null>(null)
  const [draft, setDraft] = useState<Draft | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [addKey, setAddKey] = useState("")
  const [newValue, setNewValue] = useState("")

  useEffect(() => {
    setConfig(null)
    setDraft(null)
    setError(null)
    apiFetch<PHPConfig>(configPath)
      .then((c) => {
        setConfig(c)
        setDraft(draftOf(c))
      })
      .catch((err) => setError(errorText(err)))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [configPath])

  const dirty = !!config && !!draft && !same(draft, draftOf(config))
  const canSave = config?.can_save ?? false
  const usedKeys = new Set(Object.keys(draft?.ini ?? {}))
  const availableKeys = (config?.available_ini_keys ?? []).filter((k) => !usedKeys.has(k))
  const newValueOk = !newValue || INI_VALUE_RE.test(newValue)

  function setValue(key: string, value: string) {
    setDraft((d) => (d ? { ...d, ini: { ...d.ini, [key]: value } } : d))
  }

  function removeKey(key: string) {
    setDraft((d) => {
      if (!d) return d
      const ini = { ...d.ini }
      delete ini[key]
      return { ...d, ini }
    })
  }

  function addSetting() {
    if (!addKey || !newValue || !newValueOk) return
    setDraft((d) => (d ? { ...d, ini: { ...d.ini, [addKey]: newValue } } : d))
    setAddKey("")
    setNewValue("")
  }

  function discard() {
    if (config) setDraft(draftOf(config))
  }

  async function save() {
    if (!draft) return
    setBusy(true)
    setError(null)
    try {
      const r = await apiFetch<PHPConfig>(configPath, { method: "PUT", body: { php_version: draft.version, php_ini: draft.ini } })
      setConfig(r)
      setDraft(draftOf(r))
      toast.success(t("site.php.saved"))
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section aria-labelledby="php-h">
      <SectionTitle id="php-h" title={t("site.php.title")} hint={t("site.php.hint")} />
      <ErrorBand message={error} />
      {!config || !draft ? (
        <LoadingRows rows={3} />
      ) : (
        <>
          {!canSave ? <Notice>{t("common.readOnly")}</Notice> : null}
          <fieldset disabled={!canSave || busy} className="contents">
            <div className="nq-band py-5">
              <label className="mb-1 block text-sm font-medium text-foreground">{t("site.php.version")}</label>
              <p className="mb-3 max-w-xl text-xs text-muted-foreground">{t("site.php.versionHint")}</p>
              <Select value={draft.version} onValueChange={(v) => v && setDraft((d) => (d ? { ...d, version: v } : d))} disabled={!canSave || busy}>
                <SelectTrigger className="w-full sm:w-48">
                  <SelectValue>{(v: string) => (v === "auto" ? t("site.php.versionAuto") : v)}</SelectValue>
                </SelectTrigger>
                <SelectContent>
                  {config.available_versions.map((v) => (
                    <SelectItem key={v} value={v}>
                      {v === "auto" ? t("site.php.versionAuto") : v}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>

            <div className="flex flex-col gap-3 px-6 py-5">
              <p className="text-sm font-medium text-foreground">{t("site.php.ini")}</p>
              <p className="max-w-xl text-xs text-muted-foreground">{t("site.php.iniHint")}</p>

              {Object.keys(draft.ini).length === 0 ? (
                <p className="py-2 text-sm text-muted-foreground">{t("site.php.iniEmpty")}</p>
              ) : (
                <div className="flex flex-col gap-2">
                  {Object.keys(draft.ini)
                    .sort()
                    .map((key) => {
                      const value = draft.ini[key]
                      const ok = INI_VALUE_RE.test(value)
                      return (
                        <div key={key} className="flex flex-wrap items-center gap-2">
                          <span dir="ltr" className="w-full font-mono text-xs text-foreground sm:w-56 sm:shrink-0">
                            {key}
                          </span>
                          <Input
                            dir="ltr"
                            maxLength={200}
                            value={value}
                            aria-invalid={!ok}
                            onChange={(e) => setValue(key, e.target.value)}
                            className="min-w-0 flex-1 font-mono text-xs"
                          />
                          <Button type="button" variant="ghost" size="icon-sm" onClick={() => removeKey(key)} aria-label={t("site.php.removeSetting")}>
                            <Trash2Icon />
                          </Button>
                          {!ok ? <p className="w-full text-xs text-destructive">{t("site.php.invalidValue")}</p> : null}
                        </div>
                      )
                    })}
                </div>
              )}

              <div className="flex flex-wrap items-end gap-2 border-t border-border pt-4">
                <div>
                  <label className="mb-1 block text-xs font-medium text-foreground">{t("site.php.key")}</label>
                  <Select value={addKey} onValueChange={(v) => setAddKey(v ?? "")} disabled={!canSave || busy || availableKeys.length === 0}>
                    <SelectTrigger className="w-full sm:w-56">
                      <SelectValue placeholder={t("site.php.key")} />
                    </SelectTrigger>
                    <SelectContent>
                      {availableKeys.map((k) => (
                        <SelectItem key={k} value={k}>
                          {k}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
                <div>
                  <label className="mb-1 block text-xs font-medium text-foreground">{t("site.php.value")}</label>
                  <Input
                    dir="ltr"
                    maxLength={200}
                    value={newValue}
                    aria-invalid={!newValueOk}
                    onChange={(e) => setNewValue(e.target.value)}
                    onKeyDown={(e) => {
                      if (e.key === "Enter") {
                        e.preventDefault()
                        addSetting()
                      }
                    }}
                    className="w-full sm:w-48 font-mono text-xs"
                  />
                </div>
                <Button type="button" variant="secondary" size="sm" disabled={!canSave || busy || !addKey || !newValue || !newValueOk} onClick={addSetting}>
                  <PlusIcon />
                  {t("site.php.addSetting")}
                </Button>
              </div>
              {!newValueOk ? <p className="text-xs text-destructive">{t("site.php.invalidValue")}</p> : null}
            </div>
          </fieldset>
        </>
      )}

      {canSave && dirty ? (
        <div className="sticky bottom-0 z-20 flex items-center justify-between gap-3 border-t border-border bg-popover/95 px-6 py-3 backdrop-blur supports-backdrop-filter:bg-popover/80">
          <p className="text-xs text-muted-foreground">{t("site.deployScript.unsaved")}</p>
          <div className="flex gap-2">
            <Button type="button" variant="secondary" size="sm" disabled={busy} onClick={discard}>
              {t("common.discard")}
            </Button>
            <Button variant="primary" type="button" size="sm" disabled={busy} onClick={() => void save()}>
              {busy ? t("common.working") : t("common.save")}
            </Button>
          </div>
        </div>
      ) : null}
    </section>
  )
}
