"use client"

import { useEffect, useState } from "react"
import { ArrowDownIcon, ArrowUpIcon, HistoryIcon, PlusIcon, Trash2Icon } from "lucide-react"
import { toast } from "sonner"

import { useErrorText } from "@/components/form-error"
import { useLoadedSite } from "@/components/site/context"
import { ErrorBand, LoadingRows, Notice, SectionTitle } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Textarea } from "@/components/ui/textarea"
import { apiFetch } from "@/lib/api"
import { useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"
import { cn } from "@/lib/utils"

/** Mirrors internal/api DeployStepIn/DeployConfigOut/DeployConfigVersionOut/DeployPresetOut. */
export type DeployMode = "auto" | "fast" | "full"
export type DeployStepKey = "install" | "build" | "migrate" | "release" | "after_release" | "on_failure"
export const DEPLOY_STEP_KEYS: DeployStepKey[] = ["install", "build", "migrate", "release", "after_release", "on_failure"]

export type DeployStep = { key: string; name: string; run: string; enabled: boolean }
export type DeployConfig = {
  mode: DeployMode
  preset: string
  steps: DeployStep[]
  version: number
  updated_by?: string | null
  updated_at: string | null
  can_save: boolean
}
export type DeployConfigVersion = {
  version: number
  mode: DeployMode
  preset: string
  steps: DeployStep[]
  updated_by?: string | null
  created_at: string
}
export type DeployPreset = { preset: string; steps: DeployStep[] }

type Draft = { mode: DeployMode; preset: string; steps: DeployStep[] }
const draftOf = (c: DeployConfig): Draft => ({ mode: c.mode, preset: c.preset, steps: c.steps.map((s) => ({ ...s })) })
const same = (a: Draft, b: Draft) => JSON.stringify(a) === JSON.stringify(b)

/** The per-app deploy script configuration: mode, preset, pipeline steps, and its version history. */
export function SiteDeployConfig() {
  const { t, locale } = useI18n()
  const { team } = useTeam()
  const errorText = useErrorText()
  const { app, path } = useLoadedSite()
  const configPath = `${path}/deploy-config`

  const [config, setConfig] = useState<DeployConfig | null>(null)
  const [draft, setDraft] = useState<Draft | null>(null)
  const [presets, setPresets] = useState<DeployPreset[] | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [pendingPreset, setPendingPreset] = useState<string | null>(null)
  const [historyOpen, setHistoryOpen] = useState(false)
  const [versions, setVersions] = useState<DeployConfigVersion[] | null>(null)
  const [restoreTarget, setRestoreTarget] = useState<DeployConfigVersion | null>(null)
  const [restoring, setRestoring] = useState(false)
  const [addKey, setAddKey] = useState("")

  useEffect(() => {
    setConfig(null)
    setDraft(null)
    setError(null)
    apiFetch<DeployConfig>(configPath)
      .then((c) => {
        setConfig(c)
        setDraft(draftOf(c))
      })
      .catch((err) => setError(errorText(err)))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [configPath])

  useEffect(() => {
    apiFetch<{ presets: DeployPreset[] }>(`/api/teams/${team.slug}/deploy-presets`)
      .then((r) => setPresets(r.presets ?? []))
      .catch(() => setPresets([]))
  }, [team.slug])

  const dirty = !!config && !!draft && !same(draft, draftOf(config))
  const canSave = config?.can_save ?? false

  function loadVersions() {
    apiFetch<{ versions: DeployConfigVersion[] }>(`${configPath}/versions`)
      .then((r) => setVersions(r.versions ?? []))
      .catch(() => setVersions([]))
  }

  function applyPreset(name: string, force: boolean) {
    if (!force && dirty) {
      setPendingPreset(name)
      return
    }
    const p = presets?.find((p) => p.preset === name)
    setDraft((d) => (d ? { ...d, preset: name, steps: p ? p.steps.map((s) => ({ ...s })) : d.steps } : d))
    setPendingPreset(null)
  }

  function updateStep(i: number, patch: Partial<DeployStep>) {
    setDraft((d) => (d ? { ...d, steps: d.steps.map((s, si) => (si === i ? { ...s, ...patch } : s)) } : d))
  }

  function moveStep(i: number, dir: -1 | 1) {
    setDraft((d) => {
      if (!d) return d
      const j = i + dir
      if (j < 0 || j >= d.steps.length) return d
      const steps = d.steps.slice()
      ;[steps[i], steps[j]] = [steps[j], steps[i]]
      return { ...d, steps }
    })
  }

  function removeStep(i: number) {
    setDraft((d) => (d ? { ...d, steps: d.steps.filter((_, si) => si !== i) } : d))
  }

  function addStep(key: DeployStepKey) {
    setDraft((d) => (d ? { ...d, steps: [...d.steps, { key, name: t(`site.deployScript.stepKeys.${key}`), run: "", enabled: true }] } : d))
  }

  function discard() {
    if (config) setDraft(draftOf(config))
  }

  async function save() {
    if (!draft) return
    setBusy(true)
    setError(null)
    try {
      const r = await apiFetch<DeployConfig>(configPath, { method: "PUT", body: draft })
      setConfig(r)
      setDraft(draftOf(r))
      toast.success(t("site.deployScript.saved"))
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  async function restore(v: DeployConfigVersion) {
    setRestoring(true)
    try {
      const r = await apiFetch<DeployConfig>(`${configPath}/versions/${v.version}/restore`, { method: "POST" })
      setConfig(r)
      setDraft(draftOf(r))
      toast.success(t("site.deployScript.restored", { version: v.version }))
      setRestoreTarget(null)
      setHistoryOpen(false)
    } catch (err) {
      throw new Error(errorText(err))
    } finally {
      setRestoring(false)
    }
  }

  const usedKeys = new Set((draft?.steps ?? []).map((s) => s.key))
  const availableKeys = DEPLOY_STEP_KEYS.filter((k) => !usedKeys.has(k))
  const sheetSide = locale === "ar" ? "left" : "right"

  return (
    <>
      <SectionTitle
        title={t("site.nav.deployScript")}
        hint={t("site.deployScript.hint")}
        action={
          <Button
            variant="outline"
            size="sm"
            onClick={() => {
              setHistoryOpen(true)
              loadVersions()
            }}
          >
            <HistoryIcon />
            {t("site.deployScript.history")}
          </Button>
        }
      />
      <ErrorBand message={error} />
      {!config || !draft ? (
        <LoadingRows rows={4} />
      ) : (
        <>
          {!canSave ? <Notice>{t("common.readOnly")}</Notice> : null}
          <fieldset disabled={!canSave || busy} className="contents">
            <div className="border-b border-line px-6 py-5">
              <p className="mb-3 text-sm font-medium text-grid-fg">{t("site.deployScript.mode")}</p>
              <div className="flex flex-wrap gap-2" role="radiogroup" aria-label={t("site.deployScript.mode")}>
                {(["auto", "fast", "full"] as DeployMode[]).map((m) => (
                  <button
                    key={m}
                    type="button"
                    role="radio"
                    aria-checked={draft.mode === m}
                    onClick={() => setDraft((d) => (d ? { ...d, mode: m } : d))}
                    className={cn(
                      "flex-1 min-w-40 border px-3 py-2 text-start transition-colors",
                      draft.mode === m ? "border-[var(--grid-brand)] bg-grid-soft" : "border-line hover:bg-grid-soft",
                    )}
                  >
                    <span className="block text-sm font-medium text-grid-fg">{t(`site.deployScript.modes.${m}`)}</span>
                    <span className="block text-xs text-grid-muted">{t(`site.deployScript.modeHints.${m}`)}</span>
                  </button>
                ))}
              </div>
            </div>

            <div className="border-b border-line px-6 py-5">
              <p className="mb-2 text-sm font-medium text-grid-fg">{t("site.deployScript.preset")}</p>
              <p className="mb-3 max-w-xl text-xs text-grid-muted">{t("site.deployScript.presetHint")}</p>
              <Select value={draft.preset} onValueChange={(v) => v && applyPreset(v, false)} disabled={!canSave || busy || !presets}>
                <SelectTrigger className="w-full sm:w-64">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {(presets ?? []).map((p) => (
                    <SelectItem key={p.preset} value={p.preset}>
                      {p.preset}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>

            <div className="flex flex-col gap-3 border-b border-line px-6 py-5">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <p className="text-sm font-medium text-grid-fg">{t("site.deployScript.steps")}</p>
                <Select
                  value={addKey}
                  onValueChange={(v) => {
                    if (v) {
                      addStep(v as DeployStepKey)
                      setAddKey("")
                    }
                  }}
                  disabled={!canSave || busy || availableKeys.length === 0}
                >
                  <SelectTrigger className="w-fit" aria-label={t("site.deployScript.addStep")}>
                    <PlusIcon className="size-3.5" />
                    <SelectValue placeholder={t("site.deployScript.addStep")} />
                  </SelectTrigger>
                  <SelectContent>
                    {availableKeys.map((k) => (
                      <SelectItem key={k} value={k}>
                        {t(`site.deployScript.stepKeys.${k}`)}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>

              {draft.steps.length === 0 ? (
                <p className="py-4 text-sm text-grid-muted">{t("site.deployScript.stepsEmpty")}</p>
              ) : (
                <div className="flex flex-col gap-3">
                  {draft.steps.map((s, i) => (
                    <div key={`${s.key}-${i}`} className="flex flex-col gap-3 border border-line bg-grid-card px-4 py-3">
                      <div className="flex flex-wrap items-center gap-2">
                        <Badge variant="outline" dir="ltr">
                          {t(`site.deployScript.stepKeys.${s.key}`, undefined, s.key)}
                        </Badge>
                        <Input
                          dir="auto"
                          maxLength={120}
                          value={s.name}
                          onChange={(e) => updateStep(i, { name: e.target.value })}
                          aria-label={t("site.deployScript.stepName")}
                          className="min-w-0 flex-1"
                        />
                        <label className="flex items-center gap-2 text-xs text-grid-muted">
                          <Checkbox checked={s.enabled} onCheckedChange={(v) => updateStep(i, { enabled: v === true })} />
                          {t("site.deployScript.enabled")}
                        </label>
                        <div className="flex items-center gap-1">
                          <Button type="button" variant="ghost" size="icon-sm" disabled={i === 0} onClick={() => moveStep(i, -1)} aria-label={t("site.deployScript.moveUp")}>
                            <ArrowUpIcon />
                          </Button>
                          <Button
                            type="button"
                            variant="ghost"
                            size="icon-sm"
                            disabled={i === draft.steps.length - 1}
                            onClick={() => moveStep(i, 1)}
                            aria-label={t("site.deployScript.moveDown")}
                          >
                            <ArrowDownIcon />
                          </Button>
                          <Button type="button" variant="ghost" size="icon-sm" onClick={() => removeStep(i)} aria-label={t("site.deployScript.removeStep")}>
                            <Trash2Icon />
                          </Button>
                        </div>
                      </div>
                      <Textarea
                        dir="ltr"
                        rows={3}
                        maxLength={4000}
                        value={s.run}
                        onChange={(e) => updateStep(i, { run: e.target.value })}
                        aria-label={t("site.deployScript.stepRun")}
                        className="font-mono text-xs"
                      />
                    </div>
                  ))}
                </div>
              )}
            </div>
          </fieldset>
        </>
      )}

      {canSave && dirty ? (
        <div className="sticky bottom-0 z-20 flex items-center justify-between gap-3 border-t border-line bg-popover/95 px-6 py-3 backdrop-blur supports-backdrop-filter:bg-popover/80">
          <p className="text-xs text-grid-muted">{t("site.deployScript.unsaved")}</p>
          <div className="flex gap-2">
            <Button type="button" variant="outline" size="sm" disabled={busy} onClick={discard}>
              {t("common.discard")}
            </Button>
            <Button type="button" size="sm" disabled={busy} onClick={() => void save()}>
              {busy ? t("common.working") : t("common.save")}
            </Button>
          </div>
        </div>
      ) : null}

      <Dialog open={!!pendingPreset} onOpenChange={(o) => !o && setPendingPreset(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("site.deployScript.presetConfirmTitle")}</DialogTitle>
            <DialogDescription>{t("site.deployScript.presetConfirmBody")}</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setPendingPreset(null)}>
              {t("common.cancel")}
            </Button>
            <Button onClick={() => pendingPreset && applyPreset(pendingPreset, true)}>{t("site.deployScript.presetConfirmAction")}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Sheet open={historyOpen} onOpenChange={setHistoryOpen}>
        <SheetContent side={sheetSide} className="w-full gap-0 p-0 sm:max-w-md">
          <SheetHeader className="border-b border-line px-4 py-4">
            <SheetTitle>{t("site.deployScript.history")}</SheetTitle>
            <SheetDescription>{t("site.deployScript.historyHint", { name: app.name })}</SheetDescription>
          </SheetHeader>
          <div className="flex-1 overflow-y-auto">
            {versions === null ? (
              <LoadingRows rows={4} />
            ) : versions.length === 0 ? (
              <p className="px-4 py-6 text-sm text-grid-muted">{t("site.deployScript.historyEmpty")}</p>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t("site.deployScript.version")}</TableHead>
                    <TableHead>{t("site.deployScript.updatedBy")}</TableHead>
                    <TableHead>{t("site.deployScript.updatedAt")}</TableHead>
                    {canSave ? <TableHead className="text-end">{t("common.actions")}</TableHead> : null}
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {versions.map((v) => (
                    <TableRow key={v.version}>
                      <TableCell dir="ltr">
                        v{v.version}
                        {v.version === config?.version ? (
                          <Badge variant="outline" className="ms-2">
                            {t("site.deployScript.current")}
                          </Badge>
                        ) : null}
                      </TableCell>
                      <TableCell dir="auto">{v.updated_by ?? "—"}</TableCell>
                      <TableCell dir="ltr">{new Date(v.created_at).toLocaleString(locale)}</TableCell>
                      {canSave ? (
                        <TableCell className="text-end">
                          <Button size="sm" variant="outline" disabled={v.version === config?.version} onClick={() => setRestoreTarget(v)}>
                            {t("site.deployScript.restore")}
                          </Button>
                        </TableCell>
                      ) : null}
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </div>
        </SheetContent>
      </Sheet>

      <Dialog open={!!restoreTarget} onOpenChange={(o) => !o && setRestoreTarget(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("site.deployScript.restoreConfirmTitle")}</DialogTitle>
            <DialogDescription>{restoreTarget ? t("site.deployScript.restoreConfirmBody", { version: restoreTarget.version }) : null}</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setRestoreTarget(null)} disabled={restoring}>
              {t("common.cancel")}
            </Button>
            <Button disabled={restoring} onClick={() => restoreTarget && void restore(restoreTarget)}>
              {restoring ? t("common.working") : t("site.deployScript.restoreConfirmAction")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}
