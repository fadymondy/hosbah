"use client"

import { useCallback, useEffect, useState, type FormEvent } from "react"
import { NetworkIcon, PlusIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { ConfirmButton } from "@/components/confirm-button"
import { FormError, useErrorText } from "@/components/form-error"
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
import { Skeleton } from "@/components/ui/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { apiFetch } from "@/lib/api"
import { useDocumentTitle, useI18n } from "@/lib/i18n"
import type { ProxyHost, Server } from "@/lib/servers"
import { useTeam } from "@/lib/team"

const TLS_MODES = ["auto", "internal", "off"] as const

export default function ProxyHostsPage() {
  const { t } = useI18n()
  const { team, manage } = useTeam()
  useDocumentTitle(`${t("proxy.title")} — ${team.name}`)
  const errorText = useErrorText()
  const [hosts, setHosts] = useState<ProxyHost[] | null>(null)
  const [servers, setServers] = useState<Server[]>([])
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      const [p, s] = await Promise.all([
        apiFetch<{ proxy_hosts: ProxyHost[] }>(`/api/teams/${team.slug}/proxy-hosts`),
        apiFetch<{ servers: Server[] }>(`/api/teams/${team.slug}/servers`),
      ])
      setHosts(p.proxy_hosts ?? [])
      setServers((s.servers ?? []).filter((x) => x.status === "enrolled"))
    } catch (err) {
      setError(errorText(err))
      setHosts([])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [team.slug])

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
    <PageBody
      title={t("proxy.title")}
      description={t("proxy.description")}
      actions={manage ? <AddProxyHost servers={servers} onSaved={load} /> : null}
    >
      <FormError message={error} />
      {hosts === null ? (
        <Skeleton className="h-16 w-full" />
      ) : hosts.length === 0 ? (
        <Empty className="border">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <NetworkIcon />
            </EmptyMedia>
            <EmptyTitle>{t("proxy.emptyTitle")}</EmptyTitle>
            <EmptyDescription>{t("proxy.emptyHint")}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <div className="border">
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
              {hosts.map((h) => (
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
        </div>
      )}
    </PageBody>
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
      <DialogTrigger render={<Button size="sm" />}>
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
