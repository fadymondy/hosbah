"use client"

import { Badge, Button, Checkbox, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger, EmptyState, Field, FieldDescription, FieldLabel, Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, type DataTableColumn, toast } from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useState, type FormEvent } from "react"
import { BellIcon, PencilIcon, PlayIcon, PlusIcon, Trash2Icon } from "lucide-react"

import { ConfirmButton } from "@/components/confirm-button"
import { useDialogOpen, type Controlled } from "@/components/dialog-state"
import { FormError, useErrorText } from "@/components/form-error"








import { NasaqTable } from "@/components/lists/nasaq-table"
import { ErrorBand, LoadingRows, SectionTitle } from "@/components/states"

import { apiFetch } from "@/lib/api"
import { useI18n } from "@/lib/i18n"

export type ChannelKind = "email" | "slack" | "discord" | "webhook"
export type NotificationChannel = {
  id: string
  kind: ChannelKind
  name: string
  emails: string[]
  url_hint: string
  secret_set: boolean
  events: string[]
  enabled: boolean
}

const KINDS: ChannelKind[] = ["email", "slack", "discord", "webhook"]
const EVENTS = ["deploy.failed", "server.offline", "backup.failed"]

/**
 * Team settings "Alerts & channels" (MH-575 H2): destinations a monitor's incidents — and later
 * deploy/server/backup events — can reach beyond a monitor's own email/webhook. Owner or admin
 * manage; any member reads (matching internal/api/notification_channels.go).
 */
export function NotificationChannelsSection({ team, manage }: { team: string; manage: boolean }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [channels, setChannels] = useState<NotificationChannel[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      const r = await apiFetch<{ channels: NotificationChannel[] }>(`/api/teams/${team}/notification-channels`)
      setChannels(r.channels ?? [])
      setError(null)
    } catch (err) {
      setError(errorText(err))
      setChannels((x) => x ?? [])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [team])

  useEffect(() => {
    void load()
  }, [load])

  async function test(c: NotificationChannel) {
    setBusy(c.id)
    setError(null)
    try {
      await apiFetch(`/api/teams/${team}/notification-channels/${c.id}/test`, { method: "POST" })
      toast.success(t("alerts.testSent"))
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(null)
    }
  }

  const add = manage ? <ChannelDialog team={team} onSaved={load} /> : null

  return (
    <section aria-labelledby="alerts-h">
      <SectionTitle id="alerts-h" title={t("settings.alertsTitle")} hint={t("settings.alertsHint")} action={add} />
      <ErrorBand message={error} />
      {channels === null ? (
        <LoadingRows rows={2} />
      ) : channels.length === 0 ? (
        <EmptyState className="border-b" icon={BellIcon} title={t("alerts.emptyTitle")} description={t("alerts.emptyHint")} actions={<>{add}</>} />
      ) : (
        <div className="px-6 py-6">
          <NasaqTable
            data={channels}
            rowId={(c) => c.id}
            label={t("settings.alertsTitle")}
            rowLabel={(c) => c.name}
            search={t("alerts.search")}
            empty={t("home.noMatch")}
            facets={[{ column: "kind", title: t("alerts.kind"), options: [...new Set(channels.map((c) => c.kind))].map((k) => ({ value: k, label: t(`alerts.kinds.${k}`) })) }]}
            columns={[
              {
                id: "name",
                header: t("alerts.name"),
                label: t("alerts.name"),
                sortValue: (c) => c.name,
                searchValue: (c) => [c.name, c.kind, c.url_hint, ...c.events].join(" "),
                cell: (c) => (
                  <span className="flex items-center gap-2">
                    <span dir="auto" className="font-medium">
                      {c.name}
                    </span>
                    {!c.enabled ? <Badge variant="outline">{t("site.monitors.paused")}</Badge> : null}
                  </span>
                ),
              },
              { id: "kind", header: t("alerts.kind"), label: t("alerts.kind"), sortValue: (c) => c.kind, filterValue: (c) => c.kind, cell: (c) => t(`alerts.kinds.${c.kind}`) },
              {
                id: "destination",
                header: t("alerts.destination"),
                label: t("alerts.destination"),
                className: "max-md:hidden",
                headerClassName: "max-md:hidden",
                cell: (c) => (
                  <span dir="ltr" className="block max-w-56 truncate font-mono text-caption text-muted-foreground">
                    {c.kind === "email" ? c.emails.join(", ") : c.url_hint}
                  </span>
                ),
              },
              {
                id: "events",
                header: t("alerts.events"),
                label: t("alerts.events"),
                className: "max-lg:hidden",
                headerClassName: "max-lg:hidden",
                cell: (c) => <span className="text-caption text-muted-foreground">{c.events.length ? c.events.map((e) => t(`alerts.eventNames.${e}`)).join(", ") : "—"}</span>,
              },
              {
                id: "actions",
                header: <span className="sr-only">{t("common.actions")}</span>,
                label: t("common.actions"),
                cell: (c) =>
                  manage ? (
                    <div className="flex flex-wrap justify-end gap-2">
                      <Button size="sm" variant="secondary" disabled={busy === c.id} onClick={() => void test(c)}>
                        <PlayIcon />
                        {t("alerts.test")}
                      </Button>
                      <ChannelDialog team={team} channel={c} onSaved={load} />
                      <DeleteChannel team={team} channel={c} onDeleted={load} />
                    </div>
                  ) : null,
              },
            ] satisfies DataTableColumn<NotificationChannel>[]}
          />
        </div>
      )}
    </section>
  )
}

function DeleteChannel({
  team,
  channel: c,
  onDeleted,
  ...controlled
}: { team: string; channel: NotificationChannel; onDeleted: () => Promise<void> | void } & Controlled) {
  const { t } = useI18n()
  const errorText = useErrorText()
  return (
    <ConfirmButton
      {...controlled}
      icon={<Trash2Icon />}
      label={t("common.delete")}
      title={t("alerts.deleteTitle", { name: c.name })}
      description={t("alerts.deleteBody")}
      confirmLabel={t("common.delete")}
      onConfirm={async () => {
        try {
          await apiFetch(`/api/teams/${team}/notification-channels/${c.id}`, { method: "DELETE" })
        } catch (err) {
          throw new Error(errorText(err))
        }
        await onDeleted()
      }}
    />
  )
}

function ChannelDialog({
  team,
  channel,
  onSaved,
  ...controlled
}: { team: string; channel?: NotificationChannel; onSaved: () => Promise<void> | void } & Controlled) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [name, setName] = useState("")
  const [kind, setKind] = useState<ChannelKind>("email")
  const [emails, setEmails] = useState("")
  const [url, setUrl] = useState("")
  const [secret, setSecret] = useState("")
  const [clearSecret, setClearSecret] = useState(false)
  const [events, setEvents] = useState<string[]>([])
  const [enabled, setEnabled] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const { open, setOpen, controlled: fromCaller } = useDialogOpen(controlled, reset)

  function reset() {
    setError(null)
    setName(channel?.name ?? "")
    setKind(channel?.kind ?? "email")
    setEmails((channel?.emails ?? []).join(", "))
    setUrl("")
    setSecret("")
    setClearSecret(false)
    setEvents(channel?.events ?? [])
    setEnabled(channel?.enabled ?? true)
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    const email = kind === "email"
    const body: Record<string, unknown> = {
      name: name.trim(),
      ...(channel ? {} : { kind }),
      events,
      enabled,
    }
    if (email) body.emails = emails.split(/[,\s]+/).filter(Boolean)
    else if (url.trim() || !channel) body.url = url.trim()
    if (kind === "webhook") {
      if (secret.trim()) body.secret = secret.trim()
      if (channel && clearSecret) body.clear_secret = true
    }
    try {
      await apiFetch(channel ? `/api/teams/${team}/notification-channels/${channel.id}` : `/api/teams/${team}/notification-channels`, {
        method: channel ? "PATCH" : "POST",
        body,
      })
      setOpen(false)
      await onSaved()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  const email = kind === "email"
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      {fromCaller ? null : (
        <DialogTrigger render={<Button size="sm" variant={channel ? "secondary" : "primary"} />}>
          {channel ? <PencilIcon /> : <PlusIcon />}
          {channel ? t("lists.edit") : t("alerts.add")}
        </DialogTrigger>
      )}
      <DialogContent className="max-h-[90dvh] overflow-y-auto">
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{channel ? channel.name : t("alerts.add")}</DialogTitle>
            <DialogDescription>{t("alerts.emptyHint")}</DialogDescription>
          </DialogHeader>
          <div className="flex w-full flex-col gap-5">
            <Field>
              <FieldLabel htmlFor="ch-name">{t("alerts.name")}</FieldLabel>
              <Input id="ch-name" dir="auto" required maxLength={80} value={name} onChange={(e) => setName(e.target.value)} />
            </Field>
            <Field>
              <FieldLabel>{t("alerts.kind")}</FieldLabel>
              <Select value={kind} onValueChange={(v) => v && setKind(v as ChannelKind)} disabled={!!channel}>
                <SelectTrigger aria-label={t("alerts.kind")}>
                  <SelectValue>{(v: string) => t(`alerts.kinds.${v}`)}</SelectValue>
                </SelectTrigger>
                <SelectContent>
                  {KINDS.map((k) => (
                    <SelectItem key={k} value={k}>
                      {t(`alerts.kinds.${k}`)}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            {email ? (
              <Field>
                <FieldLabel htmlFor="ch-emails">{t("alerts.emails")}</FieldLabel>
                <Input id="ch-emails" dir="ltr" placeholder="ops@example.com, me@example.com" value={emails} onChange={(e) => setEmails(e.target.value)} />
              </Field>
            ) : (
              <>
                <Field>
                  <FieldLabel htmlFor="ch-url">{t("alerts.url")}</FieldLabel>
                  <Input
                    id="ch-url"
                    dir="ltr"
                    type="url"
                    required={!channel}
                    placeholder={channel ? channel.url_hint : "https://…"}
                    value={url}
                    onChange={(e) => setUrl(e.target.value)}
                  />
                  <FieldDescription>{t("alerts.urlHint")}</FieldDescription>
                </Field>
                {kind === "webhook" ? (
                  <Field>
                    <FieldLabel htmlFor="ch-secret">{t("alerts.secret")}</FieldLabel>
                    <Input
                      id="ch-secret"
                      dir="ltr"
                      type="password"
                      placeholder={channel?.secret_set ? t("alerts.secretSet") : ""}
                      value={secret}
                      onChange={(e) => setSecret(e.target.value)}
                    />
                    <FieldDescription>{t("alerts.secretHint")}</FieldDescription>
                    {channel?.secret_set ? (
                      <label className="flex items-center gap-2 text-sm">
                        <Checkbox checked={clearSecret} onCheckedChange={(v) => setClearSecret(v === true)} />
                        {t("alerts.clearSecret")}
                      </label>
                    ) : null}
                  </Field>
                ) : null}
              </>
            )}
            <Field>
              <FieldLabel>{t("alerts.events")}</FieldLabel>
              <div className="flex flex-col gap-2">
                {EVENTS.map((ev) => (
                  <label key={ev} className="flex items-center gap-2 text-sm">
                    <Checkbox
                      checked={events.includes(ev)}
                      onCheckedChange={(v) => setEvents((cur) => (v === true ? [...cur, ev] : cur.filter((x) => x !== ev)))}
                    />
                    {t(`alerts.eventNames.${ev}`)}
                  </label>
                ))}
              </div>
            </Field>
            <label className="flex items-center gap-2 text-sm">
              <Checkbox checked={enabled} onCheckedChange={(v) => setEnabled(v === true)} />
              {t("alerts.enabled")}
            </label>
          </div>
          <FormError message={error} />
          <DialogFooter>
            <Button type="button" variant="secondary" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button variant="primary" type="submit" disabled={busy || !name.trim()}>
              {busy ? t("common.working") : t("common.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
