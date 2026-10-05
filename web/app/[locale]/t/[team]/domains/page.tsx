"use client"

import {
  Button,
  DataTable,
  DataTableFacetFilter,
  DataTablePagination,
  DataTableSearch,
  DataTableToolbar,
  EmptyState,
  Status,
  useDataTable,
  type DataTableColumn,
  type DataTableRowAction,
} from "@fadymondy/nasaq/web"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { useMemo } from "react"
import { BoxesIcon, ExternalLinkIcon, GlobeIcon, SettingsIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { useTeamDomains, type Domain, type Host } from "@/components/domains/data"
import { DomainIssues, HostPath } from "@/components/domains/parts"
import { AddProxyHost } from "@/components/server-tabs/proxy"
import { ErrorBand, LoadingRows } from "@/components/states"
import { apiFetch } from "@/lib/api"
import { useDocumentTitle, useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

/**
 * Every domain the team runs, one row per main domain: where it is connected (server → app) and the
 * TLS health of its hostnames. Problems open in a popover; a row opens the domain with its DNS.
 */
export default function DomainsPage() {
  const { t, locale } = useI18n()
  const { team, manage } = useTeam()
  useDocumentTitle(`${t("nav.domains")} — ${team.name}`)
  const router = useRouter()
  const base = `/${locale}/t/${team.slug}`
  const { domains, servers, certs, error, setError, load, health } = useTeamDomains(team.slug, base)

  async function verify(h: Host) {
    try {
      await apiFetch(h.verifyPath, { method: "POST" })
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  const columns = useMemo<DataTableColumn<Domain>[]>(
    () => [
      {
        id: "domain",
        header: t("domains2.domain"),
        label: t("domains2.domain"),
        sortValue: (d) => d.name,
        searchValue: (d) => [d.name, ...d.hosts.flatMap((h) => [h.hostname, h.target])].join(" "),
        cell: (d) => (
          <span className="flex min-w-0 items-center gap-3">
            <span className="flex size-8 shrink-0 items-center justify-center rounded-control border border-border bg-secondary text-muted-foreground">
              <GlobeIcon aria-hidden className="size-4" />
            </span>
            <span className="flex min-w-0 flex-col">
              <bdi dir="ltr" className="truncate text-start font-medium text-foreground">
                {d.name}
              </bdi>
              <span className="text-caption text-muted-foreground">
                {d.hosts.length ? t("domains2.hostCount", { n: d.hosts.length }) : t("domains2.dnsOnly")}
                {d.zone ? ` · ${d.zone.credential_name}` : ""}
              </span>
            </span>
            <DomainIssues hosts={d.hosts} certs={certs} health={health} manage={manage} onVerify={(h) => void verify(h)} />
          </span>
        ),
      },
      {
        id: "path",
        header: t("domains2.connectedTo"),
        label: t("domains2.connectedTo"),
        sortValue: (d) => d.hosts[0]?.target ?? "",
        cell: (d) => {
          // One path per distinct server → target; the rest are summarised.
          const seen = new Map<string, Host>()
          for (const h of d.hosts) if (!seen.has(`${h.serverId}/${h.target}`)) seen.set(`${h.serverId}/${h.target}`, h)
          const paths = [...seen.values()]
          if (!paths.length) return <span className="text-body-sm text-muted-foreground">{t("domains2.notConnected")}</span>
          return (
            <span className="flex min-w-0 flex-col gap-0.5">
              <HostPath host={paths[0]} servers={servers} />
              {paths.length > 1 ? (
                <span className="text-caption text-muted-foreground">{t("serverHere.more", { n: paths.length - 1 })}</span>
              ) : null}
            </span>
          )
        },
      },
      {
        id: "tls",
        header: t("domains2.tls"),
        label: t("domains2.tls"),
        sortValue: (d) => Number(d.health !== "attention"),
        filterValue: (d) => d.health,
        cell: (d) =>
          !d.hosts.length ? (
            <span className="text-body-sm text-muted-foreground">—</span>
          ) : d.health === "attention" ? (
            <Status tone="warning">{t("domains2.needsAttention")}</Status>
          ) : d.health === "off" ? (
            <Status tone="neutral">{t("proxy.disabled")}</Status>
          ) : (
            <Status tone="success">{t("domains2.healthy")}</Status>
          ),
      },
      {
        id: "activity",
        header: t("lists.sortActivity"),
        label: t("lists.sortActivity"),
        sortValue: (d) => d.activeAt,
        cell: () => null,
        defaultHidden: true,
        hideable: false,
      },
    ],
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [t, servers, certs, health, manage],
  )
  const table = useDataTable({
    data: domains ?? [],
    columns,
    getRowId: (d) => d.name,
    pageSize: 20,
    defaultSort: { id: "activity", direction: "desc" },
  })

  const open = (d: Domain) => router.push(`${base}/domains/${encodeURIComponent(d.name)}`)
  const actions = (d: Domain): DataTableRowAction[] => [
    { id: "manage", label: t("domains2.manage"), icon: SettingsIcon, onSelect: () => open(d) },
    { id: "open", label: d.name, icon: ExternalLinkIcon, onSelect: () => window.open(`https://${d.name}`, "_blank", "noreferrer") },
  ]

  const add = manage && servers.length > 0 ? <AddProxyHost servers={servers} onSaved={load} /> : null

  return (
    <PageBody title={t("nav.domains")} description={t("domains2.lede")} actions={add} bleed divider={false}>
      <ErrorBand message={error} />
      {domains === null ? (
        <LoadingRows rows={4} />
      ) : domains.length === 0 ? (
        <div className="px-6 pb-6">
          <EmptyState
            icon={GlobeIcon}
            title={t("domains2.emptyTitle")}
            description={t("domains2.emptyBody")}
            actions={
              <>
                <Button variant="primary" nativeButton={false} render={<Link href={`${base}/apps`} />}>
                  <BoxesIcon />
                  {t("domains2.toApps")}
                </Button>
                {add}
              </>
            }
          />
        </div>
      ) : (
        <div className="flex flex-col gap-3 px-6 pb-6">
          <DataTableToolbar>
            <DataTableSearch table={table} placeholder={t("domains2.search")} />
            <DataTableFacetFilter
              table={table}
              column="tls"
              title={t("domains2.tls")}
              options={[
                { value: "attention", label: t("domains2.needsAttention") },
                { value: "ok", label: t("domains2.healthy") },
                { value: "off", label: t("proxy.disabled") },
              ]}
            />
          </DataTableToolbar>
          <DataTable
            table={table}
            label={t("nav.domains")}
            rowLabel={(d) => d.name}
            rowActions={actions}
            onRowClick={open}
            empty={t("domains2.noMatch")}
          />
          <DataTablePagination table={table} />
        </div>
      )}
    </PageBody>
  )
}
