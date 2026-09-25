"use client"

import { useCallback, useEffect, useState, type FormEvent } from "react"
import { CheckCircle2Icon, KeyRoundIcon, MailIcon, PlusIcon, RefreshCcwIcon, Trash2Icon, UploadCloudIcon } from "lucide-react"
import { toast } from "sonner"

import { ConfirmButton } from "@/components/confirm-button"
import { CopyField } from "@/components/copy-field"
import { useDialogOpen, type Controlled } from "@/components/dialog-state"
import { FormError, useErrorText } from "@/components/form-error"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { ErrorBand, LoadingRows, SectionTitle, TableBand } from "@/components/states"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { apiFetch } from "@/lib/api"
import { useI18n } from "@/lib/i18n"
import {
  dnsStatusVariant,
  mailStatusVariant,
  type MailAlias,
  type MailDnsPublishResult,
  type MailDomain,
  type MailDomainDns,
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
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      const [s, hs, d] = await Promise.all([
        apiFetch<{ servers: MailServer[] }>(`${base}/servers`),
        apiFetch<{ servers: Server[] }>(`/api/teams/${team.slug}/servers`),
        apiFetch<{ domains: MailDomain[] }>(`${base}/domains`),
      ])
      setServers(s.servers ?? [])
      setHostServers(hs.servers ?? [])
      setDomains(d.domains ?? [])
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
          <Empty className="border-b">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <MailIcon />
              </EmptyMedia>
              <EmptyTitle>{t("mail.servers.emptyTitle")}</EmptyTitle>
              <EmptyDescription>{t("mail.servers.emptyHint")}</EmptyDescription>
            </EmptyHeader>
            {manage ? <InstallDialog base={base} servers={enrolledServers} onSaved={load} /> : null}
          </Empty>
        ) : (
          <TableBand>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t("mail.servers.hostname")}</TableHead>
                  <TableHead>{t("mail.servers.onServer")}</TableHead>
                  <TableHead>{t("mail.statusLabel")}</TableHead>
                  <TableHead className="text-end">{t("common.actions")}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {servers.map((s) => (
                  <TableRow key={s.id}>
                    <TableCell dir="ltr" className="text-start font-medium">
                      {s.hostname}
                    </TableCell>
                    <TableCell dir="ltr" className="text-start text-xs text-grid-muted">
                      {enrolledServers.find((hs) => hs.id === s.server_id)?.name ?? s.server_id.slice(0, 8)}
                    </TableCell>
                    <TableCell>
                      <Badge variant={mailStatusVariant(s.status)}>{t(`mail.status.${s.status}`, undefined, s.status)}</Badge>
                      {s.error ? <p className="mt-1 max-w-64 text-xs text-grid-danger">{s.error}</p> : null}
                    </TableCell>
                    <TableCell>
                      <div className="flex justify-end">
                        {manage ? <DeleteServer base={base} server={s} onDeleted={load} /> : null}
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </TableBand>
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
          <p className="border-b border-line px-6 py-6 text-sm text-grid-muted">
            {servers && servers.length === 0 ? t("mail.domains.needServer") : t("mail.domains.empty")}
          </p>
        ) : (
          <div className="flex flex-col">
            {domains.map((d) => (
              <DomainCard key={d.id} base={base} domain={d} manage={manage} onChanged={load} />
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
        <DialogTrigger render={<Button size="sm" />}>
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
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="mail-inst-server">{t("mail.servers.onServer")}</FieldLabel>
              {servers.length === 0 ? (
                <p className="text-sm text-grid-muted">{t("mail.servers.noServers")}</p>
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
          </FieldGroup>
          <FormError message={error} />
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={busy || !server || !hostname.trim()}>
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
        <DialogTrigger render={<Button size="sm" />}>
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
          <FieldGroup>
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
          </FieldGroup>
          <FormError message={error} />
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={busy || !server || !domain.trim()}>
              {busy ? t("common.working") : t("mail.domains.add")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

/** One domain: its status, mailboxes, aliases and DNS records, each loaded lazily once expanded. */
function DomainCard({ base, domain, manage, onChanged }: { base: string; domain: MailDomain; manage: boolean; onChanged: () => Promise<void> | void }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const domBase = `${base}/domains/${domain.id}`
  const [mailboxes, setMailboxes] = useState<Mailbox[] | null>(null)
  const [aliases, setAliases] = useState<MailAlias[] | null>(null)
  const [dns, setDns] = useState<MailDomainDns | null>(null)
  const [publishResult, setPublishResult] = useState<MailDnsPublishResult | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState<"check" | "publish" | null>(null)

  const load = useCallback(async () => {
    try {
      const [m, a, d] = await Promise.all([
        apiFetch<{ mailboxes: Mailbox[] }>(`${domBase}/mailboxes`),
        apiFetch<{ aliases: MailAlias[] }>(`${domBase}/aliases`),
        apiFetch<MailDomainDns>(`${domBase}/dns`),
      ])
      setMailboxes(m.mailboxes ?? [])
      setAliases(a.aliases ?? [])
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

  return (
    <div className="border-b border-line">
      <div className="flex flex-wrap items-center justify-between gap-3 px-6 py-4">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <span dir="ltr" className="font-medium text-grid-fg">
              {domain.domain}
            </span>
            <Badge variant={mailStatusVariant(domain.status)}>{t(`mail.status.${domain.status}`, undefined, domain.status)}</Badge>
          </div>
          {domain.error ? <p className="mt-1 text-xs text-grid-danger">{domain.error}</p> : null}
        </div>
        <div className="flex flex-wrap gap-2">
          <Button size="sm" variant="outline" disabled={busy !== null} onClick={() => void checkDns()}>
            <RefreshCcwIcon />
            {busy === "check" ? t("common.working") : t("mail.dns.check")}
          </Button>
          {manage ? (
            <Button size="sm" variant="outline" disabled={busy !== null} onClick={() => void publishDns()}>
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

      {publishResult ? (
        <div className="mx-6 mb-4 flex items-start gap-3 border border-line bg-grid-soft px-4 py-3 text-sm">
          <CheckCircle2Icon className="mt-0.5 size-4 shrink-0 text-grid-brand" />
          <div>
            <p className="font-medium text-grid-fg">{t("mail.dns.publishedTitle")}</p>
            <p dir="ltr" className="mt-1 text-xs text-grid-muted">
              {publishResult.published.join(", ") || "—"}
            </p>
          </div>
        </div>
      ) : null}

      {/* DNS records */}
      <div className="px-6 pb-4">
        <p className="mb-2 text-xs font-medium tracking-wide text-grid-muted uppercase">{t("mail.dns.records")}</p>
        {dns === null ? (
          <LoadingRows rows={2} />
        ) : dns.records.length === 0 ? (
          <p className="text-sm text-grid-muted">{t("mail.dns.noRecords")}</p>
        ) : (
          <div className="flex flex-col gap-2">
            {dns.records.map((r, i) => (
              <div key={`${r.type}-${r.name}-${i}`} className="flex flex-wrap items-center gap-2">
                <Badge variant="outline" dir="ltr">
                  {r.type}
                </Badge>
                <Badge variant={dnsStatusVariant(r.status)}>{t(`mail.dns.status.${r.status}`, undefined, r.status)}</Badge>
                <span dir="ltr" className="shrink-0 text-xs text-grid-muted">
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
              <span dir="ltr" className="text-xs text-grid-muted">
                {dns.ptr.hostname}
              </span>
            </div>
          </div>
        )}
      </div>

      {/* Mailboxes */}
      <div className="px-6 pb-4">
        <div className="mb-2 flex items-center justify-between gap-3">
          <p className="text-xs font-medium tracking-wide text-grid-muted uppercase">{t("mail.mailboxes.title")}</p>
          {manage ? <MailboxDialog base={domBase} domain={domain.domain} onSaved={load} /> : null}
        </div>
        {mailboxes === null ? (
          <LoadingRows rows={2} />
        ) : mailboxes.length === 0 ? (
          <p className="text-sm text-grid-muted">{t("mail.mailboxes.empty")}</p>
        ) : (
          <div className="border border-line">
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
      <div className="px-6 pb-6">
        <div className="mb-2 flex items-center justify-between gap-3">
          <p className="text-xs font-medium tracking-wide text-grid-muted uppercase">{t("mail.aliases.title")}</p>
          {manage ? <AliasDialog base={domBase} domain={domain.domain} onSaved={load} /> : null}
        </div>
        {aliases === null ? (
          <LoadingRows rows={2} />
        ) : aliases.length === 0 ? (
          <p className="text-sm text-grid-muted">{t("mail.aliases.empty")}</p>
        ) : (
          <div className="border border-line">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t("mail.aliases.email")}</TableHead>
                  <TableHead>{t("mail.aliases.kind")}</TableHead>
                  <TableHead>{t("mail.aliases.destinations")}</TableHead>
                  <TableHead className="text-end">{t("common.actions")}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {aliases.map((al) => (
                  <TableRow key={al.id}>
                    <TableCell dir="ltr" className="text-start">
                      {al.email}
                    </TableCell>
                    <TableCell>
                      <Badge variant="outline">{t(`mail.aliases.kinds.${al.kind}`, undefined, al.kind)}</Badge>
                    </TableCell>
                    <TableCell dir="ltr" className="max-w-64 truncate text-start text-xs text-grid-muted">
                      {al.destinations.join(", ")}
                    </TableCell>
                    <TableCell>
                      <div className="flex justify-end">
                        {manage ? (
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
                        ) : null}
                      </div>
                    </TableCell>
                  </TableRow>
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
        <TableCell dir="ltr" className="text-start text-xs text-grid-muted">
          {mailbox.quota_mb} MB
        </TableCell>
        <TableCell>
          <Badge variant={mailStatusVariant(mailbox.status)}>{t(`mail.status.${mailbox.status}`, undefined, mailbox.status)}</Badge>
        </TableCell>
        <TableCell>
          <div className="flex flex-wrap justify-end gap-2">
            {manage ? (
              <Button size="sm" variant="outline" disabled={busy} onClick={() => void rotatePassword()}>
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
          <Button onClick={onClose}>{t("common.close")}</Button>
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
          <DialogTrigger render={<Button size="sm" variant="outline" />}>
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
            <FieldGroup>
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
                  <span dir="ltr" className="text-sm text-grid-muted">
                    @{domain}
                  </span>
                </div>
              </Field>
              <Field>
                <FieldLabel htmlFor="mbx-quota">{t("mail.mailboxes.quota")}</FieldLabel>
                <Input id="mbx-quota" dir="ltr" type="number" min={1} max={1048576} required value={quota} onChange={(e) => setQuota(e.target.value)} />
                <FieldDescription>{t("mail.mailboxes.quotaHint")}</FieldDescription>
              </Field>
            </FieldGroup>
            <FormError message={error} />
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => setOpen(false)}>
                {t("common.cancel")}
              </Button>
              <Button type="submit" disabled={busy || !localPart.trim()}>
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
        <DialogTrigger render={<Button size="sm" variant="outline" />}>
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
          <FieldGroup>
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
                <span dir="ltr" className="text-sm text-grid-muted">
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
          </FieldGroup>
          <FormError message={error} />
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={busy || !localPart.trim() || !destinations.trim()}>
              {busy ? t("common.working") : t("mail.aliases.add")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
