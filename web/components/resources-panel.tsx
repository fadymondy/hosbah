"use client"

import { useEffect, useState, type FormEvent } from "react"

import { useErrorText } from "@/components/form-error"
import { ErrorBand, LoadingRows, SectionTitle, SettingsRow } from "@/components/states"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { apiFetch } from "@/lib/api"
import { useI18n } from "@/lib/i18n"

/** Mirrors agentproto.ResourceLimits (internal/api/resources_handler.go). 0 means unlimited. */
export type ResourceLimits = { memory_mb: number; cpus: number }

const EMPTY: ResourceLimits = { memory_mb: 0, cpus: 0 }

const MEMORY_PRESETS_MB = [256, 512, 1024, 2048, 4096]
const CPU_PRESETS = [0.25, 0.5, 1, 2, 4]

function memoryLabel(mb: number): string {
  return mb >= 1024 && mb % 1024 === 0 ? `${mb / 1024} GB` : `${mb} MB`
}

function memoryPresetKey(mb: number): string {
  if (mb === 0) return "unlimited"
  return MEMORY_PRESETS_MB.includes(mb) ? String(mb) : "custom"
}

function cpuPresetKey(cpus: number): string {
  if (cpus === 0) return "unlimited"
  return CPU_PRESETS.includes(cpus) ? String(cpus) : "custom"
}

/** One process's (or the base's) memory + CPU limits: a preset selector, with a custom field when needed. */
function ResourceFields({
  idPrefix,
  value,
  onChange,
  disabled,
}: {
  idPrefix: string
  value: ResourceLimits
  onChange: (v: ResourceLimits) => void
  disabled: boolean
}) {
  const { t } = useI18n()
  const [memMode, setMemMode] = useState<"preset" | "custom">(memoryPresetKey(value.memory_mb) === "custom" ? "custom" : "preset")
  const [cpuMode, setCpuMode] = useState<"preset" | "custom">(cpuPresetKey(value.cpus) === "custom" ? "custom" : "preset")
  const memSelectValue = memMode === "custom" ? "custom" : memoryPresetKey(value.memory_mb)
  const cpuSelectValue = cpuMode === "custom" ? "custom" : cpuPresetKey(value.cpus)

  return (
    <div className="flex flex-wrap items-center gap-x-6 gap-y-2">
      <div className="flex items-center gap-2">
        <label htmlFor={`${idPrefix}-mem`} className="text-xs text-grid-muted">
          {t("resources.memory")}
        </label>
        <Select
          value={memSelectValue}
          disabled={disabled}
          onValueChange={(v) => {
            if (!v) return
            if (v === "custom") {
              setMemMode("custom")
              return
            }
            setMemMode("preset")
            onChange({ ...value, memory_mb: v === "unlimited" ? 0 : Number(v) })
          }}
        >
          <SelectTrigger id={`${idPrefix}-mem`} size="sm" className="w-32">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="unlimited">{t("resources.unlimited")}</SelectItem>
            {MEMORY_PRESETS_MB.map((mb) => (
              <SelectItem key={mb} value={String(mb)}>
                {memoryLabel(mb)}
              </SelectItem>
            ))}
            <SelectItem value="custom">{t("resources.custom")}</SelectItem>
          </SelectContent>
        </Select>
        {memMode === "custom" ? (
          <span className="flex items-center gap-1">
            <Input
              dir="ltr"
              type="number"
              min={32}
              max={1024 * 1024}
              className="w-24"
              disabled={disabled}
              aria-label={t("resources.memory")}
              value={value.memory_mb || ""}
              onChange={(e) => onChange({ ...value, memory_mb: Number(e.target.value) })}
            />
            <span dir="ltr" className="text-xs text-grid-muted">
              MB
            </span>
          </span>
        ) : null}
      </div>
      <div className="flex items-center gap-2">
        <label htmlFor={`${idPrefix}-cpu`} className="text-xs text-grid-muted">
          {t("resources.cpu")}
        </label>
        <Select
          value={cpuSelectValue}
          disabled={disabled}
          onValueChange={(v) => {
            if (!v) return
            if (v === "custom") {
              setCpuMode("custom")
              return
            }
            setCpuMode("preset")
            onChange({ ...value, cpus: v === "unlimited" ? 0 : Number(v) })
          }}
        >
          <SelectTrigger id={`${idPrefix}-cpu`} size="sm" className="w-28">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="unlimited">{t("resources.unlimited")}</SelectItem>
            {CPU_PRESETS.map((c) => (
              <SelectItem key={c} value={String(c)}>
                {c}
              </SelectItem>
            ))}
            <SelectItem value="custom">{t("resources.custom")}</SelectItem>
          </SelectContent>
        </Select>
        {cpuMode === "custom" ? (
          <Input
            dir="ltr"
            type="number"
            min={0.05}
            max={256}
            step={0.05}
            className="w-20"
            disabled={disabled}
            aria-label={t("resources.cpu")}
            value={value.cpus || ""}
            onChange={(e) => onChange({ ...value, cpus: Number(e.target.value) })}
          />
        ) : null}
      </div>
    </div>
  )
}

type AppResourcesOut = { resources: ResourceLimits; processes?: Record<string, ResourceLimits> }

/**
 * Resources: memory and CPU limits (MH-574 E13). Shared by an app's settings (with per-process
 * overrides) and a data service's settings (base only). Saving applies live to the running
 * container(s) — no redeploy or restart needed — and sticks for the next one too.
 */
export function ResourcesCard({
  path,
  manage,
  processes,
  readOnlyReason,
}: {
  /** GET/PUT …/resources for this app or data service. */
  path: string
  manage: boolean
  /** Process names ("web", "worker:<name>") to offer per-process overrides for. Omit for a data service. */
  processes?: string[]
  /** Set when saving is refused (an imported data service): shown instead of the save button. */
  readOnlyReason?: string
}) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [loaded, setLoaded] = useState(false)
  const [base, setBase] = useState<ResourceLimits>(EMPTY)
  const [overrides, setOverrides] = useState<Record<string, ResourceLimits>>({})
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [saved, setSaved] = useState(false)
  const canEdit = manage && !readOnlyReason

  useEffect(() => {
    let alive = true
    setLoaded(false)
    ;(async () => {
      try {
        if (processes) {
          const r = await apiFetch<AppResourcesOut>(path)
          if (!alive) return
          setBase(r.resources ?? EMPTY)
          setOverrides(r.processes ?? {})
        } else {
          const r = await apiFetch<ResourceLimits>(path)
          if (!alive) return
          setBase(r ?? EMPTY)
        }
        setError(null)
      } catch (err) {
        if (alive) setError(errorText(err))
      } finally {
        if (alive) setLoaded(true)
      }
    })()
    return () => {
      alive = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path])

  function setOverride(name: string, v: ResourceLimits | null) {
    setOverrides((o) => {
      const next = { ...o }
      if (!v) delete next[name]
      else next[name] = v
      return next
    })
  }

  async function save(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    setSaved(false)
    try {
      if (processes) {
        await apiFetch(path, { method: "PUT", body: { resources: base, processes: overrides } })
      } else {
        await apiFetch(path, { method: "PUT", body: { resources: base } })
      }
      setSaved(true)
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  if (!loaded) {
    return (
      <section aria-labelledby="res-h">
        <SectionTitle id="res-h" title={t("resources.title")} hint={t("resources.hint")} />
        <LoadingRows rows={2} />
      </section>
    )
  }

  return (
    <form onSubmit={save} aria-labelledby="res-h">
      <SectionTitle id="res-h" title={t("resources.title")} hint={t("resources.hint")} />
      <ErrorBand message={error} />
      {readOnlyReason ? <p className="border-b border-line px-6 py-3 text-sm text-grid-muted">{readOnlyReason}</p> : null}
      <SettingsRow label={t("resources.base")} hint={t("resources.baseHint")}>
        <ResourceFields idPrefix="res-base" value={base} onChange={setBase} disabled={!canEdit || busy} />
      </SettingsRow>
      {processes && processes.length > 0 ? (
        <SettingsRow label={t("resources.processes")} hint={t("resources.processesHint")}>
          <div className="flex flex-col gap-4">
            {processes.map((name) => {
              const has = name in overrides
              const value = overrides[name] ?? base
              return (
                <div key={name} className="flex flex-wrap items-center gap-3">
                  <label className="flex items-center gap-2 text-sm">
                    <Checkbox
                      checked={has}
                      disabled={!canEdit || busy}
                      onCheckedChange={(v) => setOverride(name, v ? { ...base } : null)}
                    />
                    <span dir="ltr" className="font-mono">
                      {name}
                    </span>
                  </label>
                  {has ? (
                    <ResourceFields idPrefix={`res-${name}`} value={value} onChange={(v) => setOverride(name, v)} disabled={!canEdit || busy} />
                  ) : (
                    <span className="text-xs text-grid-muted">{t("resources.usesBase")}</span>
                  )}
                </div>
              )
            })}
          </div>
        </SettingsRow>
      ) : null}
      {canEdit ? (
        <div className="flex flex-wrap items-center justify-end gap-3 border-b border-line px-6 py-4">
          {saved ? (
            <p role="status" className="text-sm text-grid-muted">
              {t("resources.saved")}
            </p>
          ) : null}
          <Button type="submit" disabled={busy}>
            {busy ? t("common.working") : t("common.save")}
          </Button>
        </div>
      ) : null}
    </form>
  )
}
