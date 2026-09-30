"use client"

import { Progress } from "@fadymondy/nasaq/web"
import { useEffect, useState } from "react"

import { useErrorText } from "@/components/form-error"
import { ErrorBand, LoadingRows, SectionTitle } from "@/components/states"
import { apiFetch } from "@/lib/api"
import { useI18n } from "@/lib/i18n"
import { percent } from "@/lib/servers"

type Usage = {
  max_servers: number | null
  max_apps: number | null
  max_data_services: number | null
  servers: number
  apps: number
  data_services: number
}

/**
 * Usage & quotas (MH-574 E13): this team's servers/apps/data-services against its quota (set by a
 * platform admin; null = unlimited). Counts only what Hosbah manages — an imported app or database
 * never counts against a quota.
 */
export function TeamQuotasCard({ team }: { team: string }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [usage, setUsage] = useState<Usage | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    apiFetch<Usage>(`/api/teams/${team}/quotas`)
      .then((u) => {
        setUsage(u)
        setError(null)
      })
      .catch((err) => setError(errorText(err)))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [team])

  return (
    <section aria-labelledby="quota-h">
      <SectionTitle id="quota-h" title={t("settings.quotasTitle")} hint={t("settings.quotasHint")} />
      <ErrorBand message={error} />
      {!usage ? (
        error ? null : <LoadingRows rows={3} />
      ) : (
        <dl className="grid gap-px border-b border-border bg-border sm:grid-cols-3">
          <QuotaMeter label={t("settings.quotaServers")} used={usage.servers} max={usage.max_servers} />
          <QuotaMeter label={t("settings.quotaApps")} used={usage.apps} max={usage.max_apps} />
          <QuotaMeter label={t("settings.quotaDataServices")} used={usage.data_services} max={usage.max_data_services} />
        </dl>
      )}
    </section>
  )
}

function QuotaMeter({ label, used, max }: { label: string; used: number; max: number | null }) {
  const { t } = useI18n()
  const text = max === null ? t("settings.quotaUnlimited") : t("settings.quotaOf", { used, max })
  return (
    <div className="flex flex-col gap-2 bg-background px-6 py-4">
      <dt className="eyebrow">{label}</dt>
      <dd className="flex flex-col gap-2">
        <span dir="ltr" className="text-start font-mono text-sm tabular-nums">
          {text}
        </span>
        {max !== null ? <Progress value={percent(used, max)} aria-label={label} size="sm" /> : null}
      </dd>
    </div>
  )
}
