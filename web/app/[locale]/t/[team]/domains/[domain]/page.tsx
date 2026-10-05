"use client"

import {
  Button,
  DataTable,
  DnsManagement,
  EmptyState,
  Status,
  useDataTable,
  type DataTableColumn,
  type DnsRecordInput,
} from "@fadymondy/nasaq/web"
import Link from "next/link"
import { useParams } from "next/navigation"
import { useCallback, useEffect, useMemo, useState } from "react"
import { ArrowLeftIcon, ExternalLinkIcon, GlobeIcon, KeyRoundIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { useTeamDomains, useTlsOf, type Host } from "@/components/domains/data"
import { DomainIssues, HostPath } from "@/components/domains/parts"
import { useErrorText } from "@/components/form-error"
import { ErrorBand, LoadingRows } from "@/components/states"
import { apiFetch } from "@/lib/api"
import { DNS_ZONE_RECORD_TYPES, type DNSZone, type DNSZoneRecord } from "@/lib/dns-zones"
import { useDocumentTitle, useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

/**
 * One main domain: the hostnames Hosbah serves under it (each with its server → app path and TLS)
 * and, when a team Cloudflare credential holds the zone, its full DNS records to manage.
 */
export default function DomainPage() {
  const { domain: raw } = useParams<{ domain: string }>()
  const name = decodeURIComponent(String(raw)).toLowerCase()
  const { t, locale } = useI18n()
  const { team, manage } = useTeam()
  useDocumentTitle(`${name} — ${team.name}`)
  const base = `/${locale}/t/${team.slug}`
  const { domains, servers, certs, error, setError, load, health } = useTeamDomains(team.slug, base)
  const tlsOf = useTlsOf(certs)
  const errorText = useErrorText()
  const domain = domains?.find((d) => d.name === name) ?? null
  const hosts = useMemo(() => domain?.hosts ?? [], [domain])

  async function verify(h: Host) {
    try {
      await apiFetch(h.verifyPath, { method: "POST" })
      await load()
    } catch (err) {
      setError(errorText(err))
    }
  }

  const columns = useMemo<DataTableColumn<Host>[]>(
    () => [
      {
        id: "hostname",
        header: t("proxy.hostname"),
        label: t("proxy.hostname"),
        sortValue: (h) => h.hostname,
        cell: (h) => (
          <span className="flex min-w-0 flex-col">
            <bdi dir="ltr" className="truncate text-start font-medium text-foreground">
              {h.hostname}
            </bdi>
            <span className="text-caption text-muted-foreground">{t(`domains2.kinds.${h.kind}`)}</span>
          </span>
        ),
      },
      {
        id: "path",
        header: t("domains2.connectedTo"),
        label: t("domains2.connectedTo"),
        sortValue: (h) => h.target,
        cell: (h) => <HostPath host={h} servers={servers} link />,
      },
      {
        id: "tls",
        header: t("domains2.tls"),
        label: t("domains2.tls"),
        sortValue: (h) => Number(health(h) !== "attention"),
        cell: (h) => {
          const [tone, label] = tlsOf(h)
          return <Status tone={tone}>{label}</Status>
        },
      },
    ],
    [t, servers, health, tlsOf],
  )
  const table = useDataTable({
    data: hosts,
    columns,
    getRowId: (h) => h.key,
    pageSize: 50,
    defaultSort: { id: "hostname", direction: "asc" },
  })

  return (
    <PageBody
      eyebrow={
        <Link href={`${base}/domains`} className="inline-flex items-center gap-1 hover:text-foreground hover:underline">
          <ArrowLeftIcon aria-hidden className="size-3.5 rtl:rotate-180" />
          {t("nav.domains")}
        </Link>
      }
      title={
        <span className="inline-flex items-center gap-2">
          <bdi dir="ltr">{name}</bdi>
          {domain ? <DomainIssues hosts={hosts} certs={certs} health={health} manage={manage} onVerify={(h) => void verify(h)} /> : null}
        </span>
      }
      description={domain?.zone ? t("domains2.zoneVia", { name: domain.zone.credential_name }) : undefined}
      actions={
        <Button variant="secondary" nativeButton={false} render={<a href={`https://${name}`} target="_blank" rel="noreferrer" />}>
          <ExternalLinkIcon />
          {t("domains2.visit")}
        </Button>
      }
      bleed
      divider={false}
    >
      <ErrorBand message={error} />
      {domains === null ? (
        <LoadingRows rows={4} />
      ) : !domain ? (
        <div className="px-6 pb-6">
          <EmptyState icon={GlobeIcon} title={t("domains2.notFound")} description={t("domains2.notFoundBody", { name })} />
        </div>
      ) : (
        <div className="flex flex-col gap-8 px-6 pb-6">
          {hosts.length ? (
            <section className="flex flex-col gap-3">
              <h2 className="text-heading-sm font-medium text-foreground">{t("domains2.connections")}</h2>
              <DataTable
                table={table}
                label={t("domains2.connections")}
                rowLabel={(h) => h.hostname}
                rowActions={(h) => [
                  ...(!h.verified && manage ? [{ id: "verify", label: t("proxy.verify"), onSelect: () => void verify(h) }] : []),
                  { id: "manage", label: t("domains2.manage"), onSelect: () => (window.location.href = h.manageHref) },
                ]}
              />
            </section>
          ) : null}
          <section className="flex flex-col gap-3">
            {domain.zone ? (
              <ZoneDNS team={team.slug} zone={domain.zone} manage={manage} onChanged={load} />
            ) : (
              <EmptyState
                icon={KeyRoundIcon}
                title={t("domains2.noZoneTitle")}
                description={t("domains2.noZoneBody", { name })}
                actions={
                  manage ? (
                    <Button variant="secondary" nativeButton={false} render={<Link href={`${base}/credentials`} />}>
                      {t("domains2.connectCloudflare")}
                    </Button>
                  ) : undefined
                }
              />
            )}
          </section>
        </div>
      )}
    </PageBody>
  )
}

/** The zone's records in Nasaq's DnsManagement, read and written through the team's Cloudflare credential. */
function ZoneDNS({ team, zone, manage, onChanged }: { team: string; zone: DNSZone; manage: boolean; onChanged: () => void }) {
  const errorText = useErrorText()
  const [records, setRecords] = useState<DNSZoneRecord[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const path = `/api/teams/${team}/dns/zones/${encodeURIComponent(zone.credential_id)}/${encodeURIComponent(zone.name)}/records`

  const load = useCallback(async () => {
    try {
      const res = await apiFetch<{ records: DNSZoneRecord[] }>(path)
      setRecords(res.records ?? [])
      setError(null)
    } catch (err) {
      setError(errorText(err))
      setRecords((x) => x ?? [])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path])

  useEffect(() => {
    void load()
  }, [load])

  async function run(fn: () => Promise<unknown>) {
    try {
      await fn()
      await load()
      onChanged()
    } catch (err) {
      return { error: errorText(err) }
    }
  }

  const save = (r: DnsRecordInput) =>
    run(() =>
      r.id
        ? apiFetch(`${path}/${r.id}`, {
            method: "PATCH",
            body: { content: r.content, ttl: r.ttl, priority: r.priority, proxied: r.proxied },
          })
        : apiFetch(path, {
            method: "POST",
            body: { type: r.type, name: r.name, content: r.content, ttl: r.ttl, priority: r.priority, proxied: r.proxied },
          }),
    )

  return (
    <>
      <ErrorBand message={error} />
      <DnsManagement
        zone={zone.name}
        records={records ?? []}
        loading={records === null}
        types={DNS_ZONE_RECORD_TYPES}
        onSave={save}
        onDelete={manage ? (id) => run(() => apiFetch(`${path}/${id}`, { method: "DELETE" })) : undefined}
        onToggleProxy={manage ? (id, proxied) => run(() => apiFetch(`${path}/${id}`, { method: "PATCH", body: { proxied } })) : undefined}
      />
    </>
  )
}
