"use client"

import { Button, Checkbox, Input } from "@fadymondy/nasaq/web"
import { useEffect, useState, type FormEvent } from "react"
import { toast } from "sonner"

import { ListError, LoadingRows } from "@/components/admin/admin-list"
import { useErrorText } from "@/components/form-error"
import { ErrorBand, SectionTitle, SettingsRow } from "@/components/states"



import { useAdminResource } from "@/lib/admin"
import { apiFetch } from "@/lib/api"
import { useI18n } from "@/lib/i18n"

type Quota = { max_servers: number | null; max_apps: number | null; max_data_services: number | null }

/**
 * A team's quota (MH-574 E13): a platform admin's own guardrail — servers/apps/data services — on
 * top of the platform-wide settings. Null (the "unlimited" checkbox) clears a field.
 */
export function QuotaEditor({ teamId }: { teamId: string }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const res = useAdminResource<Quota>(`/api/admin/teams/${encodeURIComponent(teamId)}/quota`)
  const [form, setForm] = useState<Quota | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (res.data) setForm(res.data)
  }, [res.data])

  async function submit(e: FormEvent) {
    e.preventDefault()
    if (!form) return
    setBusy(true)
    setError(null)
    try {
      await apiFetch(`/api/admin/teams/${encodeURIComponent(teamId)}/quota`, { method: "PUT", body: form })
      toast.success(t("admin.teams.quotaSaved"))
      await res.reload()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section aria-labelledby="quota-h">
      <SectionTitle id="quota-h" title={t("admin.teams.quotaTitle")} hint={t("admin.teams.quotaHint")} />
      {res.error ? (
        <ListError error={res.error} />
      ) : !form ? (
        <LoadingRows rows={3} />
      ) : (
        <form onSubmit={submit}>
          <ErrorBand message={error} />
          <QuotaField
            label={t("admin.teams.quotaServers")}
            value={form.max_servers}
            onChange={(v) => setForm((f) => (f ? { ...f, max_servers: v } : f))}
          />
          <QuotaField
            label={t("admin.teams.quotaApps")}
            value={form.max_apps}
            onChange={(v) => setForm((f) => (f ? { ...f, max_apps: v } : f))}
          />
          <QuotaField
            label={t("admin.teams.quotaDataServices")}
            value={form.max_data_services}
            onChange={(v) => setForm((f) => (f ? { ...f, max_data_services: v } : f))}
          />
          <div className="flex justify-end border-b border-border px-6 py-4">
            <Button variant="primary" type="submit" disabled={busy}>
              {busy ? t("common.working") : t("common.save")}
            </Button>
          </div>
        </form>
      )}
    </section>
  )
}

function QuotaField({ label, value, onChange }: { label: string; value: number | null; onChange: (v: number | null) => void }) {
  const { t } = useI18n()
  const unlimited = value === null
  return (
    <SettingsRow label={label}>
      <div className="flex flex-wrap items-center gap-3">
        <label className="flex items-center gap-2 text-sm">
          <Checkbox checked={unlimited} onCheckedChange={(v) => onChange(v ? null : 0)} />
          {t("admin.teams.quotaUnlimited")}
        </label>
        {!unlimited ? (
          <Input
            dir="ltr"
            type="number"
            min={0}
            max={100000}
            className="w-28 font-mono"
            aria-label={label}
            value={value}
            onChange={(e) => onChange(Number(e.target.value))}
          />
        ) : null}
      </div>
    </SettingsRow>
  )
}
