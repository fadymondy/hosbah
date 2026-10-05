"use client"

import { Badge, Button, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger, EmptyState, Field, FieldDescription, FieldLabel, Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, Table, TableBody, TableCell, TableHead, TableHeader, TableRow, toast } from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useState, type FormEvent } from "react"
import {
  CheckCircle2Icon,
  KeyRoundIcon,
  LinkIcon,
  MailIcon,
  PlusIcon,
  RefreshCcwIcon,
  SearchCheckIcon,
  Trash2Icon,
  UploadCloudIcon,
} from "lucide-react"

import { ConfirmButton } from "@/components/confirm-button"
import { CopyField } from "@/components/copy-field"
import { useDialogOpen, type Controlled } from "@/components/dialog-state"
import { FormError, useErrorText } from "@/components/form-error"







import { NasaqTable } from "@/components/lists/nasaq-table"
import { ErrorBand, LoadingRows, SectionTitle } from "@/components/states"

import { apiFetch } from "@/lib/api"
import { useI18n } from "@/lib/i18n"
import {
  blacklistStatusVariant,
  dnsStatusVariant,
  mailStatusVariant,
  type DeliverabilityReport,
  type MailAlias,
  type MailDnsPublishResult,
  type MailDomain,
  type MailDomainDns,
  type MailRelay,
  type MailServer,
  type Mailbox,
} from "@/lib/mail"
import type { Server } from "@/lib/servers"
import { useTeam } from "@/lib/team"

/**
 * The team's mail servers (MH-577 I1-I3): install Stalwart on one of the team's own servers, add
 * domains to it, manage mailboxes and aliases, and check/publish the DNS records each domain
 * needs. Reading is any member; installing, adding and removing is owner or admin
 * (tenancy.AbilityManageMail, the same tier as manage_servers).
 */
export function TeamMail() {
  const { t } = useI18n()
  const { team, manage } = useTeam()
  const errorText = useErrorText()
  const base = `/api/teams/${team.slug}/mail`

  const [servers, setServers] = useState<MailServer[] | null>(null)
  const [hostServers, setHostServers] = useState<Server[] | null>(null)
  const [domains, setDomains] = useState<MailDomain[] | null>(null)
  const [apps, setApps] = useState<{ id: string; name: string }[]>([])
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      const [s, hs, d, a] = await Promise.all([
        apiFetch<{ servers: MailServer[] }>(`${base}/servers`),
        apiFetch<{ servers: Server[] }>(`/api/teams/${team.slug}/servers`),
        apiFetch<{ domains: MailDomain[] }>(`${base}/domains`),
        apiFetch<{ apps: { id: string; name: string }[] }>(`/api/teams/${team.slug}/apps`),
      ])
      setServers(s.servers ?? [])
      setHostServers(hs.servers ?? [])
      setDomains(d.domains ?? [])
      setApps(a.apps ?? [])
      setError(null)
    } catch (err) {
      setError(errorText(err))
      setServers((x) => x ?? [])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [base, team.slug])

  useEffect(() => {
    void load()
    const id = setInterval(load, 15_000)
    return () => clearInterval(id)
  }, [load])

  const enrolledServers = (hostServers ?? []).filter((s) => s.status === "enrolled")

  return (
    <>
      <section aria-labelledby="mail-servers-h">
        <SectionTitle
          id="mail-servers-h"
          title={t("mail.servers.title")}
          hint={t("mail.servers.hint")}
          action={manage && servers && servers.length > 0 ? <InstallDialog base={base} servers={enrolledServers} onSaved={load} /> : null}
        />
        <ErrorBand message={error} />
        {servers === null ? (
          <LoadingRows rows={2} />
        ) : servers.length === 0 ? (
          <EmptyState className="border-b" icon={MailIcon} title={t("mail.servers.emptyTitle")} description={t("mail.servers.emptyHint")} actions={<>{manage ? <InstallDialog base={base} servers={enrolledServers} onSaved={load} /> : null}</>} />
        ) : (
          <div className="px-6 py-4">
            <NasaqTable
              data={servers}
              rowId={(s) => s.id}
              label={t("mail.servers.title")}
              rowLabel={(s) => s.hostname}
              columns={[
                {
                  id: "hostname",
                  header: t("mail.servers.hostname"),
                  sortValue: (s) => s.hostname,
                  cell: (s) => (
                    <span dir="ltr" className="flex items-center gap-2 font-medium">
                      <MailIcon className="size-4 text-muted-foreground" />
                      {s.hostname}
                    </span>
                  ),
                },
                {
                  id: "server",
                  header: t("mail.servers.onServer"),
                  cell: (s) => (
                    <span dir="ltr" className="text-caption text-muted-foreground">
                      {enrolledServers.find((hs) => hs.id === s.server_id)?.name ?? s.server_id.slice(0, 8)}
                    </span>
                  ),
                },
                {
                  id: "status",
                  header: t("mail.statusLabel"),
                  cell: (s) => (
                    <span className="flex flex-col items-start gap-1">
                      <Badge variant={mailStatusVariant(s.status)}>{t(`mail.status.${s.status}`, undefined, s.status)}</Badge>
                      {s.error ? <span className="max-w-64 text-caption text-destructive">{s.error}</span> : null}
                    </span>
                  ),
                },
                ...(manage
                  ? [
                      {
                        id: "actions",
                        header: <span className="sr-only">{t("common.actions")}</span>,
                        align: "end" as const,
                        cell: (s: MailServer) => <DeleteServer base={base} server={s} onDeleted={load} />,
                      },
                    ]
                  : []),
              ]}
            />
          </div>
        )}
      </section>

      <section aria-labelledby="mail-domains-h">
        <SectionTitle
          id="mail-domains-h"
          title={t("mail.domains.title")}
          hint={t("mail.domains.hint")}
          action={manage && servers && servers.length > 0 ? <AddDomainDialog base={base} servers={servers} onSaved={load} /> : null}
        />
        {domains === null ? (
          <LoadingRows rows={2} />
        ) : domains.length === 0 ? (
          <p className="border-b border-border px-6 py-6 text-sm text-muted-foreground">
            {servers && servers.length === 0 ? t("mail.domains.needServer") : t("mail.domains.empty")}
          </p>
        ) : (
          <div className="flex flex-col">
            {domains.map((d) => (
              <DomainCard key={d.id} base={base} domain={d} manage={manage} apps={apps} onChanged={load} />
            ))}
          </div>
        )}
      </section>
    </>
  )
}

/** Install a Stalwart mail server on one of the team's own enrolled servers. */
function InstallDialog({ base, servers, onSaved, ...controlled }: { base: string; servers: Server[]; onSaved: () => Promise<void> | void } & Controlled) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [server, setServer] = useState("")
  const [hostname, setHostname] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const { open, setOpen, controlled: fromCaller } = useDialogOpen(controlled, reset)

  function reset() {
    setError(null)
    setServer(servers[0]?.id ?? "")
    setHostname("")
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await apiFetch(`${base}/servers`, { method: "POST", body: { server, hostname: hostname.trim() } })
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
      {fromCaller ? null : (
        <DialogTrigger render={<Button variant="primary" size="sm" />}>
          <PlusIcon />
          {t("mail.servers.install")}
        </DialogTrigger>
      )}
      <DialogContent>
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{t("mail.servers.install")}</DialogTitle>
            <DialogDescription>{t("mail.servers.installHint")}</DialogDescription>
          </DialogHeader>
          <div className="flex w-full flex-col gap-5">
            <Field>
              <FieldLabel htmlFor="mail-inst-server">{t("mail.servers.onServer")}</FieldLabel>
              {servers.length === 0 ? (
                <p className="text-sm text-muted-foreground">{t("mail.servers.noServers")}</p>
              ) : (
                <Select value={server} onValueChange={(v) => setServer(v ?? "")}>
                  <SelectTrigger id="mail-inst-server">
                    <SelectValue>{(v: string) => servers.find((s) => s.id === v)?.name ?? v}</SelectValue>
                  </SelectTrigger>
                  <SelectContent>
                    {servers.map((s) => (
                      <SelectItem key={s.id} value={s.id}>
                        {s.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              )}
            </Field>
            <Field>
              <FieldLabel htmlFor="mail-inst-host">{t("mail.servers.hostname")}</FieldLabel>
              <Input
                id="mail-inst-host"
                dir="ltr"
                required
                maxLength={253}
                placeholder="mail.example.com"
                value={hostname}
                onChange={(e) => setHostname(e.target.value)}
              />
              <FieldDescription>{t("mail.servers.hostnameHint")}</FieldDescription>
            </Field>
          </div>
          <FormError message={error} />
          <DialogFooter>
            <Button type="button" variant="secondary" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button variant="primary" type="submit" disabled={busy || !server || !hostname.trim()}>
              {busy ? t("common.working") : t("mail.servers.install")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function DeleteServer({ base, server, onDeleted }: { base: string; server: MailServer; onDeleted: () => Promise<void> | void }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  return (
    <ConfirmButton
      icon={<Trash2Icon />}
      label={t("common.delete")}
      title={t("mail.servers.deleteTitle", { name: server.hostname })}
      description={t("mail.servers.deleteBody")}
      confirmLabel={t("common.delete")}
      onConfirm={async () => {
        try {
          await apiFetch(`${base}/servers/${server.id}`, { method: "DELETE" })
        } catch (err) {
          throw new Error(errorText(err))
        }
        await onDeleted()
      }}
    />
  )
}

/** Add a domain to a mail server. */
function AddDomainDialog({ base, servers, onSaved, ...controlled }: { base: string; servers: MailServer[]; onSaved: () => Promise<void> | void } & Controlled) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [server, setServer] = useState("")
  const [domain, setDomain] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const { open, setOpen, controlled: fromCaller } = useDialogOpen(controlled, reset)

  function reset() {
    setError(null)
    setServer(servers[0]?.id ?? "")
    setDomain("")
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await apiFetch(`${base}/domains`, { method: "POST", body: { server, domain: domain.trim() } })
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
      {fromCaller ? null : (
        <DialogTrigger render={<Button variant="primary" size="sm" />}>
          <PlusIcon />
          {t("mail.domains.add")}
        </DialogTrigger>
      )}
      <DialogContent>
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{t("mail.domains.add")}</DialogTitle>
            <DialogDescription>{t("mail.domains.addHint")}</DialogDescription>
          </DialogHeader>
          <div className="flex w-full flex-col gap-5">
            <Field>
              <FieldLabel htmlFor="mail-dom-server">{t("mail.servers.title")}</FieldLabel>
              <Select value={server} onValueChange={(v) => setServer(v ?? "")}>
                <SelectTrigger id="mail-dom-server">
                  <SelectValue>{(v: string) => servers.find((s) => s.id === v)?.hostname ?? v}</SelectValue>
                </SelectTrigger>
                <SelectContent>
                  {servers.map((s) => (
                    <SelectItem key={s.id} value={s.id}>
                      {s.hostname}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            <Field>
              <FieldLabel htmlFor="mail-dom-name">{t("mail.domains.domain")}</FieldLabel>
              <Input id="mail-dom-name" dir="ltr" required maxLength={253} placeholder="example.com" value={domain} onChange={(e) => setDomain(e.target.value)} />
            </Field>
          </div>
          <FormError message={error} />
          <DialogFooter>
            <Button type="button" variant="secondary" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button variant="primary" type="submit" disabled={busy || !server || !domain.trim()}>
              {busy ? t("common.working") : t("mail.domains.add")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

/** One domain: its status, mailboxes, aliases, relay credentials and DNS records, each loaded lazily once expanded. */
function DomainCard({
  base,
  domain,
  manage,
  apps,
  onChanged,
}: {
  base: string
  domain: MailDomain
  manage: boolean
  apps: { id: string; name: string }[]
  onChanged: () => Promise<void> | void
}) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const domBase = `${base}/domains/${domain.id}`
  const [mailboxes, setMailboxes] = useState<Mailbox[] | null>(null)
  const [aliases, setAliases] = useState<MailAlias[] | null>(null)
  const [relays, setRelays] = useState<MailRelay[] | null>(null)
  const [dns, setDns] = useState<MailDomainDns | null>(null)
  const [publishResult, setPublishResult] = useState<MailDnsPublishResult | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState<"check" | "publish" | null>(null)
  const [report, setReport] = useState<DeliverabilityReport | null | "loading">(null)

  const load = useCallback(async () => {
    try {
      const [m, a, r, d] = await Promise.all([
        apiFetch<{ mailboxes: Mailbox[] }>(`${domBase}/mailboxes`),
        apiFetch<{ aliases: MailAlias[] }>(`${domBase}/aliases`),
        apiFetch<{ relays: MailRelay[] }>(`${domBase}/relays`),
        apiFetch<MailDomainDns>(`${domBase}/dns`),
      ])
      setMailboxes(m.mailboxes ?? [])
      setAliases(a.aliases ?? [])
      setRelays(r.relays ?? [])
      setDns(d)
      setError(null)
    } catch (err) {
      setError(errorText(err))
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [domBase])

  useEffect(() => {
    void load()
  }, [load])

  async function deleteDomain() {
    try {
      await apiFetch(`${base}/domains/${domain.domain}`, { method: "DELETE" })
    } catch (err) {
      throw new Error(errorText(err))
    }
    await onChanged()
  }

  async function checkDns() {
    setBusy("check")
    try {
      const d = await apiFetch<MailDomainDns>(`${domBase}/dns/check`, { method: "POST" })
      setDns(d)
      setPublishResult(null)
    } catch (err) {
      toast.error(errorText(err))
    } finally {
      setBusy(null)
    }
  }

  async function publishDns() {
    setBusy("publish")
    try {
      const r = await apiFetch<MailDnsPublishResult>(`${domBase}/dns/publish`, { method: "POST" })
      setPublishResult(r)
      toast.success(t("mail.dns.published"))
      await checkDns()
    } catch (err) {
      toast.error(errorText(err))
    } finally {
      setBusy(null)
    }
  }

  async function runDeliverability() {
    setReport("loading")
    try {
      const r = await apiFetch<DeliverabilityReport>(`${domBase}/deliverability`)
      setReport(r)
    } catch (err) {
      toast.error(errorText(err))
      setReport(null)
    }
  }

  return (
    <div className="border-b border-border">
      <div className="flex flex-wrap items-center justify-between gap-3 px-6 py-4">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <span dir="ltr" className="font-medium text-foreground">
              {domain.domain}
            </span>
            <Badge variant={mailStatusVariant(domain.status)}>{t(`mail.status.${domain.status}`, undefined, domain.status)}</Badge>
          </div>
          {domain.error ? <p className="mt-1 text-xs text-nq-danger-text">{domain.error}</p> : null}
        </div>
        <div className="flex flex-wrap gap-2">
          <Button size="sm" variant="secondary" disabled={busy !== null} onClick={() => void checkDns()}>
            <RefreshCcwIcon />
            {busy === "check" ? t("common.working") : t("mail.dns.check")}
          </Button>
          <Button size="sm" variant="secondary" disabled={report === "loading"} onClick={() => void runDeliverability()}>
            <SearchCheckIcon />
            {report === "loading" ? t("common.working") : t("mail.deliverability.run")}
          </Button>
          {manage ? (
            <Button size="sm" variant="secondary" disabled={busy !== null} onClick={() => void publishDns()}>
              <UploadCloudIcon />
              {busy === "publish" ? t("common.working") : t("mail.dns.publish")}
            </Button>
          ) : null}
          {manage ? (
            <ConfirmButton
              icon={<Trash2Icon />}
              label={t("common.delete")}
              title={t("mail.domains.deleteTitle", { name: domain.domain })}
              description={t("mail.domains.deleteBody")}
              confirmLabel={t("common.delete")}
              onConfirm={deleteDomain}
            />
          ) : null}
        </div>
      </div>
      <ErrorBand message={error} />

      {report && report !== "loading" ? <DeliverabilityDialog report={report} onClose={() => setReport(null)} /> : null}

      {publishResult ? (
        <div className="mx-6 mb-4 flex items-start gap-3 border border-border bg-secondary px-4 py-3 text-sm">
          <CheckCircle2Icon className="mt-0.5 size-4 shrink-0 text-nq-brand" />
          <div>
            <p className="font-medium text-foreground">{t("mail.dns.publishedTitle")}</p>
            <p dir="ltr" className="mt-1 text-xs text-muted-foreground">
              {publishResult.published.join(", ") || "—"}
            </p>
          </div>
        </div>
      ) : null}

      {/* DNS records */}
      <div className="px-6 pb-4">
        <p className="mb-2 text-xs font-medium tracking-wide text-muted-foreground uppercase">{t("mail.dns.records")}</p>
        {dns === null ? (
          <LoadingRows rows={2} />
        ) : dns.records.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t("mail.dns.noRecords")}</p>
        ) : (
          <div className="flex flex-col gap-2">
            {dns.records.map((r, i) => (
              <div key={`${r.type}-${r.name}-${i}`} className="flex flex-wrap items-center gap-2">
                <Badge variant="outline" dir="ltr">
                  {r.type}
                </Badge>
                <Badge variant={dnsStatusVariant(r.status)}>{t(`mail.dns.status.${r.status}`, undefined, r.status)}</Badge>
                <span dir="ltr" className="shrink-0 text-xs text-muted-foreground">
                  {r.name}
                </span>
                <CopyField value={r.value} className="min-w-0 flex-1" />
              </div>
            ))}
            <div className="flex flex-wrap items-center gap-2">
              <Badge variant="outline" dir="ltr">
                PTR
              </Badge>
              <Badge variant={dnsStatusVariant(dns.ptr.status)}>{t(`mail.dns.status.${dns.ptr.status}`, undefined, dns.ptr.status)}</Badge>
              <span dir="ltr" className="text-xs text-muted-foreground">
                {dns.ptr.hostname}
              </span>
            </div>
          </div>
        )}
      </div>

      {/* Mailboxes */}
      <div className="px-6 pb-4">
        <div className="mb-2 flex items-center justify-between gap-3">
          <p className="text-xs font-medium tracking-wide text-muted-foreground uppercase">{t("mail.mailboxes.title")}</p>
          {manage ? <MailboxDialog base={domBase} domain={domain.domain} onSaved={load} /> : null}
        </div>
        {mailboxes === null ? (
          <LoadingRows rows={2} />
        ) : mailboxes.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t("mail.mailboxes.empty")}</p>
        ) : (
          <div className="border border-border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t("mail.mailboxes.email")}</TableHead>
                  <TableHead>{t("mail.mailboxes.quota")}</TableHead>
                  <TableHead>{t("mail.statusLabel")}</TableHead>
                  <TableHead className="text-end">{t("common.actions")}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {mailboxes.map((mb) => (
                  <MailboxRow key={mb.id} base={domBase} mailbox={mb} manage={manage} onChanged={load} />
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </div>

      {/* Aliases */}
      <div className="px-6 pb-4">
        <div className="mb-2 flex items-center justify-between gap-3">
          <p className="text-xs font-medium tracking-wide text-muted-foreground uppercase">{t("mail.aliases.title")}</p>
          {manage ? <AliasDialog base={domBase} domain={domain.domain} onSaved={load} /> : null}
        </div>
        {aliases === null ? (
          <LoadingRows rows={2} />
        ) : aliases.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t("mail.aliases.empty")}</p>
        ) : (
          <NasaqTable
            data={aliases}
            rowId={(al) => al.id}
            label={t("mail.aliases.title")}
            rowLabel={(al) => al.email}
            columns={[
              { id: "email", header: t("mail.aliases.email"), sortValue: (al) => al.email, cell: (al) => <span dir="ltr">{al.email}</span> },
              {
                id: "kind",
                header: t("mail.aliases.kind"),
                cell: (al) => <Badge variant="outline">{t(`mail.aliases.kinds.${al.kind}`, undefined, al.kind)}</Badge>,
              },
              {
                id: "destinations",
                header: t("mail.aliases.destinations"),
                cell: (al) => (
                  <span dir="ltr" className="block max-w-64 truncate text-caption text-muted-foreground">
                    {al.destinations.join(", ")}
                  </span>
                ),
              },
              ...(manage
                ? [
                    {
                      id: "actions",
                      header: <span className="sr-only">{t("common.actions")}</span>,
                      align: "end" as const,
                      cell: (al: MailAlias) => (
                        <ConfirmButton
                          icon={<Trash2Icon />}
                          label={t("common.delete")}
                          title={t("mail.aliases.deleteTitle", { name: al.email })}
                          description={t("mail.aliases.deleteBody")}
                          confirmLabel={t("common.delete")}
                          onConfirm={async () => {
                            try {
                              await apiFetch(`${domBase}/aliases/${al.id}`, { method: "DELETE" })
                            } catch (err) {
                              throw new Error(errorText(err))
                            }
                            await load()
                          }}
                        />
                      ),
                    },
                  ]
                : []),
            ]}
          />
        )}
      </div>

      {/* SMTP relay credentials (MH-577 I6) */}
      <div className="px-6 pb-6">
        <div className="mb-2 flex items-center justify-between gap-3">
          <p className="text-xs font-medium tracking-wide text-muted-foreground uppercase">{t("mail.relay.title")}</p>
          {manage && domain.status === "active" && apps.length > 0 ? <RelayDialog base={domBase} domain={domain.domain} apps={apps} onSaved={load} /> : null}
        </div>
        {relays === null ? (
          <LoadingRows rows={1} />
        ) : relays.length === 0 ? (
          <p className="text-sm text-muted-foreground">{apps.length === 0 ? t("mail.relay.needApp") : t("mail.relay.empty")}</p>
        ) : (
          <div className="border border-border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t("mail.relay.email")}</TableHead>
                  <TableHead>{t("mail.relay.app")}</TableHead>
                  <TableHead>{t("mail.relay.attached")}</TableHead>
                  <TableHead>{t("mail.statusLabel")}</TableHead>
                  <TableHead className="text-end">{t("common.actions")}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {relays.map((r) => (
                  <RelayRow key={r.id} base={domBase} relay={r} apps={apps} manage={manage} onChanged={load} />
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </div>
    </div>
  )
}

function MailboxRow({ base, mailbox, manage, onChanged }: { base: string; mailbox: Mailbox; manage: boolean; onChanged: () => Promise<void> | void }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [newPassword, setNewPassword] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function rotatePassword() {
    setBusy(true)
    try {
      const r = await apiFetch<{ password: string }>(`${base}/mailboxes/${mailbox.local_part}/password`, { method: "POST" })
      setNewPassword(r.password)
    } catch (err) {
      toast.error(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <TableRow>
        <TableCell dir="ltr" className="text-start font-medium">
          {mailbox.email}
        </TableCell>
        <TableCell dir="ltr" className="text-start text-xs text-muted-foreground">
          {mailbox.quota_mb} MB
        </TableCell>
        <TableCell>
          <Badge variant={mailStatusVariant(mailbox.status)}>{t(`mail.status.${mailbox.status}`, undefined, mailbox.status)}</Badge>
        </TableCell>
        <TableCell>
          <div className="flex flex-wrap justify-end gap-2">
            {manage ? (
              <Button size="sm" variant="secondary" disabled={busy} onClick={() => void rotatePassword()}>
                <KeyRoundIcon />
                {t("mail.mailboxes.resetPassword")}
              </Button>
            ) : null}
            {manage ? (
              <ConfirmButton
                icon={<Trash2Icon />}
                label={t("common.delete")}
                title={t("mail.mailboxes.deleteTitle", { name: mailbox.email })}
                description={t("mail.mailboxes.deleteBody")}
                confirmLabel={t("common.delete")}
                onConfirm={async () => {
                  try {
                    await apiFetch(`${base}/mailboxes/${mailbox.local_part}`, { method: "DELETE" })
                  } catch (err) {
                    throw new Error(errorText(err))
                  }
                  await onChanged()
                }}
              />
            ) : null}
          </div>
        </TableCell>
      </TableRow>
      {newPassword ? (
        <TableRow>
          <TableCell colSpan={4}>
            <PasswordShown email={mailbox.email} password={newPassword} onClose={() => setNewPassword(null)} />
          </TableCell>
        </TableRow>
      ) : null}
    </>
  )
}

/** A generated password, shown once. */
function PasswordShown({ email, password, onClose }: { email: string; password: string; onClose: () => void }) {
  const { t } = useI18n()
  return (
    <Dialog open onOpenChange={(v) => !v && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("mail.mailboxes.passwordTitle", { name: email })}</DialogTitle>
          <DialogDescription>{t("mail.mailboxes.passwordHint")}</DialogDescription>
        </DialogHeader>
        <CopyField value={password} />
        <DialogFooter>
          <Button variant="primary" onClick={onClose}>{t("common.close")}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

/** Add a mailbox: local part and quota. Hosbah generates the password and shows it once. */
function MailboxDialog({ base, domain, onSaved, ...controlled }: { base: string; domain: string; onSaved: () => Promise<void> | void } & Controlled) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [localPart, setLocalPart] = useState("")
  const [quota, setQuota] = useState("1024")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [created, setCreated] = useState<{ email: string; password: string } | null>(null)
  const { open, setOpen, controlled: fromCaller } = useDialogOpen(controlled, reset)

  function reset() {
    setError(null)
    setLocalPart("")
    setQuota("1024")
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const r = await apiFetch<{ mailbox: Mailbox; password: string }>(base + "/mailboxes", {
        method: "POST",
        body: { local_part: localPart.trim(), quota_mb: Number(quota) || 1024 },
      })
      setOpen(false)
      setCreated({ email: r.mailbox.email, password: r.password })
      await onSaved()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <Dialog open={open} onOpenChange={setOpen}>
        {fromCaller ? null : (
          <DialogTrigger render={<Button size="sm" variant="secondary" />}>
            <PlusIcon />
            {t("mail.mailboxes.add")}
          </DialogTrigger>
        )}
        <DialogContent>
          <form onSubmit={submit} className="flex flex-col gap-4">
            <DialogHeader>
              <DialogTitle>{t("mail.mailboxes.add")}</DialogTitle>
              <DialogDescription>{t("mail.mailboxes.addHint", { domain })}</DialogDescription>
            </DialogHeader>
            <div className="flex w-full flex-col gap-5">
              <Field>
                <FieldLabel htmlFor="mbx-local">{t("mail.mailboxes.localPart")}</FieldLabel>
                <div className="flex items-center gap-2">
                  <Input
                    id="mbx-local"
                    dir="ltr"
                    required
                    maxLength={64}
                    placeholder="sales"
                    value={localPart}
                    onChange={(e) => setLocalPart(e.target.value)}
                    className="max-w-48"
                  />
                  <span dir="ltr" className="text-sm text-muted-foreground">
                    @{domain}
                  </span>
                </div>
              </Field>
              <Field>
                <FieldLabel htmlFor="mbx-quota">{t("mail.mailboxes.quota")}</FieldLabel>
                <Input id="mbx-quota" dir="ltr" type="number" min={1} max={1048576} required value={quota} onChange={(e) => setQuota(e.target.value)} />
                <FieldDescription>{t("mail.mailboxes.quotaHint")}</FieldDescription>
              </Field>
            </div>
            <FormError message={error} />
            <DialogFooter>
              <Button type="button" variant="secondary" onClick={() => setOpen(false)}>
                {t("common.cancel")}
              </Button>
              <Button variant="primary" type="submit" disabled={busy || !localPart.trim()}>
                {busy ? t("common.working") : t("mail.mailboxes.add")}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      {created ? <PasswordShown email={created.email} password={created.password} onClose={() => setCreated(null)} /> : null}
    </>
  )
}

/** Add an alias (fans out to one or more mailboxes) or an external forward. */
function AliasDialog({ base, domain, onSaved, ...controlled }: { base: string; domain: string; onSaved: () => Promise<void> | void } & Controlled) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [localPart, setLocalPart] = useState("")
  const [kind, setKind] = useState<"alias" | "forward">("alias")
  const [destinations, setDestinations] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const { open, setOpen, controlled: fromCaller } = useDialogOpen(controlled, reset)

  function reset() {
    setError(null)
    setLocalPart("")
    setKind("alias")
    setDestinations("")
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    const dests = destinations
      .split(/[,\n]/)
      .map((s) => s.trim())
      .filter(Boolean)
    try {
      await apiFetch(base + "/aliases", { method: "POST", body: { local_part: localPart.trim(), kind, destinations: dests } })
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
      {fromCaller ? null : (
        <DialogTrigger render={<Button size="sm" variant="secondary" />}>
          <PlusIcon />
          {t("mail.aliases.add")}
        </DialogTrigger>
      )}
      <DialogContent>
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{t("mail.aliases.add")}</DialogTitle>
            <DialogDescription>{t("mail.aliases.addHint", { domain })}</DialogDescription>
          </DialogHeader>
          <div className="flex w-full flex-col gap-5">
            <Field>
              <FieldLabel htmlFor="al-local">{t("mail.aliases.localPart")}</FieldLabel>
              <div className="flex items-center gap-2">
                <Input
                  id="al-local"
                  dir="ltr"
                  required
                  maxLength={64}
                  placeholder="info"
                  value={localPart}
                  onChange={(e) => setLocalPart(e.target.value)}
                  className="max-w-48"
                />
                <span dir="ltr" className="text-sm text-muted-foreground">
                  @{domain}
                </span>
              </div>
            </Field>
            <Field>
              <FieldLabel htmlFor="al-kind">{t("mail.aliases.kind")}</FieldLabel>
              <Select value={kind} onValueChange={(v) => setKind(v as "alias" | "forward")}>
                <SelectTrigger id="al-kind">
                  <SelectValue>{(v: string) => t(`mail.aliases.kinds.${v}`, undefined, v)}</SelectValue>
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="alias">{t("mail.aliases.kinds.alias")}</SelectItem>
                  <SelectItem value="forward">{t("mail.aliases.kinds.forward")}</SelectItem>
                </SelectContent>
              </Select>
            </Field>
            <Field>
              <FieldLabel htmlFor="al-dest">{t("mail.aliases.destinations")}</FieldLabel>
              <Input
                id="al-dest"
                dir="ltr"
                required
                placeholder="sales@example.com, ops@example.com"
                value={destinations}
                onChange={(e) => setDestinations(e.target.value)}
              />
              <FieldDescription>{kind === "alias" ? t("mail.aliases.destHintAlias") : t("mail.aliases.destHintForward")}</FieldDescription>
            </Field>
          </div>
          <FormError message={error} />
          <DialogFooter>
            <Button type="button" variant="secondary" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button variant="primary" type="submit" disabled={busy || !localPart.trim() || !destinations.trim()}>
              {busy ? t("common.working") : t("mail.aliases.add")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

/** MH-577 I5: a domain's deliverability report — DNS/PTR status plus DNSBL blacklist lookups. */
function DeliverabilityDialog({ report, onClose }: { report: DeliverabilityReport; onClose: () => void }) {
  const { t } = useI18n()
  return (
    <Dialog open onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>{t("mail.deliverability.title", { domain: report.domain })}</DialogTitle>
          <DialogDescription>{t("mail.deliverability.hint")}</DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-4">
          <div>
            <p className="mb-2 text-xs font-medium tracking-wide text-muted-foreground uppercase">{t("mail.dns.records")}</p>
            <div className="flex flex-col gap-2">
              {report.dns.records.map((r, i) => (
                <div key={`${r.type}-${r.name}-${i}`} className="flex flex-wrap items-center gap-2 text-sm">
                  <Badge variant="outline" dir="ltr">
                    {r.type}
                  </Badge>
                  <Badge variant={dnsStatusVariant(r.status)}>{t(`mail.dns.status.${r.status}`, undefined, r.status)}</Badge>
                  <span dir="ltr" className="text-xs text-muted-foreground">
                    {r.name}
                  </span>
                </div>
              ))}
              <div className="flex flex-wrap items-center gap-2 text-sm">
                <Badge variant="outline" dir="ltr">
                  PTR
                </Badge>
                <Badge variant={dnsStatusVariant(report.dns.ptr.status)}>{t(`mail.dns.status.${report.dns.ptr.status}`, undefined, report.dns.ptr.status)}</Badge>
                <span dir="ltr" className="text-xs text-muted-foreground">
                  {report.dns.ptr.hostname}
                </span>
              </div>
            </div>
          </div>
          <div>
            <p className="mb-2 text-xs font-medium tracking-wide text-muted-foreground uppercase">{t("mail.deliverability.blacklists")}</p>
            <div className="flex flex-col gap-2">
              {report.blacklists.map((bl) => (
                <div key={bl.zone} className="flex flex-wrap items-center gap-2 text-sm">
                  <Badge variant={blacklistStatusVariant(bl.status)}>{t(`mail.deliverability.status.${bl.status}`, undefined, bl.status)}</Badge>
                  <span dir="ltr" className="text-xs text-muted-foreground">
                    {bl.zone}
                  </span>
                  {bl.detail ? (
                    <span dir="ltr" className="text-xs text-muted-foreground">
                      ({bl.detail})
                    </span>
                  ) : null}
                </div>
              ))}
            </div>
          </div>
          <p className="border-t border-border pt-3 text-xs text-muted-foreground">{report.spam_filter_note}</p>
        </div>
        <DialogFooter>
          <Button variant="primary" onClick={onClose}>{t("common.close")}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

/** MH-577 I6: an SMTP relay credential row — rotate its password or revoke it, and attach it to its app. */
function RelayRow({
  base,
  relay,
  apps,
  manage,
  onChanged,
}: {
  base: string
  relay: MailRelay
  apps: { id: string; name: string }[]
  manage: boolean
  onChanged: () => Promise<void> | void
}) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [newPassword, setNewPassword] = useState<string | null>(null)
  const [busy, setBusy] = useState<"rotate" | "attach" | null>(null)
  const appName = apps.find((a) => a.id === relay.app_id)?.name ?? relay.app_id

  async function rotate() {
    setBusy("rotate")
    try {
      const r = await apiFetch<{ password: string }>(`${base}/relays/${relay.id}/rotate`, { method: "POST" })
      setNewPassword(r.password)
      await onChanged()
    } catch (err) {
      toast.error(errorText(err))
    } finally {
      setBusy(null)
    }
  }

  async function attach() {
    setBusy("attach")
    try {
      await apiFetch(`${base}/relays/${relay.id}/attach`, { method: "POST" })
      toast.success(t("mail.relay.attachedToast", { app: appName }))
      await onChanged()
    } catch (err) {
      toast.error(errorText(err))
    } finally {
      setBusy(null)
    }
  }

  return (
    <>
      <TableRow>
        <TableCell dir="ltr" className="text-start font-medium">
          {relay.email}
        </TableCell>
        <TableCell dir="ltr" className="text-start text-xs text-muted-foreground">
          {appName}
        </TableCell>
        <TableCell>
          <Badge variant={relay.attached ? "brand" : "outline"}>{relay.attached ? t("mail.relay.yes") : t("mail.relay.no")}</Badge>
        </TableCell>
        <TableCell>
          <Badge variant={mailStatusVariant(relay.status)}>{t(`mail.status.${relay.status}`, undefined, relay.status)}</Badge>
          {relay.error ? <p className="mt-1 max-w-64 text-xs text-nq-danger-text">{relay.error}</p> : null}
        </TableCell>
        <TableCell>
          <div className="flex flex-wrap justify-end gap-2">
            {manage && relay.status === "ready" ? (
              <Button size="sm" variant="secondary" disabled={busy !== null} onClick={() => void attach()}>
                <LinkIcon />
                {busy === "attach" ? t("common.working") : relay.attached ? t("mail.relay.reattach") : t("mail.relay.attach")}
              </Button>
            ) : null}
            {manage ? (
              <Button size="sm" variant="secondary" disabled={busy !== null} onClick={() => void rotate()}>
                <KeyRoundIcon />
                {busy === "rotate" ? t("common.working") : t("mail.relay.rotate")}
              </Button>
            ) : null}
            {manage ? (
              <ConfirmButton
                icon={<Trash2Icon />}
                label={t("common.delete")}
                title={t("mail.relay.revokeTitle", { name: relay.email })}
                description={t("mail.relay.revokeBody")}
                confirmLabel={t("common.delete")}
                onConfirm={async () => {
                  try {
                    await apiFetch(`${base}/relays/${relay.id}`, { method: "DELETE" })
                  } catch (err) {
                    throw new Error(errorText(err))
                  }
                  await onChanged()
                }}
              />
            ) : null}
          </div>
        </TableCell>
      </TableRow>
      {newPassword ? (
        <TableRow>
          <TableCell colSpan={5}>
            <PasswordShown email={relay.email} password={newPassword} onClose={() => setNewPassword(null)} />
          </TableCell>
        </TableRow>
      ) : null}
    </>
  )
}

/** MH-577 I6: create an SMTP relay credential dedicated to one app. Hosbah generates the password and shows it once. */
function RelayDialog({
  base,
  domain,
  apps,
  onSaved,
  ...controlled
}: { base: string; domain: string; apps: { id: string; name: string }[]; onSaved: () => Promise<void> | void } & Controlled) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [app, setApp] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [created, setCreated] = useState<{ email: string; password: string } | null>(null)
  const { open, setOpen, controlled: fromCaller } = useDialogOpen(controlled, reset)

  function reset() {
    setError(null)
    setApp(apps[0]?.id ?? "")
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const r = await apiFetch<{ relay: MailRelay; password: string }>(base + "/relays", { method: "POST", body: { app } })
      setOpen(false)
      setCreated({ email: r.relay.email, password: r.password })
      await onSaved()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <Dialog open={open} onOpenChange={setOpen}>
        {fromCaller ? null : (
          <DialogTrigger render={<Button size="sm" variant="secondary" />}>
            <PlusIcon />
            {t("mail.relay.add")}
          </DialogTrigger>
        )}
        <DialogContent>
          <form onSubmit={submit} className="flex flex-col gap-4">
            <DialogHeader>
              <DialogTitle>{t("mail.relay.add")}</DialogTitle>
              <DialogDescription>{t("mail.relay.addHint", { domain })}</DialogDescription>
            </DialogHeader>
            <div className="flex w-full flex-col gap-5">
              <Field>
                <FieldLabel htmlFor="relay-app">{t("mail.relay.app")}</FieldLabel>
                <Select value={app} onValueChange={(v) => setApp(v ?? "")}>
                  <SelectTrigger id="relay-app">
                    <SelectValue>{(v: string) => apps.find((a) => a.id === v)?.name ?? v}</SelectValue>
                  </SelectTrigger>
                  <SelectContent>
                    {apps.map((a) => (
                      <SelectItem key={a.id} value={a.id}>
                        {a.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <FieldDescription>{t("mail.relay.appHint")}</FieldDescription>
              </Field>
            </div>
            <FormError message={error} />
            <DialogFooter>
              <Button type="button" variant="secondary" onClick={() => setOpen(false)}>
                {t("common.cancel")}
              </Button>
              <Button variant="primary" type="submit" disabled={busy || !app}>
                {busy ? t("common.working") : t("mail.relay.add")}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      {created ? <PasswordShown email={created.email} password={created.password} onClose={() => setCreated(null)} /> : null}
    </>
  )
}
