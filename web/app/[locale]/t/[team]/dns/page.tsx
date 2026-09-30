"use client"

import { Badge, Button, Checkbox, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger, EmptyState, Field, FieldDescription, FieldLabel, Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useMemo, useState, type FormEvent } from "react"
import { GlobeIcon, PlusIcon, ServerIcon, ShieldAlertIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { ConfirmButton } from "@/components/confirm-button"
import { FormError, useErrorText } from "@/components/form-error"
import { ErrorBand, LoadingRows, TableBand } from "@/components/states"









import { apiFetch } from "@/lib/api"
import { DNS_ZONE_RECORD_TYPES, type DNSZone, type DNSZoneRecord, type DNSZoneRecordType } from "@/lib/dns-zones"
import { useDocumentTitle, useI18n } from "@/lib/i18n"
import type { Server } from "@/lib/servers"
import { useTeam } from "@/lib/team"

const MANAGED_KIND_LABEL: Record<string, string> = {
  app_domain: "dns.managedKind.appDomain",
  proxy_host: "dns.managedKind.proxyHost",
  mail: "dns.managedKind.mail",
}

export default function DNSZonesPage() {
  const { t } = useI18n()
  const { team, manage } = useTeam()
  useDocumentTitle(`${t("nav.dns")} — ${team.name}`)
  const errorText = useErrorText()

  const [zones, setZones] = useState<DNSZone[] | null>(null)
  const [zonesError, setZonesError] = useState<string | null>(null)
  const [zoneKey, setZoneKey] = useState<string | null>(null)

  const loadZones = useCallback(async () => {
    try {
      const res = await apiFetch<{ zones: DNSZone[] }>(`/api/teams/${team.slug}/dns/zones`)
      setZones(res.zones ?? [])
    } catch (err) {
      setZonesError(errorText(err))
      setZones([])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [team.slug])

  useEffect(() => {
    void loadZones()
  }, [loadZones])

  useEffect(() => {
    if (zoneKey || !zones?.length) return
    const z = zones[0]
    setZoneKey(`${z.credential_id}:${z.name}`)
  }, [zones, zoneKey])

  const zone = useMemo(() => zones?.find((z) => `${z.credential_id}:${z.name}` === zoneKey) ?? null, [zones, zoneKey])

  return (
    <PageBody title={t("nav.dns")} description={t("dns.description")} bleed>
      <ErrorBand message={zonesError} />
      {zones === null ? (
        <LoadingRows />
      ) : zones.length === 0 ? (
        <EmptyState className="border-b" icon={GlobeIcon} title={t("dns.emptyTitle")} description={t("dns.emptyHint")} />
      ) : (
        <div className="flex flex-col gap-4 p-4">
          <Field className="max-w-sm">
            <FieldLabel htmlFor="dns-zone">{t("dns.zone")}</FieldLabel>
            <Select value={zoneKey ?? undefined} onValueChange={(v) => v && setZoneKey(v as string)}>
              <SelectTrigger id="dns-zone" className="w-full">
                <SelectValue>
                  {() => (zone ? `${zone.name} (${zone.credential_name})` : t("dns.chooseZone"))}
                </SelectValue>
              </SelectTrigger>
              <SelectContent>
                {zones.map((z) => (
                  <SelectItem key={`${z.credential_id}:${z.name}`} value={`${z.credential_id}:${z.name}`}>
                    <span dir="ltr">
                      {z.name} ({z.credential_name})
                    </span>
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
          {zone ? <ZoneRecords zone={zone} manage={manage} /> : null}
        </div>
      )}
    </PageBody>
  )
}

function ZoneRecords({ zone, manage }: { zone: DNSZone; manage: boolean }) {
  const { t } = useI18n()
  const { team } = useTeam()
  const errorText = useErrorText()
  const [records, setRecords] = useState<DNSZoneRecord[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  const recordsPath = `/api/teams/${team.slug}/dns/zones/${encodeURIComponent(zone.credential_id)}/${encodeURIComponent(zone.name)}/records`

  const load = useCallback(async () => {
    setRecords(null)
    try {
      const res = await apiFetch<{ records: DNSZoneRecord[] }>(recordsPath)
      setRecords(res.records ?? [])
    } catch (err) {
      setError(errorText(err))
      setRecords([])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [recordsPath])

  useEffect(() => {
    void load()
  }, [load])

  return (
    <>
      <div className="flex items-center justify-between">
        <h2 className="text-sm font-medium text-muted-foreground">{t("dns.records")}</h2>
        {manage ? <RecordDialog recordsPath={recordsPath} onSaved={load} /> : null}
      </div>
      <ErrorBand message={error} />
      {records === null ? (
        <LoadingRows />
      ) : records.length === 0 ? (
        <EmptyState className="border-b" title={t("dns.noRecords")} />
      ) : (
        <TableBand>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("dns.type")}</TableHead>
                <TableHead>{t("dns.name")}</TableHead>
                <TableHead>{t("dns.content")}</TableHead>
                <TableHead>{t("dns.ttl")}</TableHead>
                <TableHead>{t("dns.proxied")}</TableHead>
                <TableHead>{t("dns.managed")}</TableHead>
                <TableHead className="text-end">{t("common.actions")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {records.map((r) => (
                <TableRow key={r.id}>
                  <TableCell>
                    <Badge variant="outline" dir="ltr">
                      {r.type}
                    </Badge>
                  </TableCell>
                  <TableCell dir="ltr" className="font-mono text-xs">
                    {r.name}
                  </TableCell>
                  <TableCell dir="ltr" className="max-w-[24ch] truncate font-mono text-xs text-muted-foreground" title={r.content}>
                    {r.content}
                    {r.type === "MX" && r.priority ? ` (${r.priority})` : null}
                  </TableCell>
                  <TableCell dir="ltr" className="text-sm text-muted-foreground">
                    {r.ttl <= 1 ? t("dns.auto") : r.ttl}
                  </TableCell>
                  <TableCell>{r.proxied ? <Badge variant="outline">{t("dns.proxiedOn")}</Badge> : null}</TableCell>
                  <TableCell>
                    {r.managed_kind ? (
                      <Badge variant="outline" className="gap-1 text-nq-warning-text">
                        <ShieldAlertIcon className="size-3" />
                        {t(MANAGED_KIND_LABEL[r.managed_kind] ?? "dns.managedKind.other")}
                      </Badge>
                    ) : r.managed_by_hosbah ? (
                      <Badge variant="outline">{t("dns.managedByHosbah")}</Badge>
                    ) : null}
                  </TableCell>
                  <TableCell>
                    <div className="flex justify-end gap-2">
                      {manage ? (
                        <>
                          <RecordDialog recordsPath={recordsPath} record={r} onSaved={load} />
                          <ConfirmButton
                            label={t("dns.delete")}
                            title={t("dns.deleteTitle")}
                            description={
                              r.managed_kind
                                ? t("dns.deleteManagedHint", { name: r.name, kind: t(MANAGED_KIND_LABEL[r.managed_kind] ?? "dns.managedKind.other") })
                                : t("dns.deleteHint", { name: r.name })
                            }
                            confirmLabel={t("dns.delete")}
                            onConfirm={async () => {
                              try {
                                await apiFetch(`${recordsPath}/${r.id}`, { method: "DELETE" })
                              } catch (err) {
                                throw new Error(errorText(err))
                              }
                              await load()
                            }}
                          />
                        </>
                      ) : null}
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </TableBand>
      )}
    </>
  )
}

/** Create (record undefined) or change (record given) one record of the zone. */
function RecordDialog({
  recordsPath,
  record,
  onSaved,
}: {
  recordsPath: string
  record?: DNSZoneRecord
  onSaved: () => Promise<void>
}) {
  const { t } = useI18n()
  const { team } = useTeam()
  const errorText = useErrorText()
  const [open, setOpen] = useState(false)
  const [type, setType] = useState<DNSZoneRecordType>(record?.type ?? "A")
  const [name, setName] = useState(record?.name ?? "")
  const [content, setContent] = useState(record?.content ?? "")
  const [ttl, setTtl] = useState(record?.ttl && record.ttl > 1 ? String(record.ttl) : "")
  const [priority, setPriority] = useState(record?.priority ? String(record.priority) : "")
  const [proxied, setProxied] = useState(record?.proxied ?? false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [servers, setServers] = useState<Server[] | null>(null)

  const isEdit = Boolean(record)

  useEffect(() => {
    if (!open || servers !== null) return
    apiFetch<{ servers: Server[] }>(`/api/teams/${team.slug}/servers`)
      .then((res) => setServers(res.servers ?? []))
      .catch(() => setServers([]))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])

  function reset() {
    setType(record?.type ?? "A")
    setName(record?.name ?? "")
    setContent(record?.content ?? "")
    setTtl(record?.ttl && record.ttl > 1 ? String(record.ttl) : "")
    setPriority(record?.priority ? String(record.priority) : "")
    setProxied(record?.proxied ?? false)
    setError(null)
  }

  const canPointToServer = type === "A" || type === "AAAA"
  const validName = isEdit || name.trim() !== ""
  const validContent = content.trim() !== ""
  const validPriority = type !== "MX" || (priority.trim() !== "" && Number(priority) > 0)
  const valid = validName && validContent && validPriority

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const body: Record<string, unknown> = {
        content: content.trim(),
        ttl: ttl.trim() ? Number(ttl) : undefined,
        priority: type === "MX" ? Number(priority) : undefined,
        proxied,
      }
      if (!isEdit) {
        body.type = type
        body.name = name.trim()
      }
      await apiFetch(isEdit ? `${recordsPath}/${record!.id}` : recordsPath, {
        method: isEdit ? "PATCH" : "POST",
        body,
      })
      setOpen(false)
      await onSaved()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        setOpen(o)
        if (o) reset()
      }}
    >
      <DialogTrigger render={<Button variant={isEdit ? "secondary" : "primary"} size={isEdit ? "sm" : "md"} />}>
        {isEdit ? t("dns.edit") : (
          <>
            <PlusIcon />
            {t("dns.addRecord")}
          </>
        )}
      </DialogTrigger>
      <DialogContent className="max-h-[90dvh] overflow-y-auto">
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{isEdit ? t("dns.editRecord") : t("dns.addRecord")}</DialogTitle>
            <DialogDescription>{isEdit ? t("dns.editRecordHint") : t("dns.addRecordHint")}</DialogDescription>
          </DialogHeader>
          {record?.managed_kind ? (
            <p className="flex items-center gap-2 rounded-md border border-nq-warning/40 bg-nq-warning/10 p-2 text-sm text-nq-warning-text" dir="auto">
              <ShieldAlertIcon className="size-4 shrink-0" />
              {t("dns.editManagedWarning", { kind: t(MANAGED_KIND_LABEL[record.managed_kind] ?? "dns.managedKind.other") })}
            </p>
          ) : null}
          <div className="flex w-full flex-col gap-4">
            {!isEdit ? (
              <>
                <Field>
                  <FieldLabel htmlFor="dns-type">{t("dns.type")}</FieldLabel>
                  <Select value={type} onValueChange={(v) => v && setType(v as DNSZoneRecordType)}>
                    <SelectTrigger id="dns-type" className="w-full">
                      <SelectValue>{() => type}</SelectValue>
                    </SelectTrigger>
                    <SelectContent>
                      {DNS_ZONE_RECORD_TYPES.map((v) => (
                        <SelectItem key={v} value={v}>
                          <span dir="ltr">{v}</span>
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </Field>
                <Field>
                  <FieldLabel htmlFor="dns-name">{t("dns.name")}</FieldLabel>
                  <Input id="dns-name" dir="ltr" required maxLength={253} value={name} onChange={(e) => setName(e.target.value)} />
                  <FieldDescription>{t("dns.nameHint")}</FieldDescription>
                </Field>
              </>
            ) : null}
            <Field>
              <div className="flex items-center justify-between">
                <FieldLabel htmlFor="dns-content">{t("dns.content")}</FieldLabel>
                {canPointToServer && servers?.length ? (
                  <Select onValueChange={(v) => v && setContent(String(v))}>
                    <SelectTrigger className="h-7 w-auto gap-1 text-xs">
                      <ServerIcon className="size-3" />
                      <SelectValue>{() => t("dns.pointToServer")}</SelectValue>
                    </SelectTrigger>
                    <SelectContent>
                      {servers
                        .filter((s) => s.ip_address)
                        .map((s) => (
                          <SelectItem key={s.id} value={s.ip_address as string}>
                            <span dir="ltr">
                              {s.name} — {s.ip_address}
                            </span>
                          </SelectItem>
                        ))}
                    </SelectContent>
                  </Select>
                ) : null}
              </div>
              <Input id="dns-content" dir="ltr" required maxLength={2000} value={content} onChange={(e) => setContent(e.target.value)} />
            </Field>
            <div className="grid grid-cols-2 gap-4">
              <Field>
                <FieldLabel htmlFor="dns-ttl">{t("dns.ttl")}</FieldLabel>
                <Input
                  id="dns-ttl"
                  dir="ltr"
                  type="number"
                  min={0}
                  placeholder={t("dns.auto")}
                  value={ttl}
                  onChange={(e) => setTtl(e.target.value)}
                />
              </Field>
              {type === "MX" ? (
                <Field>
                  <FieldLabel htmlFor="dns-priority">{t("dns.priority")}</FieldLabel>
                  <Input
                    id="dns-priority"
                    dir="ltr"
                    type="number"
                    min={0}
                    required
                    value={priority}
                    onChange={(e) => setPriority(e.target.value)}
                  />
                </Field>
              ) : null}
            </div>
            <Field className="flex-row items-center">
              <Checkbox id="dns-proxied" checked={proxied} onCheckedChange={(v) => setProxied(v === true)} />
              <FieldLabel htmlFor="dns-proxied" className="font-normal">
                {t("dns.proxied")}
              </FieldLabel>
            </Field>
          </div>
          <FormError message={error} />
          <DialogFooter>
            <Button type="button" variant="secondary" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button variant="primary" type="submit" disabled={busy || !valid}>
              {busy ? t("common.working") : isEdit ? t("dns.save") : t("dns.addRecord")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
