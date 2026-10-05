"use client"

import { Badge, Button, Checkbox, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger, EmptyState, Field, FieldDescription, FieldLabel, Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, type DataTableColumn } from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useState, type FormEvent } from "react"
import { NetworkIcon, PlusIcon } from "lucide-react"

import { ConfirmButton } from "@/components/confirm-button"
import { NasaqTable } from "@/components/lists/nasaq-table"
import { EditDialog } from "@/components/server-tabs/list-tools"
import { FormError, useErrorText } from "@/components/form-error"
import { SiteRules } from "@/components/site/rules"
import { ErrorBand, LoadingRows, SectionTitle } from "@/components/states"









import { apiFetch } from "@/lib/api"
import { useI18n } from "@/lib/i18n"
import type { ProxyHost, Server } from "@/lib/servers"
import { useTeam } from "@/lib/team"

const TLS_MODES = ["auto", "internal", "off"] as const

/** The proxy hosts Caddy serves on one server. */
export function ServerProxy({ serverId }: { serverId: string }) {
  const { t } = useI18n()
  const { team, manage } = useTeam()
  const errorText = useErrorText()
  const [hosts, setHosts] = useState<ProxyHost[] | null>(null)
  const [servers, setServers] = useState<Server[]>([])
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      const [p, s] = await Promise.all([
        apiFetch<{ proxy_hosts: ProxyHost[] }>(`/api/teams/${team.slug}/proxy-hosts?server=${serverId}`),
        apiFetch<{ servers: Server[] }>(`/api/teams/${team.slug}/servers`),
      ])
      setHosts(p.proxy_hosts ?? [])
      setServers((s.servers ?? []).filter((x) => x.id === serverId && x.status === "enrolled"))
    } catch (err) {
      setError(errorText(err))
      setHosts([])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [team.slug, serverId])

  useEffect(() => {
    void load()
  }, [load])

  async function act(path: string, method: string, body?: unknown) {
    setError(null)
    try {
      await apiFetch(`/api/teams/${team.slug}/proxy-hosts/${path}`, { method, body })
    } catch (err) {
      setError(errorText(err))
    }
    await load()
  }

  return (
    <section aria-labelledby="tab-h">
      <SectionTitle id="tab-h" title={t("proxy.title")} hint={t("proxy.description")} action={manage ? <AddProxyHost servers={servers} onSaved={load} /> : null} />
      <ErrorBand message={error} />
      {hosts === null ? (
        <LoadingRows />
      ) : hosts.length === 0 ? (
        <EmptyState className="mt-3 rounded-lg border border-border bg-card" icon={NetworkIcon} title={t("proxy.emptyTitle")} description={t("proxy.emptyHint")} />
      ) : (
        <div className="mt-3">
          <NasaqTable
            data={hosts}
            rowId={(h) => h.id}
            label={t("proxy.title")}
            rowLabel={(h) => h.hostname}
            search={t("lists.searchProxy")}
            empty={t("home.noMatch")}
            facets={[{ column: "tls", title: t("proxy.tls"), options: [...new Set(hosts.map((h) => h.tls))].map((v) => ({ value: v, label: t(`proxy.tlsModes.${v}`) })) }]}
            rowActions={
              manage
                ? (h) => [
                    ...(h.tls === "auto" && !h.verified ? [{ id: "verify", label: t("proxy.verify"), onSelect: () => void act(`${h.id}/verify`, "POST") }] : []),
                    { id: "toggle", label: h.enabled ? t("proxy.disable") : t("proxy.enable"), onSelect: () => void act(h.id, "PATCH", { enabled: !h.enabled }) },
                  ]
                : undefined
            }
            columns={[
              {
                id: "hostname",
                header: t("proxy.hostname"),
                label: t("proxy.hostname"),
                sortValue: (h) => h.hostname,
                searchValue: (h) => [h.hostname, h.upstream, h.server_name ?? "", h.tls].join(" "),
                cell: (h) => (
                  <span className="block min-w-0">
                    <span className="flex flex-wrap items-center gap-2">
                      <span dir="ltr" className="font-medium">
                        {h.hostname}
                      </span>
                      <Badge variant={h.enabled ? "success" : "outline"}>{h.enabled ? t("proxy.enabled") : t("proxy.disabled")}</Badge>
                      {h.tls === "auto" ? <Badge variant={h.verified ? "neutral" : "warning"}>{h.verified ? t("proxy.verified") : t("proxy.unverified")}</Badge> : null}
                    </span>
                    {h.tls === "auto" && !h.verified ? (
                      <span className="mt-1 block max-w-md text-caption text-muted-foreground">{t("proxy.verifyHint", { name: h.verify_name, value: h.verify_value })}</span>
                    ) : null}
                  </span>
                ),
              },
              {
                id: "upstream",
                header: t("proxy.upstream"),
                label: t("proxy.upstream"),
                sortValue: (h) => h.upstream,
                cell: (h) => (
                  <span dir="ltr" className="font-mono text-caption">
                    {h.upstream}
                  </span>
                ),
              },
              {
                id: "server",
                header: t("proxy.server"),
                label: t("proxy.server"),
                className: "max-lg:hidden",
                headerClassName: "max-lg:hidden",
                cell: (h) => <span dir="ltr">{h.server_name ?? "—"}</span>,
              },
              { id: "tls", header: t("proxy.tls"), label: t("proxy.tls"), sortValue: (h) => h.tls, filterValue: (h) => h.tls, cell: (h) => <Badge variant="neutral">{t(`proxy.tlsModes.${h.tls}`)}</Badge> },
              {
                id: "actions",
                header: <span className="sr-only">{t("common.actions")}</span>,
                label: t("common.actions"),
                cell: (h) =>
                  !manage ? null : (
                    <div className="flex flex-wrap justify-end gap-2">
                      <EditProxy host={h} team={team.slug} onSaved={load} />
                      <ProxyRules host={h} team={team.slug} />
                      <ConfirmButton
                        label={t("proxy.remove")}
                        title={t("proxy.remove")}
                        description={t("proxy.removeConfirm", { host: h.hostname })}
                        confirmLabel={t("proxy.remove")}
                        onConfirm={async () => {
                          try {
                            await apiFetch(`/api/teams/${team.slug}/proxy-hosts/${h.id}`, { method: "DELETE" })
                          } catch (err) {
                            throw new Error(errorText(err))
                          }
                          await load()
                        }}
                      />
                    </div>
                  ),
              },
            ] satisfies DataTableColumn<ProxyHost>[]}
          />
        </div>
      )}
    </section>
  )
}

export function AddProxyHost({ servers, onSaved }: { servers: Server[]; onSaved: () => Promise<void> }) {
  const { t } = useI18n()
  const { team } = useTeam()
  const errorText = useErrorText()
  const [open, setOpen] = useState(false)
  const [server, setServer] = useState("")
  const [hostname, setHostname] = useState("")
  const [upstream, setUpstream] = useState("")
  const [tls, setTls] = useState<(typeof TLS_MODES)[number]>("auto")
  const [skipVerify, setSkipVerify] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (!server && servers[0]) setServer(servers[0].id)
  }, [servers, server])

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await apiFetch(`/api/teams/${team.slug}/proxy-hosts`, {
        method: "POST",
        body: { server, hostname: hostname.trim(), upstream: upstream.trim(), tls, skip_verify: skipVerify },
      })
      setOpen(false)
      setHostname("")
      setUpstream("")
      await onSaved()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger render={<Button variant="primary" />}>
        <PlusIcon />
        {t("proxy.add")}
      </DialogTrigger>
      <DialogContent className="max-h-[90dvh] overflow-y-auto">
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{t("proxy.add")}</DialogTitle>
            <DialogDescription>{t("proxy.addHint")}</DialogDescription>
          </DialogHeader>
          {servers.length === 0 ? (
            <p className="text-sm text-muted-foreground">{t("proxy.noServers")}</p>
          ) : (
            <div className="flex w-full flex-col gap-4">
              <Field>
                <FieldLabel htmlFor="px-server">{t("proxy.server")}</FieldLabel>
                <Select value={server} onValueChange={(v) => v && setServer(v as string)}>
                  <SelectTrigger id="px-server" className="w-full">
                    <SelectValue>{(v: string) => servers.find((s) => s.id === v)?.name ?? ""}</SelectValue>
                  </SelectTrigger>
                  <SelectContent>
                    {servers.map((s) => (
                      <SelectItem key={s.id} value={s.id}>
                        {s.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
              <Field>
                <FieldLabel htmlFor="px-host">{t("proxy.hostname")}</FieldLabel>
                <Input
                  id="px-host"
                  dir="ltr"
                  required
                  maxLength={253}
                  placeholder="nas.example.com"
                  value={hostname}
                  onChange={(e) => setHostname(e.target.value)}
                  autoComplete="off"
                />
              </Field>
              <Field>
                <FieldLabel htmlFor="px-up">{t("proxy.upstream")}</FieldLabel>
                <Input
                  id="px-up"
                  dir="ltr"
                  required
                  maxLength={300}
                  placeholder="10.0.0.5:5000"
                  value={upstream}
                  onChange={(e) => setUpstream(e.target.value)}
                  autoComplete="off"
                />
                <FieldDescription>{t("proxy.upstreamHint")}</FieldDescription>
              </Field>
              <Field>
                <FieldLabel htmlFor="px-tls">{t("proxy.tls")}</FieldLabel>
                <Select value={tls} onValueChange={(v) => v && setTls(v as (typeof TLS_MODES)[number])}>
                  <SelectTrigger id="px-tls" className="w-full">
                    <SelectValue>{(v: string) => t(`proxy.tlsModes.${v}`)}</SelectValue>
                  </SelectTrigger>
                  <SelectContent>
                    {TLS_MODES.map((m) => (
                      <SelectItem key={m} value={m}>
                        {t(`proxy.tlsModes.${m}`)}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
              {upstream.trim().toLowerCase().startsWith("https://") ? (
                <Field className="flex-row items-center">
                  <Checkbox id="px-skip" checked={skipVerify} onCheckedChange={(v) => setSkipVerify(v === true)} />
                  <FieldLabel htmlFor="px-skip" className="font-normal">
                    {t("proxy.skipVerify")}
                  </FieldLabel>
                </Field>
              ) : null}
            </div>
          )}
          <FormError message={error} />
          <DialogFooter>
            <Button type="button" variant="secondary" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button variant="primary" type="submit" disabled={busy || servers.length === 0 || !hostname.trim() || !upstream.trim()}>
              {busy ? t("common.working") : t("proxy.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

/**
 * Redirects, response headers, basic auth and IP access rules for one proxy host — the same editor
 * apps use (MH-578), reused here against /api/teams/{team}/proxy-hosts/{host}/rules.
 */
function ProxyRules({ host, team }: { host: ProxyHost; team: string }) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger render={<Button size="sm" variant="secondary" />}>{t("proxy.rules")}</DialogTrigger>
      <DialogContent className="max-h-[90dvh] max-w-2xl overflow-y-auto p-0">
        <DialogHeader className="px-6 pt-6">
          <DialogTitle dir="ltr">{host.hostname}</DialogTitle>
          <DialogDescription>{t("proxy.rulesHint")}</DialogDescription>
        </DialogHeader>
        {open ? <SiteRules basePath={`/api/teams/${team}/proxy-hosts/${host.id}/rules`} /> : null}
      </DialogContent>
    </Dialog>
  )
}

/** Edit a proxy host: where it sends traffic, its TLS mode and websockets. The hostname is fixed. */
function EditProxy({ host, team, onSaved }: { host: ProxyHost; team: string; onSaved: () => Promise<void> }) {
  const { t } = useI18n()
  const [upstream, setUpstream] = useState("")
  const [tls, setTls] = useState<(typeof TLS_MODES)[number]>("auto")
  const [ws, setWs] = useState(false)
  return (
    <EditDialog
      title={host.hostname}
      description={t("lists.editProxyHint")}
      onOpen={() => {
        setUpstream(host.upstream)
        setTls(host.tls as (typeof TLS_MODES)[number])
        setWs(!!(host as { websockets?: boolean }).websockets)
      }}
      onSave={async () => {
        await apiFetch(`/api/teams/${team}/proxy-hosts/${host.id}`, { method: "PATCH", body: { upstream: upstream.trim(), tls, websockets: ws } })
        await onSaved()
      }}
    >
      <div className="flex w-full flex-col gap-5">
        <Field>
          <FieldLabel htmlFor="ep-up">{t("proxy.upstream")}</FieldLabel>
          <Input id="ep-up" dir="ltr" required value={upstream} onChange={(e) => setUpstream(e.target.value)} />
        </Field>
        <Field>
          <FieldLabel>{t("proxy.tls")}</FieldLabel>
          <Select value={tls} onValueChange={(v) => v && setTls(v as (typeof TLS_MODES)[number])}>
            <SelectTrigger aria-label={t("proxy.tls")}>
              <SelectValue>{(v: string) => t(`proxy.tlsModes.${v}`)}</SelectValue>
            </SelectTrigger>
            <SelectContent>
              {TLS_MODES.map((m) => (
                <SelectItem key={m} value={m}>
                  {t(`proxy.tlsModes.${m}`)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        <label className="flex items-center gap-2 text-sm">
          <Checkbox checked={ws} onCheckedChange={(v) => setWs(v === true)} />
          {t("lists.websockets")}
        </label>
      </div>
    </EditDialog>
  )
}
