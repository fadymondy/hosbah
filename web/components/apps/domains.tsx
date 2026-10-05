"use client"

import { Badge, Button, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, EmptyState, Field, FieldDescription, FieldLabel, Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, Tooltip } from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useState, type FormEvent } from "react"
import { CheckCircle2Icon, ExternalLinkIcon, GlobeIcon, InfoIcon, PlusIcon, ShieldCheckIcon, Trash2Icon } from "lucide-react"

import { ConfirmButton } from "@/components/confirm-button"
import { CopyField } from "@/components/copy-field"
import { useDialogOpen, type Controlled } from "@/components/dialog-state"
import { FormError, useErrorText } from "@/components/form-error"
import { LoadMore, useInfiniteLimit } from "@/components/lists/infinite"
import { RowMenu, RowWithActions, type RowAction } from "@/components/lists/row-actions"
import { ErrorBand, LoadingRows, Notice, SectionTitle } from "@/components/states"







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
        <Tooltip side="top" content={<div className="flex flex-col items-start gap-1">
            {rest.map((d) => (
              <span key={d.id} dir="ltr">
                {d.hostname}
              </span>
            ))}
          </div>}>
<button
                type="button"
                dir="ltr"
                aria-label={t("apps.domains.moreCount", { count: rest.length })}
                className="rounded-full bg-secondary px-1.5 py-0.5 text-[11px] font-medium text-muted-foreground hover:bg-secondary/80 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary">+{rest.length}</button>
</Tooltip>
      ) : null}
    </div>
  )
}

const TLS: TLSMode[] = ["auto", "internal", "off"]

/** How an imported workload's hostname reaches it (see DomainsPanel). */
export type DomainPort = { required: boolean; suggested?: number }

/** A hostname Hosbah accepts (the API checks again): exact, or "*.example.com" for every subdomain. */
export function hostnameOK(h: string) {
  return /^(?=.{3,253}$)(\*\.)?([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,}$/i.test(h.trim())
}

/** A wildcard is served over tls off or internal only (a public certificate for it needs a DNS
 * challenge): typing one moves the form off auto. */
function useWildcardTLS(hostname: string, tls: TLSMode, setTls: (m: TLSMode) => void) {
  const wildcard = hostname.trim().startsWith("*.")
  useEffect(() => {
    if (wildcard && tls === "auto") setTls("off")
  }, [wildcard, tls, setTls])
  return wildcard
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

/** The add-a-domain form as a dialog, opened from the proxy page and the architecture canvas. */
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
  const wildcard = useWildcardTLS(hostname, tls, setTls)

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
          <div className="flex w-full flex-col gap-5">
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
                    <SelectItem key={m} value={m} disabled={wildcard && m === "auto"}>
                      {t(`apps.domains.tls.${m}`)}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <FieldDescription>{t(`apps.domains.tlsHint.${tls}`)}</FieldDescription>
              {wildcard ? <FieldDescription>{t("apps.domains.wildcardHint")}</FieldDescription> : null}
            </Field>
          </div>
          <FormError message={error} />
          <DialogFooter>
            <Button type="button" variant="secondary" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button variant="primary" type="submit" disabled={busy || !ok || (!!port?.required && !portValue)}>
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
  title,
  hint,
  titleId,
}: {
  team: string
  path: string
  manage: boolean
  port?: DomainPort
  /** The section heading; the add button sits at its end. */
  title: string
  hint?: string
  titleId?: string
}) {
  const { t } = useI18n()
  const fmt = useFormatDate()
  const errorText = useErrorText()
  const [domains, setDomains] = useState<Domain[] | null>(null)
  const [appsDomain, setAppsDomain] = useState<boolean | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [adding, setAdding] = useState(false)
  const [messages, setMessages] = useState<Record<string, string>>({})
  const [removing, setRemoving] = useState<Domain | null>(null)
  const page = useInfiniteLimit(domains?.length ?? 0)

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

  const domainActions = (d: Domain): RowAction[] => [
    {
      id: "open",
      label: t("apps.domains.open"),
      icon: ExternalLinkIcon,
      onSelect: () => window.open(`${d.tls === "off" ? "http" : "https"}://${d.hostname}`, "_blank", "noopener"),
    },
    ...(manage
      ? [
          ...(!d.verified && !d.hostname.startsWith("*.")
            ? [{ id: "verify", label: t("apps.domains.verify"), icon: ShieldCheckIcon, onSelect: () => void verify(d) }]
            : []),
          { id: "remove", label: t("apps.domains.remove"), icon: Trash2Icon, danger: true, group: "danger", onSelect: () => setRemoving(d) },
        ]
      : []),
  ]

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
    <div data-testid="domains" data-stack>
      <SectionTitle
        id={titleId}
        title={title}
        hint={hint}
        action={
          manage ? (
            <Button size="sm" variant="primary" onClick={() => setAdding(true)}>
              <PlusIcon />
              {t("apps.domains.addTitle")}
            </Button>
          ) : null
        }
      />
      {appsDomain === false ? <Notice icon={<InfoIcon />}>{t("apps.domains.noAppsDomain")}</Notice> : null}
      <ErrorBand message={error} />
      {domains === null ? (
        <LoadingRows rows={2} />
      ) : domains.length === 0 ? (
        <EmptyState className="rounded-card border border-dashed border-border" icon={GlobeIcon} title={t("apps.domains.empty")} />
      ) : (
        <ul className="flex flex-col divide-y divide-border overflow-hidden rounded-card border border-border bg-card shadow-xs">
          {domains.slice(0, page.limit).map((d) => (
            <RowWithActions key={d.id} actions={domainActions(d)} render={<li className="flex flex-col gap-3 px-4 py-4 sm:px-5" data-domain={d.hostname} />}>
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
                  <span dir="ltr" className="font-mono text-xs text-muted-foreground">
                    → {d.upstream}
                  </span>
                ) : null}
                {d.hostname.startsWith("*.") ? (
                  <Badge variant="neutral">{t("apps.domains.wildcard")}</Badge>
                ) : d.verified ? (
                  <Badge variant="brand">
                    <CheckCircle2Icon />
                    {t("apps.domains.verified")}
                  </Badge>
                ) : (
                  <Badge variant="neutral">{t("apps.domains.unverified")}</Badge>
                )}
                <span className="ms-auto">
                  <RowMenu actions={domainActions(d)} label={d.hostname} />
                </span>
              </div>
              {d.hostname.startsWith("*.") ? (
                <p className="text-xs text-muted-foreground">{t("apps.domains.wildcardHint")}</p>
              ) : d.verified ? (
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
            </RowWithActions>
          ))}
        </ul>
      )}
      <LoadMore hasMore={page.hasMore} onMore={page.more} />
      {removing ? <RemoveDomain open onOpenChange={(v) => !v && setRemoving(null)} path={path} domain={removing} onRemoved={load} /> : null}

      {manage ? <AddDomainDialog path={path} port={port} open={adding} onOpenChange={setAdding} onSaved={load} /> : null}
    </div>
  )
}
