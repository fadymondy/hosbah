"use client"

import { useCallback, useEffect, useState, type FormEvent, type ReactNode } from "react"
import {
  ActivityIcon,
  BellRingIcon,
  CpuIcon,
  GlobeIcon,
  HardDriveIcon,
  HeartPulseIcon,
  MemoryStickIcon,
  PencilIcon,
  PlayIcon,
  PlusIcon,
  Trash2Icon,
} from "lucide-react"
import { toast } from "sonner"

import { ConfirmButton } from "@/components/confirm-button"
import { CopyField } from "@/components/copy-field"
import { useDialogOpen, type Controlled } from "@/components/dialog-state"
import { FormError, useErrorText } from "@/components/form-error"
import { useTimeAgo } from "@/components/home/sparkline"
import type { NotificationChannel } from "@/components/notification-channels"
import { ListSearch, matches } from "@/components/server-tabs/list-tools"
import { useLoadedSite } from "@/components/site/context"
import { ErrorBand, LoadingRows, SectionTitle, TableBand } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { apiFetch } from "@/lib/api"
import { useFormatDate, useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

type Kind = "http" | "cpu" | "memory" | "disk" | "heartbeat"
export type Monitor = {
  id: string
  name: string
  kind: Kind
  target: string
  expected_status: number
  threshold: number
  window_minutes: number
  interval_seconds: number
  enabled: boolean
  notify_emails: string[]
  notify_webhook: string
  notify_channel_ids: string[]
  heartbeat_interval_seconds: number
  heartbeat_grace_seconds: number
  ping_url_path?: string
  last_ping_at: string | null
  state: "unknown" | "ok" | "failing"
  last_checked_at: string | null
  last_error: string
  open_incident: { id: string; opened_at: string; reason: string } | null
}
type Incident = {
  id: string
  monitor: { id: string; name: string; kind: Kind }
  opened_at: string
  resolved_at: string | null
  reason: string
  last_value: string
}
type Uptime = { last_24h: number; last_7d: number; last_30d: number }

const KINDS: Kind[] = ["http", "cpu", "memory", "disk", "heartbeat"]
const KIND_ICON: Record<Kind, ReactNode> = {
  http: <GlobeIcon />,
  cpu: <CpuIcon />,
  memory: <MemoryStickIcon />,
  disk: <HardDriveIcon />,
  heartbeat: <HeartPulseIcon />,
}

/**
 * Alerts & monitors: uptime checks on the site's own hostnames and rules on its server's CPU,
 * memory and disk. A failing monitor opens an incident and tells people by email or webhook; the
 * incident closes itself when the check passes again.
 */
export function SiteMonitors() {
  const { t, locale } = useI18n()
  const { manage } = useTeam()
  const ago = useTimeAgo(locale)
  const fmt = useFormatDate()
  const errorText = useErrorText()
  const { detail, path } = useLoadedSite()
  const [monitors, setMonitors] = useState<Monitor[] | null>(null)
  const [incidents, setIncidents] = useState<Incident[]>([])
  const [cursor, setCursor] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState<string | null>(null)
  const [q, setQ] = useState("")
  const [pingURL, setPingURL] = useState<{ name: string; url: string } | null>(null)

  const load = useCallback(async () => {
    try {
      const [m, i] = await Promise.all([
        apiFetch<{ monitors: Monitor[] }>(`${path}/monitors`),
        apiFetch<{ incidents: Incident[]; next_cursor?: string | null }>(`${path}/incidents?limit=20`),
      ])
      setMonitors(m.monitors ?? [])
      setIncidents(i.incidents ?? [])
      setCursor(i.next_cursor ?? null)
      setError(null)
    } catch (err) {
      setError(errorText(err))
      setMonitors((x) => x ?? [])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path])

  useEffect(() => {
    void load()
    const id = setInterval(load, 20_000)
    return () => clearInterval(id)
  }, [load])

  async function more() {
    if (!cursor) return
    try {
      const i = await apiFetch<{ incidents: Incident[]; next_cursor?: string | null }>(`${path}/incidents?limit=20&before=${encodeURIComponent(cursor)}`)
      setIncidents((cur) => [...cur, ...(i.incidents ?? [])])
      setCursor(i.next_cursor ?? null)
    } catch (err) {
      setError(errorText(err))
    }
  }

  async function check(m: Monitor) {
    setBusy(m.id)
    setError(null)
    try {
      const next = await apiFetch<Monitor>(`${path}/monitors/${m.id}/check`, { method: "POST" })
      setMonitors((cur) => (cur ?? []).map((x) => (x.id === m.id ? next : x)))
      toast.success(t(`site.monitors.state.${next.state}`))
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(null)
    }
  }

  const hosts = detail.domains.map((d) => `${d.tls === "off" ? "http" : "https"}://${d.hostname}`)
  const shown = (monitors ?? []).filter((m) => matches(q, m.name, m.kind, m.target, m.state))

  async function saved(created?: Monitor) {
    if (created?.ping_url_path && typeof window !== "undefined") {
      setPingURL({ name: created.name, url: window.location.origin + created.ping_url_path })
    }
    await load()
  }

  const add = manage ? <MonitorDialog path={path} hosts={hosts} onSaved={saved} /> : null

  return (
    <>
      <section aria-labelledby="mon-h">
        <SectionTitle id="mon-h" title={t("site.nav.monitors")} hint={t("site.planned.monitors.hint")} action={add} />
        <ErrorBand message={error} />
        {pingURL ? (
          <div className="border-b border-line px-6 py-4">
            <p dir="auto" className="mb-2 text-sm font-medium">
              {t("site.monitors.pingUrlTitle")} — {pingURL.name}
            </p>
            <p className="mb-2 text-xs text-grid-muted">{t("site.monitors.pingUrlHint")}</p>
            <CopyField value={pingURL.url} />
            <p className="mt-2 text-xs text-grid-muted">{t("site.monitors.pingUrlCurl")}</p>
            <CopyField block value={`curl ${pingURL.url}`} className="mt-1" />
            <Button size="sm" variant="ghost" className="mt-2" onClick={() => setPingURL(null)}>
              {t("common.done")}
            </Button>
          </div>
        ) : null}
        {monitors === null ? (
          <LoadingRows rows={3} />
        ) : monitors.length === 0 ? (
          <Empty className="border-b">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <BellRingIcon />
              </EmptyMedia>
              <EmptyTitle>{t("site.monitors.emptyTitle")}</EmptyTitle>
              <EmptyDescription>{t("site.monitors.emptyHint")}</EmptyDescription>
            </EmptyHeader>
            {add}
          </Empty>
        ) : (
          <>
            <ListSearch value={q} onChange={setQ} placeholder={t("site.monitors.search")} shown={shown.length} total={monitors.length} />
            <TableBand>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t("site.monitors.name")}</TableHead>
                    <TableHead>{t("lists.status")}</TableHead>
                    <TableHead>{t("site.monitors.rule")}</TableHead>
                    <TableHead>{t("site.monitors.checked")}</TableHead>
                    <TableHead>{t("site.monitors.uptime24h")}</TableHead>
                    {manage ? <TableHead className="text-end">{t("common.actions")}</TableHead> : null}
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {shown.map((m) => (
                    <TableRow key={m.id}>
                      <TableCell>
                        <span className="inline-flex items-center gap-2 font-medium [&_svg]:size-4 [&_svg]:text-grid-brand">
                          {KIND_ICON[m.kind]}
                          <span dir="auto">{m.name}</span>
                        </span>
                        {m.kind === "http" ? (
                          <div dir="ltr" className="max-w-64 truncate text-start font-mono text-xs text-grid-muted">
                            {m.target}
                          </div>
                        ) : null}
                      </TableCell>
                      <TableCell>
                        {!m.enabled ? (
                          <Badge variant="outline">{t("site.monitors.paused")}</Badge>
                        ) : (
                          <Badge variant={m.state === "failing" ? "destructive" : m.state === "ok" ? "default" : "secondary"}>
                            {t(`site.monitors.state.${m.state}`)}
                          </Badge>
                        )}
                        {m.open_incident ? (
                          <p dir="auto" className="mt-1 max-w-xs text-xs text-grid-danger">
                            {m.open_incident.reason} · {ago(m.open_incident.opened_at)}
                          </p>
                        ) : m.last_error ? (
                          <p dir="auto" className="mt-1 max-w-xs text-xs text-grid-muted">
                            {m.last_error}
                          </p>
                        ) : null}
                      </TableCell>
                      <TableCell className="text-xs">
                        {m.kind === "http"
                          ? t("site.monitors.ruleHttp", { status: m.expected_status || "2xx/3xx", every: m.interval_seconds }) +
                            (m.threshold ? ` · ≤ ${m.threshold} ms` : "")
                          : m.kind === "heartbeat"
                            ? t("site.monitors.ruleHeartbeat", { every: m.heartbeat_interval_seconds, grace: m.heartbeat_grace_seconds })
                            : t("site.monitors.ruleResource", { kind: t(`site.monitors.kinds.${m.kind}`), pct: m.threshold, min: m.window_minutes })}
                      </TableCell>
                      <TableCell className="text-xs text-grid-muted">
                        {m.kind === "heartbeat"
                          ? m.last_ping_at
                            ? ago(m.last_ping_at)
                            : t("site.monitors.neverPinged")
                          : m.last_checked_at
                            ? ago(m.last_checked_at)
                            : "—"}
                      </TableCell>
                      <TableCell className="text-xs">
                        <UptimeBadge path={path} monitor={m} />
                      </TableCell>
                      {manage ? (
                        <TableCell>
                          <div className="flex flex-wrap justify-end gap-2">
                            {m.kind === "heartbeat" ? null : (
                              <Button size="sm" variant="outline" disabled={busy === m.id || !m.enabled} onClick={() => void check(m)}>
                                <PlayIcon />
                                {t("site.monitors.checkNow")}
                              </Button>
                            )}
                            <MonitorDialog path={path} hosts={hosts} monitor={m} onSaved={saved} />
                            <DeleteMonitor path={path} monitor={m} onDeleted={load} />
                          </div>
                        </TableCell>
                      ) : null}
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </TableBand>
          </>
        )}
      </section>

      <section aria-labelledby="inc-h">
        <SectionTitle id="inc-h" title={t("site.monitors.incidents")} hint={t("site.monitors.incidentsHint")} />
        {incidents.length === 0 ? (
          <p className="flex items-center gap-2 border-b border-line px-6 py-6 text-sm text-grid-muted">
            <ActivityIcon className="size-4" />
            {t("site.monitors.noIncidents")}
          </p>
        ) : (
          <ol className="flex flex-col divide-y divide-line border-b border-line">
            {incidents.map((i) => (
              <li key={i.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-6 py-3 text-sm">
                <Badge variant={i.resolved_at ? "secondary" : "destructive"}>{i.resolved_at ? t("site.monitors.resolved") : t("site.monitors.open")}</Badge>
                <span dir="auto" className="font-medium">
                  {i.monitor.name}
                </span>
                <span dir="auto" className="min-w-0 flex-1 truncate text-grid-muted">
                  {i.reason}
                  {i.last_value ? ` (${i.last_value})` : ""}
                </span>
                <span dir="ltr" className="text-xs text-grid-muted">
                  {fmt(i.opened_at)}
                  {i.resolved_at ? ` → ${fmt(i.resolved_at)}` : ""}
                </span>
              </li>
            ))}
          </ol>
        )}
        {cursor ? (
          <div className="flex justify-center border-b border-line px-6 py-3">
            <Button variant="ghost" size="sm" onClick={() => void more()}>
              {t("site.monitors.more")}
            </Button>
          </div>
        ) : null}
      </section>
    </>
  )
}

/** Delete a monitor (and its incidents). Also opened from the overview canvas. */
export function DeleteMonitor({ path, monitor: m, onDeleted, ...controlled }: { path: string; monitor: Monitor; onDeleted: () => Promise<void> | void } & Controlled) {
  const { t } = useI18n()
  const errorText = useErrorText()
  return (
    <ConfirmButton
      {...controlled}
      icon={<Trash2Icon />}
      label={t("common.delete")}
      title={t("site.monitors.deleteTitle", { name: m.name })}
      description={t("site.monitors.deleteBody")}
      confirmLabel={t("common.delete")}
      onConfirm={async () => {
        try {
          await apiFetch(`${path}/monitors/${m.id}`, { method: "DELETE" })
        } catch (err) {
          throw new Error(errorText(err))
        }
        await onDeleted()
      }}
    />
  )
}

/** A monitor's uptime over 24h/7d/30d, fetched once and shown as a compact badge with a tooltip. */
function UptimeBadge({ path, monitor: m }: { path: string; monitor: Monitor }) {
  const { t } = useI18n()
  const [uptime, setUptime] = useState<Uptime | null>(null)

  useEffect(() => {
    let cancelled = false
    apiFetch<Uptime>(`${path}/monitors/${m.id}/uptime`)
      .then((u) => {
        if (!cancelled) setUptime(u)
      })
      .catch(() => {
        if (!cancelled) setUptime(null)
      })
    return () => {
      cancelled = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path, m.id])

  if (!uptime) return <span className="text-grid-muted">—</span>
  const title = `${t("site.monitors.uptime7d")}: ${uptime.last_7d}% · ${t("site.monitors.uptime30d")}: ${uptime.last_30d}%`
  return (
    <Badge variant={uptime.last_24h >= 99.9 ? "default" : uptime.last_24h >= 95 ? "secondary" : "destructive"} title={title} dir="ltr">
      {uptime.last_24h}%
    </Badge>
  )
}

/**
 * Create or edit one monitor. hosts: the site's own URLs, offered as targets (the first is the
 * default). Also opened from the overview canvas.
 */
export function MonitorDialog({
  path,
  hosts,
  monitor,
  onSaved,
  ...controlled
}: { path: string; hosts: string[]; monitor?: Monitor; onSaved: (created?: Monitor) => Promise<void> | void } & Controlled) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [name, setName] = useState("")
  const [kind, setKind] = useState<Kind>("http")
  const [target, setTarget] = useState("")
  const [status, setStatus] = useState("200")
  const [threshold, setThreshold] = useState("")
  const [windowMin, setWindowMin] = useState("5")
  const [interval, setIntervalS] = useState("60")
  const [emails, setEmails] = useState("")
  const [webhook, setWebhook] = useState("")
  const [channelIds, setChannelIds] = useState<string[]>([])
  const [channels, setChannels] = useState<NotificationChannel[]>([])
  const [hbInterval, setHbInterval] = useState("300")
  const [hbGrace, setHbGrace] = useState("120")
  const [enabled, setEnabled] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const { team } = useTeam()
  const { open, setOpen, controlled: fromCaller } = useDialogOpen(controlled, reset)

  useEffect(() => {
    if (!open) return
    apiFetch<{ channels: NotificationChannel[] }>(`/api/teams/${team.slug}/notification-channels`)
      .then((r) => setChannels(r.channels ?? []))
      .catch(() => setChannels([]))
  }, [open, team.slug])

  function reset() {
    setError(null)
    setName(monitor?.name ?? "")
    setKind(monitor?.kind ?? "http")
    setTarget(monitor?.target || hosts[0] || "")
    setStatus(String(monitor?.expected_status ?? 200))
    setThreshold(monitor ? String(monitor.threshold || "") : "")
    setWindowMin(String(monitor?.window_minutes || 5))
    setIntervalS(String(monitor?.interval_seconds || 60))
    setEmails((monitor?.notify_emails ?? []).join(", "))
    setWebhook(monitor?.notify_webhook ?? "")
    setChannelIds(monitor?.notify_channel_ids ?? [])
    setHbInterval(String(monitor?.heartbeat_interval_seconds || 300))
    setHbGrace(String(monitor?.heartbeat_grace_seconds ?? 120))
    setEnabled(monitor?.enabled ?? true)
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    const http = kind === "http"
    const heartbeat = kind === "heartbeat"
    // Only what applies to the kind is sent; the kind itself is fixed once created.
    const body: Record<string, unknown> = {
      name: name.trim(),
      ...(monitor ? {} : { kind }),
      enabled,
      notify_emails: emails.split(/[,\s]+/).filter(Boolean),
      notify_webhook: webhook.trim(),
      notify_channel_ids: channelIds,
    }
    if (http) {
      body.target = target.trim()
      body.expected_status = Number(status) || 0
      body.threshold = Number(threshold) || 0
      body.interval_seconds = Number(interval) || 60
    } else if (heartbeat) {
      body.heartbeat_interval_seconds = Number(hbInterval) || 300
      body.heartbeat_grace_seconds = Number(hbGrace) || 0
    } else {
      body.window_minutes = Number(windowMin) || 5
      body.threshold = Number(threshold) || 0
      body.interval_seconds = Number(interval) || 60
    }
    try {
      const saved = await apiFetch<Monitor>(monitor ? `${path}/monitors/${monitor.id}` : `${path}/monitors`, {
        method: monitor ? "PATCH" : "POST",
        body,
      })
      setOpen(false)
      await onSaved(saved)
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  const http = kind === "http"
  const heartbeat = kind === "heartbeat"
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      {fromCaller ? null : (
        <DialogTrigger render={<Button size="sm" variant={monitor ? "outline" : "default"} />}>
          {monitor ? <PencilIcon /> : <PlusIcon />}
          {monitor ? t("lists.edit") : t("site.monitors.add")}
        </DialogTrigger>
      )}
      <DialogContent className="max-h-[90dvh] overflow-y-auto">
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{monitor ? monitor.name : t("site.monitors.add")}</DialogTitle>
            <DialogDescription>{t("site.monitors.dialogHint")}</DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="mon-name">{t("site.monitors.name")}</FieldLabel>
              <Input id="mon-name" dir="auto" required maxLength={80} value={name} onChange={(e) => setName(e.target.value)} />
            </Field>
            <Field>
              <FieldLabel>{t("lists.kind")}</FieldLabel>
              <Select value={kind} onValueChange={(v) => v && setKind(v as Kind)} disabled={!!monitor}>
                <SelectTrigger aria-label={t("lists.kind")}>
                  <SelectValue>{(v: string) => t(`site.monitors.kinds.${v}`)}</SelectValue>
                </SelectTrigger>
                <SelectContent>
                  {KINDS.map((k) => (
                    <SelectItem key={k} value={k}>
                      {t(`site.monitors.kinds.${k}`)}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            {http ? (
              <>
                <Field>
                  <FieldLabel htmlFor="mon-target">{t("site.monitors.url")}</FieldLabel>
                  <Input id="mon-target" dir="ltr" type="url" required list="mon-hosts" value={target} onChange={(e) => setTarget(e.target.value)} />
                  <datalist id="mon-hosts">
                    {hosts.map((h) => (
                      <option key={h} value={h} />
                    ))}
                  </datalist>
                  <FieldDescription>{hosts.length ? t("site.monitors.urlHint") : t("site.monitors.urlNoHosts")}</FieldDescription>
                </Field>
                <div className="grid gap-4 sm:grid-cols-2">
                  <Field>
                    <FieldLabel htmlFor="mon-status">{t("site.monitors.expected")}</FieldLabel>
                    <Input id="mon-status" dir="ltr" type="number" min={0} max={599} value={status} onChange={(e) => setStatus(e.target.value)} />
                    <FieldDescription>{t("site.monitors.expectedHint")}</FieldDescription>
                  </Field>
                  <Field>
                    <FieldLabel htmlFor="mon-ms">{t("site.monitors.maxMs")}</FieldLabel>
                    <Input id="mon-ms" dir="ltr" type="number" min={0} placeholder="0" value={threshold} onChange={(e) => setThreshold(e.target.value)} />
                  </Field>
                </div>
              </>
            ) : heartbeat ? (
              <div className="grid gap-4 sm:grid-cols-2">
                <Field>
                  <FieldLabel htmlFor="mon-hb-int">{t("site.monitors.heartbeatInterval")}</FieldLabel>
                  <Input id="mon-hb-int" dir="ltr" type="number" min={30} max={86400} required value={hbInterval} onChange={(e) => setHbInterval(e.target.value)} />
                </Field>
                <Field>
                  <FieldLabel htmlFor="mon-hb-grace">{t("site.monitors.heartbeatGrace")}</FieldLabel>
                  <Input id="mon-hb-grace" dir="ltr" type="number" min={0} max={86400} required value={hbGrace} onChange={(e) => setHbGrace(e.target.value)} />
                </Field>
              </div>
            ) : (
              <div className="grid gap-4 sm:grid-cols-2">
                <Field>
                  <FieldLabel htmlFor="mon-pct">{t("site.monitors.percent")}</FieldLabel>
                  <Input id="mon-pct" dir="ltr" type="number" min={1} max={100} required value={threshold} onChange={(e) => setThreshold(e.target.value)} />
                </Field>
                <Field>
                  <FieldLabel htmlFor="mon-win">{t("site.monitors.window")}</FieldLabel>
                  <Input id="mon-win" dir="ltr" type="number" min={1} max={60} required value={windowMin} onChange={(e) => setWindowMin(e.target.value)} />
                </Field>
              </div>
            )}
            {heartbeat ? null : (
              <Field>
                <FieldLabel htmlFor="mon-int">{t("site.monitors.interval")}</FieldLabel>
                <Input id="mon-int" dir="ltr" type="number" min={30} max={3600} required value={interval} onChange={(e) => setIntervalS(e.target.value)} />
              </Field>
            )}
            <Field>
              <FieldLabel htmlFor="mon-emails">{t("site.monitors.emails")}</FieldLabel>
              <Input id="mon-emails" dir="ltr" placeholder="ops@example.com, me@example.com" value={emails} onChange={(e) => setEmails(e.target.value)} />
            </Field>
            <Field>
              <FieldLabel htmlFor="mon-hook">{t("site.monitors.webhook")}</FieldLabel>
              <Input id="mon-hook" dir="ltr" type="url" placeholder="https://…" value={webhook} onChange={(e) => setWebhook(e.target.value)} />
              <FieldDescription>{t("site.monitors.webhookHint")}</FieldDescription>
            </Field>
            {channels.length ? (
              <Field>
                <FieldLabel>{t("site.monitors.channels")}</FieldLabel>
                <div className="flex flex-col gap-2">
                  {channels.map((c) => (
                    <label key={c.id} className="flex items-center gap-2 text-sm">
                      <Checkbox
                        checked={channelIds.includes(c.id)}
                        onCheckedChange={(v) => setChannelIds((cur) => (v === true ? [...cur, c.id] : cur.filter((x) => x !== c.id)))}
                      />
                      <span dir="auto">
                        {c.name} ({t(`alerts.kinds.${c.kind}`)})
                      </span>
                    </label>
                  ))}
                </div>
              </Field>
            ) : (
              <FieldDescription>{t("site.monitors.noChannels")}</FieldDescription>
            )}
            <label className="flex items-center gap-2 text-sm">
              <Checkbox checked={enabled} onCheckedChange={(v) => setEnabled(v === true)} />
              {t("site.monitors.enabled")}
            </label>
          </FieldGroup>
          <FormError message={error} />
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={busy || !name.trim()}>
              {busy ? t("common.working") : t("common.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
