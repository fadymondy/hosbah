"use client"

import { Badge, Button, Checkbox, EmptyState, Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, toast } from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useState } from "react"
import { CircleOffIcon, DatabaseIcon, RefreshCwIcon, ServerIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { ConfirmButton } from "@/components/confirm-button"
import { useErrorText } from "@/components/form-error"
import { ListSearch, matches } from "@/components/server-tabs/list-tools"
import { ErrorBand, LoadingRows } from "@/components/states"





import { apiFetch } from "@/lib/api"
import { useDocumentTitle, useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

export const SHARED_SERVICE_KINDS = [
  "postgres",
  "mysql",
  "redis",
  "nats",
  "minio",
  "searxng",
  "firecrawl",
  "browser",
  "meilisearch",
  "rabbitmq",
  "elasticsearch",
  "opensearch",
  "clickhouse",
  "mongodb",
  "other",
] as const

export type SharedService = {
  id: string
  server_id: string
  server_name: string
  guest_vmid: string
  guest_type: "lxc" | "qemu"
  kind: string
  name: string
  host: string | null
  port: number | null
  hidden: boolean
  name_manual: boolean
  kind_manual: boolean
}

/** The team's directory of Proxmox guests classified as shared infrastructure. */
export default function SharedServicesPage() {
  const { t } = useI18n()
  const { team, manage } = useTeam()
  useDocumentTitle(`${t("nav.sharedServices")} — ${team.name}`)
  const errorText = useErrorText()
  const [services, setServices] = useState<SharedService[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [q, setQ] = useState("")
  const [scanning, setScanning] = useState(false)
  const [showHidden, setShowHidden] = useState(false)

  const load = useCallback(async () => {
    try {
      const r = await apiFetch<{ services: SharedService[] }>(`/api/teams/${team.slug}/shared-services`)
      setServices(r.services ?? [])
    } catch (err) {
      setError(errorText(err))
      setServices([])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [team.slug])

  useEffect(() => {
    void load()
  }, [load])

  async function scan() {
    setScanning(true)
    setError(null)
    try {
      const r = await apiFetch<{ services: SharedService[] }>(`/api/teams/${team.slug}/shared-services/scan`, { method: "POST" })
      setServices(r.services ?? [])
      toast.success(t("sharedServices.scanned", { count: (r.services ?? []).length }))
    } catch (err) {
      setError(errorText(err))
    } finally {
      setScanning(false)
    }
  }

  async function update(s: SharedService, patch: Partial<Pick<SharedService, "name" | "kind" | "hidden">>) {
    try {
      const updated = await apiFetch<SharedService>(`/api/teams/${team.slug}/shared-services/${s.id}`, { method: "PUT", body: patch })
      setServices((cur) => (cur ?? []).map((x) => (x.id === s.id ? updated : x)))
    } catch (err) {
      setError(errorText(err))
    }
  }

  const visible = (services ?? []).filter((s) => showHidden || !s.hidden)
  const shown = visible.filter((s) => matches(q, s.name, s.kind, s.server_name, s.host ?? ""))
  const grouped = shown.reduce<Record<string, SharedService[]>>((acc, s) => {
    ;(acc[s.kind] ??= []).push(s)
    return acc
  }, {})

  return (
    <PageBody
      title={t("nav.sharedServices")}
      description={t("sharedServices.description")}
      actions={
        manage ? (
          <Button variant="primary" size="sm" onClick={() => void scan()} disabled={scanning}>
            <RefreshCwIcon className={scanning ? "animate-spin" : undefined} />
            {t("sharedServices.scan")}
          </Button>
        ) : null
      }
      bleed
    >
      <ErrorBand message={error} />
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-border px-6 py-3">
        <ListSearch value={q} onChange={setQ} placeholder={t("sharedServices.search")} shown={shown.length} total={visible.length} />
        <label className="flex items-center gap-2 text-xs text-muted-foreground">
          <Checkbox checked={showHidden} onCheckedChange={(v) => setShowHidden(!!v)} />
          {t("sharedServices.showHidden")}
        </label>
      </div>
      {services === null ? (
        <LoadingRows />
      ) : visible.length === 0 ? (
        <EmptyState className="border-b" icon={DatabaseIcon} title={t("sharedServices.emptyTitle")} description={t("sharedServices.emptyHint")} />
      ) : (
        <div className="grid grid-cols-1 gap-4 px-6 py-4 sm:grid-cols-2 lg:grid-cols-3">
          {Object.entries(grouped).map(([kind, list]) => (
            <div key={kind} className="col-span-full">
              <h3 className="mb-2 mt-4 text-xs font-semibold uppercase tracking-wide text-muted-foreground first:mt-0">
                {t(`sharedServices.kinds.${kind}`, undefined, kind)}
              </h3>
              <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
                {list.map((s) => (
                  <SharedServiceCard key={s.id} service={s} manage={manage} onUpdate={(patch) => update(s, patch)} />
                ))}
              </div>
            </div>
          ))}
        </div>
      )}
    </PageBody>
  )
}

function SharedServiceCard({
  service: s,
  manage,
  onUpdate,
}: {
  service: SharedService
  manage: boolean
  onUpdate: (patch: Partial<Pick<SharedService, "name" | "kind" | "hidden">>) => Promise<void>
}) {
  const { t } = useI18n()
  const [name, setName] = useState(s.name)
  useEffect(() => setName(s.name), [s.name])

  return (
    <div className="flex flex-col gap-2 rounded-md border border-border bg-card p-4">
      <div className="flex items-center justify-between gap-2">
        <div className="flex items-center gap-2 text-sm text-muted-foreground">
          <ServerIcon className="size-3.5" />
          {s.server_name} · CT {s.guest_vmid}
        </div>
        {s.hidden ? <Badge variant="outline">{t("sharedServices.hidden")}</Badge> : null}
      </div>
      {manage ? (
        <Input
          value={name}
          onChange={(e) => setName(e.target.value)}
          onBlur={() => {
            if (name.trim() && name !== s.name) void onUpdate({ name: name.trim() })
          }}
          className="font-medium"
        />
      ) : (
        <p className="font-medium">{s.name}</p>
      )}
      {manage ? (
        <Select value={s.kind} onValueChange={(v) => v && void onUpdate({ kind: v })}>
          <SelectTrigger className="w-full">
            <SelectValue>{(v: string) => t(`sharedServices.kinds.${v}`, undefined, v)}</SelectValue>
          </SelectTrigger>
          <SelectContent>
            {SHARED_SERVICE_KINDS.map((k) => (
              <SelectItem key={k} value={k}>
                {t(`sharedServices.kinds.${k}`, undefined, k)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      ) : (
        <Badge variant="neutral" className="w-fit">
          {t(`sharedServices.kinds.${s.kind}`, undefined, s.kind)}
        </Badge>
      )}
      <p dir="ltr" className="font-mono text-xs text-muted-foreground">
        {s.host ?? "—"}
        {s.port ? `:${s.port}` : ""}
      </p>
      {manage ? (
        <div className="mt-1 flex justify-end">
          <ConfirmButton
            label={s.hidden ? t("sharedServices.unhide") : t("sharedServices.hide")}
            icon={<CircleOffIcon />}
            title={s.hidden ? t("sharedServices.unhide") : t("sharedServices.hide")}
            description={s.hidden ? t("sharedServices.unhideConfirm", { name: s.name }) : t("sharedServices.hideConfirm", { name: s.name })}
            confirmLabel={s.hidden ? t("sharedServices.unhide") : t("sharedServices.hide")}
            onConfirm={() => onUpdate({ hidden: !s.hidden })}
          />
        </div>
      ) : null}
    </div>
  )
}
