"use client"

import {
  Badge,
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
  EmptyState,
  type DataTableColumn,
  Field,
  FieldDescription,
  FieldLabel,
  Input,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@fadymondy/nasaq/web"
import Link from "next/link"
import { useCallback, useEffect, useState, type FormEvent } from "react"
import { DatabaseIcon, PlusIcon, Trash2Icon } from "lucide-react"

import { ConfirmButton } from "@/components/confirm-button"
import { NasaqTable } from "@/components/lists/nasaq-table"

import { FormError, useErrorText } from "@/components/form-error"
import { ErrorBand, LoadingRows, SectionTitle } from "@/components/states"

import { apiFetch } from "@/lib/api"
import type { DataService, DataServiceKind } from "@/lib/databases"
import { isImported } from "@/lib/apps"
import { ImportedBadge } from "@/components/imported"
import { useI18n } from "@/lib/i18n"
import type { Server } from "@/lib/servers"
import { useTeam, useTeamBase } from "@/lib/team"

const KINDS: DataServiceKind[] = ["postgres", "mysql", "redis", "minio"]

export function statusVariant(status: string): "success" | "neutral" | "danger" | "outline" {
  if (status === "running") return "success"
  if (status === "failed") return "danger"
  if (status === "provisioning" || status === "new") return "neutral"
  return "outline"
}

/** The data services on one server. */
export function ServerDatabases({ serverId }: { serverId: string }) {
  const { t } = useI18n()
  const { team, manage } = useTeam()
  const errorText = useErrorText()
  const base = useTeamBase()
  const [services, setServices] = useState<DataService[] | null>(null)
  const [error, setError] = useState<string | null>(null)

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
        <EmptyState
          className="mt-3 rounded-lg border border-border bg-card"
          icon={DatabaseIcon}
          title={t("databases.emptyTitle")}
          description={manage ? t("databases.emptyHint") : t("common.readOnly")}
          actions={<>{add}</>}
        />
      ) : (
        <div className="mt-3">
          <NasaqTable
            data={services}
            rowId={(s) => s.id}
            label={t("nav.databases")}
            rowLabel={(s) => s.name}
            search={t("lists.searchDatabases")}
            empty={t("home.noMatch")}
            facets={[{ column: "kind", title: t("databases.kind"), options: [...new Set(services.map((s) => s.kind))].map((v) => ({ value: v, label: v })) }]}
            columns={[
              {
                id: "name",
                header: t("databases.name"),
                label: t("databases.name"),
                sortValue: (s) => s.name,
                searchValue: (s) => [s.name, s.kind, s.version, s.status, s.host, s.port].join(" "),
                cell: (s) => (
                  <span className="flex items-center gap-3">
                    <span className="flex size-8 shrink-0 items-center justify-center rounded-md bg-secondary text-muted-foreground">
                      <DatabaseIcon className="size-4" />
                    </span>
                    <Link href={`${base}/databases/${s.name}`} className="font-medium underline-offset-4 hover:underline">
                      {s.name}
                    </Link>
                  </span>
                ),
              },
              {
                id: "kind",
                header: t("databases.kind"),
                label: t("databases.kind"),
                sortValue: (s) => s.kind,
                filterValue: (s) => s.kind,
                cell: (s) => (
                  <Badge variant="neutral" dir="ltr">
                    {s.kind} {s.version}
                  </Badge>
                ),
              },
              {
                id: "status",
                header: t("databases.status"),
                label: t("databases.status"),
                sortValue: (s) => s.status,
                cell: (s) => (isImported(s) ? <ImportedBadge item={s} /> : <Badge variant={statusVariant(s.status)}>{t(`databases.statuses.${s.status}`)}</Badge>),
              },
              {
                id: "endpoint",
                header: t("databases.endpoint"),
                label: t("databases.endpoint"),
                className: "max-md:hidden",
                headerClassName: "max-md:hidden",
                cell: (s) => (
                  <span dir="ltr" className="font-mono text-caption text-muted-foreground">
                    {s.host && s.port ? `${s.host}:${s.port}` : "—"}
                  </span>
                ),
              },
              {
                id: "actions",
                header: <span className="sr-only">{t("common.actions")}</span>,
                label: t("common.actions"),
                cell: (s) =>
                  !manage ? null : (
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
                  ),
              },
            ] satisfies DataTableColumn<DataService>[]}
          />
        </div>
      )}
    </section>
  )
}

export function CreateDialog({
  teamSlug,
  serverId,
  onCreated,
  kinds = KINDS,
  storage = false,
}: {
  teamSlug: string
  serverId: string
  onCreated: () => void | Promise<void>
  /** The engines offered; the Databases page leaves MinIO to Storage. */
  kinds?: DataServiceKind[]
  /** Create S3 object storage (MinIO) instead of a database: own wording, no engine picker. */
  storage?: boolean
}) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [open, setOpen] = useState(false)
  const [servers, setServers] = useState<Server[]>([])
  const [name, setName] = useState("")
  const [server, setServer] = useState(serverId)
  const [kind, setKind] = useState<DataServiceKind>(kinds[0])
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
        {storage ? t("storage2.add") : t("databases.add")}
      </DialogTrigger>
      <DialogContent>
        <form onSubmit={submit}>
          <DialogHeader>
            <DialogTitle>{storage ? t("storage2.createTitle") : t("databases.createTitle")}</DialogTitle>
            <DialogDescription>{storage ? t("storage2.createHint") : t("databases.createHint")}</DialogDescription>
          </DialogHeader>
          <div className="flex w-full flex-col gap-5 py-4">
            <Field>
              <FieldLabel htmlFor="db-name">{t("databases.name")}</FieldLabel>
              <Input id="db-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="my-db" autoFocus />
            </Field>
            {kinds.length > 1 ? (
              <Field>
                <FieldLabel>{t("databases.kind")}</FieldLabel>
                <Select value={kind} onValueChange={(v) => v && setKind(v as DataServiceKind)}>
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {kinds.map((k) => (
                      <SelectItem key={k} value={k}>
                        {k}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
            ) : null}
            <Field>
              <FieldLabel>{t("databases.server")}</FieldLabel>
              <Select value={server} onValueChange={(v) => v && setServer(v)}>
                <SelectTrigger>
                  <SelectValue placeholder={t("databases.chooseServer")}>
                    {(v: string) => servers.find((s) => s.id === v)?.name ?? t("databases.chooseServer")}
                  </SelectValue>
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
                <Select
                  value={version === "pgvector-pg17" ? "pgvector-pg17" : "default"}
                  onValueChange={(v) => v && setVersion(v === "pgvector-pg17" ? "pgvector-pg17" : "")}
                >
                  <SelectTrigger>
                    <SelectValue>
                      {(v: string) => (v === "pgvector-pg17" ? t("databases.pgvectorOption") : t("databases.pgvectorDefault"))}
                    </SelectValue>
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
              <Input
                id="db-version"
                value={version}
                onChange={(e) => setVersion(e.target.value)}
                placeholder={t("databases.versionOptional")}
                dir="ltr"
              />
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
