"use client"

import { Badge, Button, Checkbox, Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle, EmptyState, Field, FieldLabel, Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, type DataTableColumn, toast } from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useState, type FormEvent } from "react"
import { PencilIcon, PlusIcon, ScrollTextIcon, Trash2Icon } from "lucide-react"

import { ConfirmButton } from "@/components/confirm-button"
import { FormError, useErrorText } from "@/components/form-error"
import { NasaqTable } from "@/components/lists/nasaq-table"
import { ErrorBand, LoadingRows, SectionTitle } from "@/components/states"









import { apiFetch } from "@/lib/api"
import type { App } from "@/lib/apps"
import { useI18n } from "@/lib/i18n"

type Kind = "syslog" | "http" | "loki"

type Syslog = { transport: "tcp+tls" | "udp"; host: string; port: number }
type HTTPCfg = { url: string; auth_header?: string }
type LokiCfg = { url: string; auth_header?: string; labels?: Record<string, string> }

type LogDrain = {
  id: string
  team_id: string
  app_id: string
  name: string
  kind: Kind
  internal: boolean
  enabled: boolean
  has_auth_value: boolean
  syslog?: Syslog
  http?: HTTPCfg
  loki?: LokiCfg
  shipped: number
  dropped: number
  last_success_at: string | null
  last_error_at: string | null
  last_error?: string
  created_at: string
  updated_at: string
}

type DrainTestResult = { ok: boolean; detail?: string }

function labelsToText(labels?: Record<string, string>): string {
  return Object.entries(labels ?? {})
    .map(([k, v]) => `${k}: ${v}`)
    .join("\n")
}

function textToLabels(text: string): Record<string, string> | undefined {
  const out: Record<string, string> = {}
  for (const raw of text.split("\n")) {
    const line = raw.trim()
    if (!line) continue
    const i = line.indexOf(":")
    if (i < 0) continue
    const k = line.slice(0, i).trim()
    const v = line.slice(i + 1).trim()
    if (k) out[k] = v
  }
  return Object.keys(out).length ? out : undefined
}

function when(at: string | null, t: (k: string) => string) {
  if (!at) return t("settings.logDrains.never")
  const d = new Date(at)
  return Number.isNaN(d.getTime()) ? at : d.toLocaleString()
}

/**
 * H8: team log drains — syslog, http or loki, scoped to one app or every app. Read: any member.
 * Add, change, delete and test: owner or admin (log drains can carry secrets and point Hosbah's
 * containers' logs at an outside collector).
 */
export function LogDrainsCard({ team, manage }: { team: string; manage: boolean }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [rows, setRows] = useState<LogDrain[] | null>(null)
  const [apps, setApps] = useState<App[]>([])
  const [error, setError] = useState<string | null>(null)
  const [editing, setEditing] = useState<LogDrain | "new" | null>(null)
  const [testing, setTesting] = useState<Record<string, DrainTestResult | "busy">>({})

  const load = useCallback(async () => {
    try {
      const r = await apiFetch<{ log_drains: LogDrain[] }>(`/api/teams/${team}/log-drains`)
      setRows(r.log_drains ?? [])
      setError(null)
    } catch (err) {
      setError(errorText(err))
      setRows([])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [team])

  useEffect(() => {
    load()
    if (manage) {
      apiFetch<{ apps: App[] }>(`/api/teams/${team}/apps`)
        .then((r) => setApps(r.apps ?? []))
        .catch(() => setApps([]))
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [team, manage])

  async function remove(d: LogDrain) {
    await apiFetch(`/api/teams/${team}/log-drains/${d.id}`, { method: "DELETE" })
    await load()
  }

  async function test(d: LogDrain) {
    setTesting((s) => ({ ...s, [d.id]: "busy" }))
    try {
      const res = await apiFetch<DrainTestResult>(`/api/teams/${team}/log-drains/${d.id}/test`, { method: "POST" })
      setTesting((s) => ({ ...s, [d.id]: res }))
      toast[res.ok ? "success" : "error"](res.ok ? t("settings.logDrains.testOk") : (res.detail ?? t("settings.logDrains.testFailed")))
    } catch (err) {
      setTesting((s) => ({ ...s, [d.id]: { ok: false, detail: errorText(err) } }))
      toast.error(errorText(err))
    }
  }

  return (
    <section aria-labelledby="log-drains-h" data-testid="log-drains">
      <SectionTitle
        id="log-drains-h"
        title={t("settings.logDrains.title")}
        hint={t("settings.logDrains.hint")}
        action={
          manage ? (
            <Button variant="primary" size="sm" onClick={() => setEditing("new")}>
              <PlusIcon />
              {t("settings.logDrains.add")}
            </Button>
          ) : undefined
        }
      />
      <ErrorBand message={error} />
      {rows === null ? (
        error ? null : <LoadingRows rows={3} />
      ) : rows.length === 0 ? (
        <EmptyState className="border-b" icon={ScrollTextIcon} title={t("settings.logDrains.empty")} />
      ) : (
        <div className="px-6 py-6">
          <NasaqTable
            data={rows}
            rowId={(d) => d.id}
            label={t("settings.logDrains.title")}
            rowLabel={(d) => d.name}
            search={t("settings.logDrains.name")}
            empty={t("home.noMatch")}
            columns={[
              {
                id: "name",
                header: t("settings.logDrains.name"),
                label: t("settings.logDrains.name"),
                sortValue: (d) => d.name,
                searchValue: (d) => `${d.name} ${d.kind}`,
                cell: (d) => (
                  <span className="flex items-center gap-2">
                    <span dir="auto" className="font-medium">
                      {d.name}
                    </span>
                    {!d.enabled ? <Badge variant="neutral">{t("settings.logDrains.disabled")}</Badge> : null}
                  </span>
                ),
              },
              { id: "kind", header: t("settings.logDrains.kind"), label: t("settings.logDrains.kind"), sortValue: (d) => d.kind, cell: (d) => t(`settings.logDrains.kinds.${d.kind}`) },
              {
                id: "scope",
                header: t("settings.logDrains.scope"),
                label: t("settings.logDrains.scope"),
                className: "max-md:hidden",
                headerClassName: "max-md:hidden",
                cell: (d) => (
                  <span dir="auto" className="block max-w-40 truncate text-caption text-muted-foreground">
                    {d.app_id ? (apps.find((a) => a.id === d.app_id)?.name ?? d.app_id) : t("settings.logDrains.scopeAll")}
                  </span>
                ),
              },
              {
                id: "status",
                header: t("settings.logDrains.status"),
                label: t("settings.logDrains.status"),
                cell: (d) => {
                  const res = testing[d.id]
                  return (
                    <div className="flex flex-col gap-0.5 text-caption">
                      <span dir="ltr" className="tabular-nums text-muted-foreground">
                        {t("settings.logDrains.shipped")}: {d.shipped} · {t("settings.logDrains.dropped")}: {d.dropped}
                      </span>
                      <span className="text-muted-foreground">
                        {t("settings.logDrains.lastSuccess")}: {when(d.last_success_at, t)}
                      </span>
                      {d.last_error ? (
                        <span dir="auto" className="text-nq-danger-text">
                          {d.last_error}
                        </span>
                      ) : null}
                      {res && res !== "busy" ? (
                        <Badge variant={res.ok ? "brand" : "danger"} className="w-fit">
                          {res.ok ? t("settings.logDrains.testOk") : (res.detail ?? t("settings.logDrains.testFailed"))}
                        </Badge>
                      ) : null}
                    </div>
                  )
                },
              },
              {
                id: "actions",
                header: <span className="sr-only">{t("common.actions")}</span>,
                label: t("common.actions"),
                cell: (d) =>
                  manage ? (
                    <div className="flex flex-wrap justify-end gap-2">
                      <Button size="sm" variant="secondary" disabled={testing[d.id] === "busy"} onClick={() => test(d)}>
                        {testing[d.id] === "busy" ? t("common.working") : t("settings.logDrains.test")}
                      </Button>
                      <Button size="sm" variant="secondary" onClick={() => setEditing(d)}>
                        <PencilIcon />
                      </Button>
                      <ConfirmButton
                        icon={<Trash2Icon />}
                        label={t("settings.logDrains.delete")}
                        title={t("settings.logDrains.delete")}
                        description={t("settings.logDrains.deleteConfirm", { name: d.name })}
                        confirmLabel={t("settings.logDrains.delete")}
                        onConfirm={() => remove(d)}
                      />
                    </div>
                  ) : null,
              },
            ] satisfies DataTableColumn<LogDrain>[]}
          />
        </div>
      )}
      {editing ? (
        <LogDrainDialog
          team={team}
          apps={apps}
          drain={editing === "new" ? null : editing}
          onClose={() => setEditing(null)}
          onSaved={async () => {
            setEditing(null)
            await load()
          }}
        />
      ) : null}
    </section>
  )
}

function LogDrainDialog({
  team,
  apps,
  drain,
  onClose,
  onSaved,
}: {
  team: string
  apps: App[]
  drain: LogDrain | null
  onClose: () => void
  onSaved: () => Promise<void>
}) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [kind, setKind] = useState<Kind>(drain?.kind ?? "syslog")
  const [name, setName] = useState(drain?.name ?? "")
  const [appId, setAppId] = useState(drain?.app_id ?? "")
  const [enabled, setEnabled] = useState(drain?.enabled ?? true)
  const [internal, setInternal] = useState(drain?.internal ?? false)
  const [transport, setTransport] = useState<Syslog["transport"]>(drain?.syslog?.transport ?? "tcp+tls")
  const [host, setHost] = useState(drain?.syslog?.host ?? "")
  const [port, setPort] = useState(String(drain?.syslog?.port ?? (drain?.kind === "syslog" ? 514 : "")))
  const [url, setUrl] = useState(drain?.http?.url ?? drain?.loki?.url ?? "")
  const [authHeader, setAuthHeader] = useState(drain?.http?.auth_header ?? drain?.loki?.auth_header ?? "")
  const [authValue, setAuthValue] = useState("")
  const [labelsText, setLabelsText] = useState(labelsToText(drain?.loki?.labels))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const body: Record<string, unknown> = { name: name.trim(), app_id: appId, enabled, internal }
      if (kind === "syslog") body.syslog = { transport, host: host.trim(), port: Number(port) || 0 }
      if (kind === "http") body.http = { url: url.trim(), auth_header: authHeader.trim() }
      if (kind === "loki") body.loki = { url: url.trim(), auth_header: authHeader.trim(), labels: textToLabels(labelsText) }
      if (authValue.trim()) body.auth_value = authValue.trim()
      if (drain) {
        await apiFetch(`/api/teams/${team}/log-drains/${drain.id}`, { method: "PATCH", body })
      } else {
        await apiFetch(`/api/teams/${team}/log-drains`, { method: "POST", body: { kind, ...body } })
      }
      await onSaved()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="max-h-[90dvh] max-w-lg overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{drain ? t("settings.logDrains.edit") : t("settings.logDrains.add")}</DialogTitle>
        </DialogHeader>
        <form onSubmit={submit} className="flex flex-col gap-4">
          <FormError message={error} />
          <Field>
            <FieldLabel htmlFor="ld-name">{t("settings.logDrains.name")}</FieldLabel>
            <Input id="ld-name" required maxLength={80} dir="auto" value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          <div className="grid grid-cols-2 gap-3">
            <Field>
              <FieldLabel htmlFor="ld-kind">{t("settings.logDrains.kind")}</FieldLabel>
              <Select value={kind} onValueChange={(v) => v && setKind(v as Kind)} disabled={!!drain}>
                <SelectTrigger id="ld-kind" className="w-full">
                  <SelectValue>{(v: string) => <span>{t(`settings.logDrains.kinds.${v}`)}</span>}</SelectValue>
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="syslog">{t("settings.logDrains.kinds.syslog")}</SelectItem>
                  <SelectItem value="http">{t("settings.logDrains.kinds.http")}</SelectItem>
                  <SelectItem value="loki">{t("settings.logDrains.kinds.loki")}</SelectItem>
                </SelectContent>
              </Select>
            </Field>
            <Field>
              <FieldLabel htmlFor="ld-scope">{t("settings.logDrains.scope")}</FieldLabel>
              <Select value={appId || "all"} onValueChange={(v) => setAppId(v === "all" ? "" : (v as string))}>
                <SelectTrigger id="ld-scope" className="w-full">
                  <SelectValue>{(v: string) => <span dir="auto">{v === "all" ? t("settings.logDrains.scopeAll") : (apps.find((a) => a.id === v)?.name ?? v)}</span>}</SelectValue>
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="all">{t("settings.logDrains.scopeAll")}</SelectItem>
                  {apps.map((a) => (
                    <SelectItem key={a.id} value={a.id}>
                      <span dir="ltr">{a.name}</span>
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
          </div>

          {kind === "syslog" ? (
            <div className="grid grid-cols-3 gap-3">
              <Field>
                <FieldLabel htmlFor="ld-transport">{t("settings.logDrains.syslogTransport")}</FieldLabel>
                <Select value={transport} onValueChange={(v) => v && setTransport(v as Syslog["transport"])}>
                  <SelectTrigger id="ld-transport" className="w-full">
                    <SelectValue>{(v: string) => <span dir="ltr">{v}</span>}</SelectValue>
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="tcp+tls">tcp+tls</SelectItem>
                    <SelectItem value="udp">udp</SelectItem>
                  </SelectContent>
                </Select>
              </Field>
              <Field className="col-span-2">
                <FieldLabel htmlFor="ld-host">{t("settings.logDrains.syslogHost")}</FieldLabel>
                <Input id="ld-host" dir="ltr" required maxLength={253} value={host} onChange={(e) => setHost(e.target.value)} />
              </Field>
              <Field>
                <FieldLabel htmlFor="ld-port">{t("settings.logDrains.syslogPort")}</FieldLabel>
                <Input id="ld-port" dir="ltr" type="number" min={1} max={65535} required value={port} onChange={(e) => setPort(e.target.value)} />
              </Field>
            </div>
          ) : (
            <>
              <Field>
                <FieldLabel htmlFor="ld-url">{t("settings.logDrains.url")}</FieldLabel>
                <Input id="ld-url" dir="ltr" required maxLength={500} value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://…" />
              </Field>
              <Field>
                <FieldLabel htmlFor="ld-auth-header">{t("settings.logDrains.authHeader")}</FieldLabel>
                <Input id="ld-auth-header" dir="ltr" maxLength={64} value={authHeader} onChange={(e) => setAuthHeader(e.target.value)} placeholder="Authorization" />
              </Field>
              {kind === "loki" ? (
                <Field>
                  <FieldLabel htmlFor="ld-labels">{t("settings.logDrains.labels")}</FieldLabel>
                  <textarea
                    id="ld-labels"
                    dir="ltr"
                    rows={3}
                    className="w-full rounded-lg border border-input bg-transparent p-2 font-mono text-xs outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50"
                    value={labelsText}
                    onChange={(e) => setLabelsText(e.target.value)}
                    placeholder="env: prod"
                  />
                  <p className="text-xs text-muted-foreground">{t("settings.logDrains.labelsHint")}</p>
                </Field>
              ) : null}
            </>
          )}

          <Field>
            <FieldLabel htmlFor="ld-auth-value">{t("settings.logDrains.authValue")}</FieldLabel>
            <Input id="ld-auth-value" dir="ltr" type="password" maxLength={4000} value={authValue} onChange={(e) => setAuthValue(e.target.value)} />
            <p className="text-xs text-muted-foreground">{drain?.has_auth_value ? t("settings.logDrains.authValueSet") + " " : ""}{t("settings.logDrains.authValueHint")}</p>
          </Field>

          <div className="flex flex-wrap items-center gap-4">
            <label className="flex items-center gap-2 text-sm">
              <Checkbox checked={enabled} onCheckedChange={(v) => setEnabled(v === true)} />
              {t("settings.logDrains.enabled")}
            </label>
            {kind !== "syslog" ? (
              <label className="flex items-center gap-2 text-sm">
                <Checkbox checked={internal} onCheckedChange={(v) => setInternal(v === true)} />
                {t("settings.logDrains.internal")}
              </label>
            ) : null}
          </div>
          {kind !== "syslog" && internal ? <p className="text-xs text-muted-foreground">{t("settings.logDrains.internalHint")}</p> : null}

          <DialogFooter>
            <Button type="button" variant="secondary" onClick={onClose}>
              {t("common.cancel")}
            </Button>
            <Button variant="primary" type="submit" disabled={busy}>
              {busy ? t("common.working") : drain ? t("settings.logDrains.save") : t("settings.logDrains.create")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
