"use client"

import { Badge, Button, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger, EmptyState, Field, FieldDescription, FieldLabel, Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@fadymondy/nasaq/web"
import Link from "next/link"
import { useCallback, useEffect, useState, type FormEvent } from "react"
import { DatabaseIcon, PlusIcon, Trash2Icon } from "lucide-react"

import { ConfirmButton } from "@/components/confirm-button"
import { ListSearch, matches } from "@/components/server-tabs/list-tools"

import { FormError, useErrorText } from "@/components/form-error"
import { ErrorBand, LoadingRows, SectionTitle, TableBand } from "@/components/states"








import { apiFetch } from "@/lib/api"
import type { DataService, DataServiceKind } from "@/lib/databases"
import { isImported } from "@/lib/apps"
import { ImportedBadge } from "@/components/imported"
import { useI18n } from "@/lib/i18n"
import type { Server } from "@/lib/servers"
import { useTeam } from "@/lib/team"

const KINDS: DataServiceKind[] = ["postgres", "mysql", "redis", "minio"]

export function statusVariant(status: string): "success" | "neutral" | "danger" | "outline" {
  if (status === "running") return "success"
  if (status === "failed") return "danger"
  if (status === "provisioning" || status === "new") return "neutral"
  return "outline"
}

/** The data services on one server. */
export function ServerDatabases({ serverId }: { serverId: string }) {
  const { t, locale } = useI18n()
  const { team, manage } = useTeam()
  const errorText = useErrorText()
  const base = `/${locale}/t/${team.slug}`
  const [services, setServices] = useState<DataService[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [q, setQ] = useState("")
  const shown = (services ?? []).filter((s) => matches(q, s.name, s.kind, s.version, s.status, s.host, s.port))

  const load = useCallback(async () => {
    try {
      const res = await apiFetch<{ services: DataService[] }>(`/api/teams/${team.slug}/data-services`)
      setServices((res.services ?? []).filter((s) => s.server_id === serverId))
      setError(null)
    } catch (err) {
      setError(errorText(err))
      setServices((s) => s ?? [])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [team.slug, serverId])

  useEffect(() => {
    void load()
    const id = setInterval(load, 8_000)
    return () => clearInterval(id)
  }, [load])

  const add = manage ? <CreateDialog teamSlug={team.slug} serverId={serverId} onCreated={load} /> : null

  return (
    <section aria-labelledby="tab-h">
      <SectionTitle id="tab-h" title={t("nav.databases")} hint={t("databases.description")} action={add} />
      <ErrorBand message={error} />
      {services === null ? (
        <LoadingRows />
      ) : services.length === 0 ? (
        <EmptyState className="border-b" icon={DatabaseIcon} title={t("databases.emptyTitle")} description={manage ? t("databases.emptyHint") : t("common.readOnly")} actions={<>{add}</>} />
      ) : (
        <>
        <ListSearch value={q} onChange={setQ} placeholder={t("lists.searchDatabases")} shown={shown.length} total={services.length} />
        {shown.length === 0 ? <p className="border-b border-border px-6 py-8 text-center text-sm text-muted-foreground">{t("home.noMatch")}</p> : null}
        <TableBand>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("databases.name")}</TableHead>
                <TableHead>{t("databases.kind")}</TableHead>
                <TableHead>{t("databases.status")}</TableHead>
                <TableHead>{t("databases.endpoint")}</TableHead>
                {manage ? <TableHead className="text-end">{t("common.actions")}</TableHead> : null}
              </TableRow>
            </TableHeader>
            <TableBody>
              {shown.map((s) => (
                <TableRow key={s.id}>
                  <TableCell>
                    <Link href={`${base}/databases/${s.name}`} className="font-medium hover:underline">
                      {s.name}
                    </Link>
                  </TableCell>
                  <TableCell dir="ltr">
                    {s.kind} {s.version}
                  </TableCell>
                  <TableCell>
                    {isImported(s) ? (
                      <ImportedBadge item={s} />
                    ) : (
                      <Badge variant={statusVariant(s.status)}>{t(`databases.statuses.${s.status}`)}</Badge>
                    )}
                  </TableCell>
                  <TableCell dir="ltr" className="text-muted-foreground">
                    {s.host && s.port ? `${s.host}:${s.port}` : "—"}
                  </TableCell>
                  {manage ? (
                    <TableCell>
                      <div className="flex flex-wrap justify-end gap-2">
                        <Button size="sm" variant="secondary" nativeButton={false} render={<Link href={`${base}/databases/${s.name}`} />}>
                          {t("lists.manage")}
                        </Button>
                        {isImported(s) ? null : (
                          <ConfirmButton
                            icon={<Trash2Icon />}
                            label={t("common.delete")}
                            title={t("lists.deleteDatabase", { name: s.name })}
                            description={t("lists.deleteDatabaseBody")}
                            confirmLabel={t("common.delete")}
                            onConfirm={async () => {
                              try {
                                await apiFetch(`/api/teams/${team.slug}/data-services/${s.name}`, { method: "DELETE" })
                              } catch (err) {
                                throw new Error(errorText(err))
                              }
                              await load()
                            }}
                          />
                        )}
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

export function CreateDialog({ teamSlug, serverId, onCreated }: { teamSlug: string; serverId: string; onCreated: () => void | Promise<void> }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [open, setOpen] = useState(false)
  const [servers, setServers] = useState<Server[]>([])
  const [name, setName] = useState("")
  const [server, setServer] = useState(serverId)
  const [kind, setKind] = useState<DataServiceKind>("postgres")
  const [version, setVersion] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (!open) return
    apiFetch<{ servers: Server[] }>(`/api/teams/${teamSlug}/servers`)
      .then((r) => setServers(r.servers ?? []))
      .catch(() => setServers([]))
  }, [open, teamSlug])

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    if (!name || !server) return
    setBusy(true)
    setError(null)
    try {
      await apiFetch(`/api/teams/${teamSlug}/data-services`, {
        method: "POST",
        body: { name, server, kind, version: version || undefined },
      })
      setOpen(false)
      setName("")
      setVersion("")
      await onCreated()
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
        {t("databases.add")}
      </DialogTrigger>
      <DialogContent>
        <form onSubmit={submit}>
          <DialogHeader>
            <DialogTitle>{t("databases.createTitle")}</DialogTitle>
            <DialogDescription>{t("databases.createHint")}</DialogDescription>
          </DialogHeader>
          <div className="flex w-full flex-col gap-5 py-4">
            <Field>
              <FieldLabel htmlFor="db-name">{t("databases.name")}</FieldLabel>
              <Input id="db-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="my-db" autoFocus />
            </Field>
            <Field>
              <FieldLabel>{t("databases.kind")}</FieldLabel>
              <Select value={kind} onValueChange={(v) => v && setKind(v as DataServiceKind)}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {KINDS.map((k) => (
                    <SelectItem key={k} value={k}>
                      {k}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            <Field>
              <FieldLabel>{t("databases.server")}</FieldLabel>
              <Select value={server} onValueChange={(v) => v && setServer(v)}>
                <SelectTrigger>
                  <SelectValue placeholder={t("databases.chooseServer")}>{(v: string) => servers.find((s) => s.id === v)?.name ?? t("databases.chooseServer")}</SelectValue>
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
            {kind === "postgres" ? (
              <Field>
                <FieldLabel>{t("databases.pgvectorLabel")}</FieldLabel>
                <Select value={version === "pgvector-pg17" ? "pgvector-pg17" : "default"} onValueChange={(v) => v && setVersion(v === "pgvector-pg17" ? "pgvector-pg17" : "")}>
                  <SelectTrigger>
                    <SelectValue>{(v: string) => (v === "pgvector-pg17" ? t("databases.pgvectorOption") : t("databases.pgvectorDefault"))}</SelectValue>
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="default">{t("databases.pgvectorDefault")}</SelectItem>
                    <SelectItem value="pgvector-pg17">{t("databases.pgvectorOption")}</SelectItem>
                  </SelectContent>
                </Select>
                <FieldDescription>{t("databases.pgvectorHint")}</FieldDescription>
              </Field>
            ) : null}
            <Field>
              <FieldLabel htmlFor="db-version">{t("databases.version")}</FieldLabel>
              <Input id="db-version" value={version} onChange={(e) => setVersion(e.target.value)} placeholder={t("databases.versionOptional")} dir="ltr" />
              <FieldDescription>{t("databases.versionHint")}</FieldDescription>
            </Field>
            <FormError message={error} />
          </div>
          <DialogFooter>
            <Button variant="primary" type="submit" disabled={busy || !name || !server}>
              {busy ? t("common.working") : t("databases.create")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
