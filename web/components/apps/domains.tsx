"use client"

import { useCallback, useEffect, useState, type FormEvent } from "react"
import { CheckCircle2Icon, GlobeIcon, InfoIcon, ShieldCheckIcon } from "lucide-react"

import { ConfirmButton } from "@/components/confirm-button"
import { CopyField } from "@/components/copy-field"
import { useDialogOpen, type Controlled } from "@/components/dialog-state"
import { FormError, useErrorText } from "@/components/form-error"
import { ErrorBand, LoadingRows, Notice, SectionTitle, SettingsRow } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia } from "@/components/ui/empty"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { apiFetch } from "@/lib/api"
import type { Domain, Integrations, TLSMode } from "@/lib/apps"
import { useFormatDate, useI18n } from "@/lib/i18n"

/**
 * A compact list-column rendering of an app's domains: the first two hostnames, then a "+N" chip
 * whose hover card (and keyboard focus — base-ui's Tooltip opens on focus too) lists the rest.
 */
export function DomainsSummary({ domains }: { domains: Pick<Domain, "id" | "hostname">[] }) {
  const { t } = useI18n()
  if (domains.length === 0) return <>—</>
  const shown = domains.slice(0, 2)
  const rest = domains.slice(2)
  return (
    <div className="flex flex-col items-start gap-1">
      {shown.map((d) => (
        <div key={d.id}>{d.hostname}</div>
      ))}
      {rest.length > 0 ? (
        <Tooltip>
          <TooltipTrigger
            render={
              <button
                type="button"
                dir="ltr"
                aria-label={t("apps.domains.moreCount", { count: rest.length })}
                className="rounded-full bg-grid-soft px-1.5 py-0.5 text-[11px] font-medium text-grid-muted hover:bg-grid-soft/80 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
              />
            }
          >
            +{rest.length}
          </TooltipTrigger>
          <TooltipContent side="top" align="start" className="flex flex-col items-start gap-1">
            {rest.map((d) => (
              <span key={d.id} dir="ltr">
                {d.hostname}
              </span>
            ))}
          </TooltipContent>
        </Tooltip>
      ) : null}
    </div>
  )
}

const TLS: TLSMode[] = ["auto", "internal", "off"]

/** How an imported workload's hostname reaches it (see DomainsPanel). */
export type DomainPort = { required: boolean; suggested?: number }

/** A hostname Hosbah accepts (the API checks again). */
export function hostnameOK(h: string) {
  return /^(?=.{3,253}$)([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,}$/i.test(h.trim())
}

/** Route a hostname to the app. */
export function addDomain(path: string, hostname: string, tls: TLSMode, port?: number) {
  return apiFetch<Domain>(`${path}/domains`, {
    method: "POST",
    body: { hostname: hostname.trim().toLowerCase(), tls, ...(port ? { port } : {}) },
  })
}

/** Stop routing a hostname to the app (plain confirmation). Also opened from the overview canvas. */
export function RemoveDomain({ path, domain: d, onRemoved, ...controlled }: { path: string; domain: Domain; onRemoved: () => Promise<void> | void } & Controlled) {
  const { t } = useI18n()
  const errorText = useErrorText()
  return (
    <ConfirmButton
      {...controlled}
      label={t("apps.domains.remove")}
      title={t("apps.domains.removeTitle")}
      description={t("apps.domains.removeHint", { host: d.hostname })}
      confirmLabel={t("apps.domains.remove")}
      onConfirm={async () => {
        try {
          await apiFetch(`${path}/domains/${d.id}`, { method: "DELETE" })
        } catch (err) {
          throw new Error(errorText(err))
        }
        await onRemoved()
      }}
    />
  )
}

/** The add-a-domain form as a dialog, opened from the overview canvas (the proxy page has it inline). */
export function AddDomainDialog({ path, port, onSaved, ...controlled }: { path: string; port?: DomainPort; onSaved: () => Promise<void> | void } & Controlled) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [hostname, setHostname] = useState("")
  const [tls, setTls] = useState<TLSMode>("auto")
  const [portValue, setPortValue] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const { open, setOpen } = useDialogOpen(controlled, () => {
    setHostname("")
    setTls("auto")
    setPortValue(port?.suggested ? String(port.suggested) : "")
    setError(null)
  })
  const ok = hostnameOK(hostname)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await addDomain(path, hostname, tls, port && portValue ? Number(portValue) : undefined)
      setOpen(false)
      await onSaved()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogContent>
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{t("apps.domains.addTitle")}</DialogTitle>
            <DialogDescription>{t("apps.domains.dialogHint")}</DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="cdom-host">{t("apps.domains.hostname")}</FieldLabel>
              <Input
                id="cdom-host"
                dir="ltr"
                required
                maxLength={253}
                placeholder="app.example.com"
                value={hostname}
                onChange={(e) => setHostname(e.target.value)}
                aria-invalid={hostname !== "" && !ok}
                autoComplete="off"
              />
            </Field>
            {port ? (
              <Field>
                <FieldLabel htmlFor="cdom-port">{t("controls.domainPort")}</FieldLabel>
                <Input
                  id="cdom-port"
                  type="number"
                  min={1}
                  max={65535}
                  dir="ltr"
                  className="w-32 font-mono"
                  required={port.required}
                  value={portValue}
                  onChange={(e) => setPortValue(e.target.value)}
                />
                <FieldDescription>{t(port.required ? "controls.domainPortGuest" : "controls.domainPortContainer")}</FieldDescription>
              </Field>
            ) : null}
            <Field>
              <FieldLabel>{t("apps.domains.tlsLabel")}</FieldLabel>
              <Select value={tls} onValueChange={(v) => v && setTls(v as TLSMode)}>
                <SelectTrigger className="w-full" aria-label={t("apps.domains.tlsLabel")}>
                  <SelectValue>{(v: string) => t(`apps.domains.tls.${v}`)}</SelectValue>
                </SelectTrigger>
                <SelectContent>
                  {TLS.map((m) => (
                    <SelectItem key={m} value={m}>
                      {t(`apps.domains.tls.${m}`)}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <FieldDescription>{t(`apps.domains.tlsHint.${tls}`)}</FieldDescription>
            </Field>
          </FieldGroup>
          <FormError message={error} />
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={busy || !ok || (!!port?.required && !portValue)}>
              {busy ? t("common.working") : t("apps.domains.add")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

/**
 * `port`: an imported workload's hostname is routed to one of its ports — required for a guest
 * (Hosbah cannot know which port it serves), optional for a container (default: its first published).
 */
export function DomainsPanel({
  team,
  path,
  manage,
  port,
}: {
  team: string
  path: string
  manage: boolean
  port?: DomainPort
}) {
  const { t } = useI18n()
  const fmt = useFormatDate()
  const errorText = useErrorText()
  const [domains, setDomains] = useState<Domain[] | null>(null)
  const [appsDomain, setAppsDomain] = useState<boolean | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [hostname, setHostname] = useState("")
  const [tls, setTls] = useState<TLSMode>("auto")
  const [portValue, setPortValue] = useState(port?.suggested ? String(port.suggested) : "")
  const [busy, setBusy] = useState(false)
  const [messages, setMessages] = useState<Record<string, string>>({})

  const load = useCallback(async () => {
    try {
      const r = await apiFetch<{ domains: Domain[] }>(`${path}/domains`)
      setDomains(r.domains ?? [])
    } catch (err) {
      setError(errorText(err))
      setDomains((d) => d ?? [])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path])

  useEffect(() => {
    void load()
    apiFetch<Integrations>(`/api/teams/${team}/integrations`)
      .then((r) => setAppsDomain(r.apps_domain.configured))
      .catch(() => setAppsDomain(null))
  }, [load, team])

  const hostOK = hostnameOK(hostname)

  async function add(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await addDomain(path, hostname, tls, port && portValue ? Number(portValue) : undefined)
      setHostname("")
      await load()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  async function verify(d: Domain) {
    setError(null)
    try {
      const r = await apiFetch<{ verified: boolean; message: string; domain: Domain }>(`${path}/domains/${d.id}/verify`, { method: "POST" })
      setMessages((m) => ({ ...m, [d.id]: r.message }))
      setDomains((list) => list?.map((x) => (x.id === d.id ? r.domain : x)) ?? list)
    } catch (err) {
      setError(errorText(err))
    }
  }

  return (
    <div data-testid="domains">
      {appsDomain === false ? <Notice icon={<InfoIcon />}>{t("apps.domains.noAppsDomain")}</Notice> : null}
      <ErrorBand message={error} />
      {domains === null ? (
        <LoadingRows rows={2} />
      ) : domains.length === 0 ? (
        <Empty className="border-b">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <GlobeIcon />
            </EmptyMedia>
            <EmptyDescription>{t("apps.domains.empty")}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <ul className="flex flex-col divide-y divide-line border-b border-line">
          {domains.map((d) => (
            <li key={d.id} className="flex flex-col gap-3 px-6 py-4" data-domain={d.hostname}>
              <div className="flex flex-wrap items-center gap-2">
                <a
                  dir="ltr"
                  href={`${d.tls === "off" ? "http" : "https"}://${d.hostname}`}
                  target="_blank"
                  rel="noreferrer"
                  className="font-mono text-sm font-medium underline-offset-4 hover:underline"
                >
                  {d.hostname}
                </a>
                <Badge variant="outline">{t(`apps.domains.tls.${d.tls}`)}</Badge>
                {d.upstream ? (
                  <span dir="ltr" className="font-mono text-xs text-grid-muted">
                    → {d.upstream}
                  </span>
                ) : null}
                {d.verified ? (
                  <Badge>
                    <CheckCircle2Icon />
                    {t("apps.domains.verified")}
                  </Badge>
                ) : (
                  <Badge variant="secondary">{t("apps.domains.unverified")}</Badge>
                )}
                {manage ? (
                  <span className="ms-auto flex gap-2">
                    {!d.verified ? (
                      <Button size="sm" variant="outline" onClick={() => verify(d)}>
                        <ShieldCheckIcon />
                        {t("apps.domains.verify")}
                      </Button>
                    ) : null}
                    <RemoveDomain path={path} domain={d} onRemoved={load} />
                  </span>
                ) : null}
              </div>
              {d.verified ? (
                <p className="text-xs text-muted-foreground">
                  {t("apps.domains.verifiedAt")} <span dir="ltr">{fmt(d.verified_at)}</span>
                </p>
              ) : (
                <div className="flex flex-col gap-2 text-sm">
                  <p className="text-muted-foreground">{t("apps.domains.txtHint")}</p>
                  <div className="grid gap-2 sm:grid-cols-[auto_1fr] sm:items-center">
                    <span className="text-xs text-muted-foreground">{t("apps.domains.txtName")}</span>
                    <CopyField value={d.verify_name} />
                    <span className="text-xs text-muted-foreground">{t("apps.domains.txtValue")}</span>
                    <CopyField value={d.verify_value} />
                  </div>
                </div>
              )}
              {messages[d.id] ? (
                <p dir="auto" role="status" className="text-xs text-muted-foreground">
                  {messages[d.id]}
                </p>
              ) : null}
            </li>
          ))}
        </ul>
      )}

      {manage ? (
        <form onSubmit={add}>
          <SectionTitle title={t("apps.domains.addTitle")} />
          <SettingsRow label={t("apps.domains.hostname")} htmlFor="dom-host">
            <Input
              id="dom-host"
              dir="ltr"
              required
              maxLength={253}
              placeholder="app.example.com"
              value={hostname}
              onChange={(e) => setHostname(e.target.value)}
              aria-invalid={hostname !== "" && !hostOK}
              autoComplete="off"
            />
          </SettingsRow>
          {port ? (
            <SettingsRow label={t("controls.domainPort")} htmlFor="dom-port" hint={t(port.required ? "controls.domainPortGuest" : "controls.domainPortContainer")}>
              <Input
                id="dom-port"
                type="number"
                min={1}
                max={65535}
                dir="ltr"
                className="w-32 font-mono"
                required={port.required}
                value={portValue}
                onChange={(e) => setPortValue(e.target.value)}
              />
            </SettingsRow>
          ) : null}
          <SettingsRow
            label={t("apps.domains.tlsLabel")}
            htmlFor="dom-tls"
            hint={
              <span className="flex flex-col gap-1">
                {TLS.map((m) => (
                  <span key={m}>
                    <span className="font-medium text-grid-fg">{t(`apps.domains.tls.${m}`)}</span> — {t(`apps.domains.tlsHint.${m}`)}
                  </span>
                ))}
              </span>
            }
          >
            <Select value={tls} onValueChange={(v) => v && setTls(v as TLSMode)}>
              <SelectTrigger id="dom-tls" className="w-full">
                <SelectValue>{(v: string) => t(`apps.domains.tls.${v}`)}</SelectValue>
              </SelectTrigger>
              <SelectContent>
                {TLS.map((m) => (
                  <SelectItem key={m} value={m}>
                    {t(`apps.domains.tls.${m}`)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </SettingsRow>
          <div className="flex justify-end border-b border-line px-6 py-4">
            <Button type="submit" disabled={busy || !hostOK || (!!port?.required && !portValue)}>
              {busy ? t("common.working") : t("apps.domains.add")}
            </Button>
          </div>
        </form>
      ) : (
        <p className="border-b border-line px-6 py-4 text-sm text-grid-muted">{t("common.readOnly")}</p>
      )}
    </div>
  )
}
