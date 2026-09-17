"use client"

import Link from "next/link"
import { useCallback, useEffect, useState, type FormEvent } from "react"
import { DatabaseIcon, PlusIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { FormError, useErrorText } from "@/components/form-error"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
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
import type { DataService, DataServiceKind } from "@/lib/databases"
import { useDocumentTitle, useI18n } from "@/lib/i18n"
import type { Server } from "@/lib/servers"
import { useTeam } from "@/lib/team"

const KINDS: DataServiceKind[] = ["postgres", "mysql", "redis"]

export function statusVariant(status: string): "default" | "secondary" | "destructive" | "outline" {
  if (status === "running") return "default"
  if (status === "failed") return "destructive"
  if (status === "provisioning" || status === "new") return "secondary"
  return "outline"
}

export default function DatabasesPage() {
  const { t, locale } = useI18n()
  const { team, manage } = useTeam()
  useDocumentTitle(`${t("nav.databases")} — ${team.name}`)
  const errorText = useErrorText()
  const base = `/${locale}/t/${team.slug}`
  const [services, setServices] = useState<DataService[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      const res = await apiFetch<{ services: DataService[] }>(`/api/teams/${team.slug}/data-services`)
      setServices(res.services ?? [])
      setError(null)
    } catch (err) {
      setError(errorText(err))
      setServices((s) => s ?? [])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [team.slug])

  useEffect(() => {
    void load()
    const id = setInterval(load, 8_000)
    return () => clearInterval(id)
  }, [load])

  const add = manage ? <CreateDialog teamSlug={team.slug} onCreated={load} /> : null

  return (
    <PageBody title={t("nav.databases")} description={t("databases.description")} actions={add}>
      <FormError message={error} />
      {services === null ? (
        <div className="flex flex-col gap-2">
          <Skeleton className="h-8 w-full" />
          <Skeleton className="h-8 w-full" />
        </div>
      ) : services.length === 0 ? (
        <Empty className="border">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <DatabaseIcon />
            </EmptyMedia>
            <EmptyTitle>{t("databases.emptyTitle")}</EmptyTitle>
            <EmptyDescription>{manage ? t("databases.emptyHint") : t("common.readOnly")}</EmptyDescription>
          </EmptyHeader>
          {add}
        </Empty>
      ) : (
        <div className="border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("databases.name")}</TableHead>
                <TableHead>{t("databases.kind")}</TableHead>
                <TableHead>{t("databases.status")}</TableHead>
                <TableHead>{t("databases.endpoint")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {services.map((s) => (
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
                    <Badge variant={statusVariant(s.status)}>{t(`databases.statuses.${s.status}`)}</Badge>
                  </TableCell>
                  <TableCell dir="ltr" className="text-muted-foreground">
                    {s.host && s.port ? `${s.host}:${s.port}` : "—"}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </PageBody>
  )
}

function CreateDialog({ teamSlug, onCreated }: { teamSlug: string; onCreated: () => void | Promise<void> }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [open, setOpen] = useState(false)
  const [servers, setServers] = useState<Server[]>([])
  const [name, setName] = useState("")
  const [server, setServer] = useState("")
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
      <DialogTrigger render={<Button size="sm" />}>
        <PlusIcon />
        {t("databases.add")}
      </DialogTrigger>
      <DialogContent>
        <form onSubmit={submit}>
          <DialogHeader>
            <DialogTitle>{t("databases.createTitle")}</DialogTitle>
            <DialogDescription>{t("databases.createHint")}</DialogDescription>
          </DialogHeader>
          <FieldGroup className="py-4">
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
                  <SelectValue placeholder={t("databases.chooseServer")} />
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
              <FieldLabel htmlFor="db-version">{t("databases.version")}</FieldLabel>
              <Input id="db-version" value={version} onChange={(e) => setVersion(e.target.value)} placeholder={t("databases.versionOptional")} dir="ltr" />
              <FieldDescription>{t("databases.versionHint")}</FieldDescription>
            </Field>
            <FormError message={error} />
          </FieldGroup>
          <DialogFooter>
            <Button type="submit" disabled={busy || !name || !server}>
              {busy ? t("common.working") : t("databases.create")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
