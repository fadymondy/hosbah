"use client"

import { useCallback, useEffect, useState, type FormEvent } from "react"
import { NetworkIcon, PlusIcon } from "lucide-react"

import { ConfirmButton } from "@/components/confirm-button"
import { EditDialog, ListSearch, matches } from "@/components/server-tabs/list-tools"
import { FormError, useErrorText } from "@/components/form-error"
import { ErrorBand, LoadingRows, SectionTitle, TableBand } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
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
  const [q, setQ] = useState("")
  const shown = (hosts ?? []).filter((h) => matches(q, h.hostname, h.upstream, h.tls))

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
        <Empty className="border-b">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <NetworkIcon />
            </EmptyMedia>
            <EmptyTitle>{t("proxy.emptyTitle")}</EmptyTitle>
            <EmptyDescription>{t("proxy.emptyHint")}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <>
        <ListSearch value={q} onChange={setQ} placeholder={t("lists.searchProxy")} shown={shown.length} total={hosts.length} />
        {shown.length === 0 ? <p className="border-b border-line px-6 py-8 text-center text-sm text-grid-muted">{t("home.noMatch")}</p> : null}
        <TableBand>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("proxy.hostname")}</TableHead>
                <TableHead>{t("proxy.upstream")}</TableHead>
                <TableHead>{t("proxy.server")}</TableHead>
                <TableHead>{t("proxy.tls")}</TableHead>
                {manage ? <TableHead className="text-end">{t("common.actions")}</TableHead> : null}
              </TableRow>
            </TableHeader>
            <TableBody>
              {shown.map((h) => (
                <TableRow key={h.id}>
                  <TableCell>
                    <span dir="ltr" className="font-medium">
                      {h.hostname}
                    </span>
                    <div className="mt-1 flex flex-wrap gap-1">
                      <Badge variant={h.enabled ? "secondary" : "outline"}>
                        {h.enabled ? t("proxy.enabled") : t("proxy.disabled")}
                      </Badge>
                      {h.tls === "auto" ? (
                        <Badge variant={h.verified ? "secondary" : "outline"}>
                          {h.verified ? t("proxy.verified") : t("proxy.unverified")}
                        </Badge>
                      ) : null}
                    </div>
                    {h.tls === "auto" && !h.verified ? (
                      <p className="mt-1 text-xs text-muted-foreground">
                        {t("proxy.verifyHint", { name: h.verify_name, value: h.verify_value })}
                      </p>
                    ) : null}
                  </TableCell>
                  <TableCell dir="ltr" className="text-start font-mono text-xs">
                    {h.upstream}
                  </TableCell>
                  <TableCell dir="ltr" className="text-start">
                    {h.server_name ?? "—"}
                  </TableCell>
                  <TableCell>{t(`proxy.tlsModes.${h.tls}`)}</TableCell>
                  {manage ? (
                    <TableCell>
                      <div className="flex flex-wrap justify-end gap-2">
                        {h.tls === "auto" && !h.verified ? (
                          <Button size="sm" variant="outline" onClick={() => act(`${h.id}/verify`, "POST")}>
                            {t("proxy.verify")}
                          </Button>
                        ) : null}
                        <EditProxy host={h} team={team.slug} onSaved={load} />
                        <Button size="sm" variant="outline" onClick={() => act(h.id, "PATCH", { enabled: !h.enabled })}>
                          {h.enabled ? t("proxy.disable") : t("proxy.enable")}
                        </Button>
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
  )
}

function AddProxyHost({ servers, onSaved }: { servers: Server[]; onSaved: () => Promise<void> }) {
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
      <DialogTrigger render={<Button />}>
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
            <FieldGroup className="gap-4">
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
                <Field orientation="horizontal">
                  <Checkbox id="px-skip" checked={skipVerify} onCheckedChange={(v) => setSkipVerify(v === true)} />
                  <FieldLabel htmlFor="px-skip" className="font-normal">
                    {t("proxy.skipVerify")}
                  </FieldLabel>
                </Field>
              ) : null}
            </FieldGroup>
          )}
          <FormError message={error} />
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={busy || servers.length === 0 || !hostname.trim() || !upstream.trim()}>
              {busy ? t("common.working") : t("proxy.save")}
            </Button>
          </DialogFooter>
        </form>
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
      <FieldGroup>
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
      </FieldGroup>
    </EditDialog>
  )
}
