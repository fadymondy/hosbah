"use client"

import { Button, EmptyState, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@fadymondy/nasaq/web"
import Link from "next/link"
import { Fragment, useCallback, useEffect, useMemo, useState } from "react"
import { BoxesIcon, GlobeIcon, LockIcon, NetworkIcon, ShieldAlertIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { useErrorText } from "@/components/form-error"
import { FilterPills, ListBar, StatusDot, type Tone } from "@/components/lists/list-bar"
import { AddProxyHost } from "@/components/server-tabs/proxy"
import { ErrorBand, LoadingRows } from "@/components/states"
import { apiFetch } from "@/lib/api"
import { useDocumentTitle, useI18n } from "@/lib/i18n"
import type { ProxyHost, Server } from "@/lib/servers"
import { useTeam } from "@/lib/team"

interface TeamDomain {
  id: string
  hostname: string
  tls: "auto" | "internal" | "off"
  verified: boolean
  verify_name: string
  verify_value: string
  app_id: string
  app_name: string
  server_id: string
}

interface Cert {
  hostname: string
  ok: boolean
  days_left?: number
  error?: string
}

/** One hostname, whether it routes to an app or is a proxy host. */
interface Row {
  key: string
  kind: "app" | "proxy"
  hostname: string
  tls: "auto" | "internal" | "off"
  verified: boolean
  verifyName: string
  verifyValue: string
  enabled: boolean
  target: string
  targetHref: string
  serverId: string
  verifyPath: string
  manageHref: string
}

type Health = "attention" | "ok" | "off"

/**
 * Every hostname the team serves, in one place: app domains and proxy hosts share one namespace.
 * Unverified hostnames show their TXT record and a Verify button; certificates come from the
 * certificate watch. Adding an app domain happens on the app; a proxy host can be added here.
 */
export default function DomainsPage() {
  const { t, locale } = useI18n()
  const { team, manage } = useTeam()
  useDocumentTitle(`${t("nav.domains")} — ${team.name}`)
  const errorText = useErrorText()
  const base = `/${locale}/t/${team.slug}`

  const [rows, setRows] = useState<Row[] | null>(null)
  const [certs, setCerts] = useState<Map<string, Cert>>(new Map())
  const [servers, setServers] = useState<Server[]>([])
  const [error, setError] = useState<string | null>(null)
  const [q, setQ] = useState("")
  const [kind, setKind] = useState("")
  const [busy, setBusy] = useState<string | null>(null)
  const [open, setOpen] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      const [d, p, s, c] = await Promise.all([
        apiFetch<{ domains: TeamDomain[] }>(`/api/teams/${team.slug}/domains`),
        apiFetch<{ proxy_hosts: ProxyHost[] }>(`/api/teams/${team.slug}/proxy-hosts`).catch(() => ({ proxy_hosts: [] as ProxyHost[] })),
        apiFetch<{ servers: Server[] }>(`/api/teams/${team.slug}/servers`).catch(() => ({ servers: [] as Server[] })),
        apiFetch<{ certificates: Cert[] }>(`/api/teams/${team.slug}/certificates`).catch(() => ({ certificates: [] as Cert[] })),
      ])
      const app = (name: string) => `${base}/apps/${encodeURIComponent(name)}`
      setRows([
        ...(d.domains ?? []).map<Row>((x) => ({
          key: `a-${x.id}`,
          kind: "app",
          hostname: x.hostname,
          tls: x.tls,
          verified: x.verified,
          verifyName: x.verify_name,
          verifyValue: x.verify_value,
          enabled: true,
          target: x.app_name,
          targetHref: app(x.app_name),
          serverId: x.server_id,
          verifyPath: `/api/teams/${team.slug}/apps/${x.app_id}/domains/${x.id}/verify`,
          manageHref: `${app(x.app_name)}/dns`,
        })),
        ...(p.proxy_hosts ?? []).map<Row>((x) => ({
          key: `p-${x.id}`,
          kind: "proxy",
          hostname: x.hostname,
          tls: x.tls as Row["tls"],
          verified: x.verified,
          verifyName: x.verify_name,
          verifyValue: x.verify_value,
          enabled: x.enabled,
          target: x.upstream,
          targetHref: "",
          serverId: x.server_id,
          verifyPath: `/api/teams/${team.slug}/proxy-hosts/${x.id}/verify`,
          manageHref: `${base}/servers/${x.server_id}/proxy`,
        })),
      ])
      setServers(s.servers ?? [])
      setCerts(new Map((c.certificates ?? []).map((x) => [x.hostname, x])))
      setError(null)
    } catch (err) {
      setError(errorText(err))
      setRows((x) => x ?? [])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [team.slug, base])

  useEffect(() => {
    void load()
    const id = setInterval(load, 60_000)
    return () => clearInterval(id)
  }, [load])

  const health = useCallback(
    (r: Row): Health => {
      if (!r.enabled) return "off"
      const c = certs.get(r.hostname)
      if (!r.verified && r.tls === "auto") return "attention"
      if (c && (!c.ok || (c.days_left !== undefined && c.days_left < 14))) return "attention"
      return "ok"
    },
    [certs],
  )

  const all = rows ?? []
  const attention = all.filter((r) => health(r) === "attention").length
  const shown = useMemo(() => {
    const needle = q.trim().toLowerCase()
    return all
      .filter((r) => (!kind || (kind === "attention" ? health(r) === "attention" : r.kind === kind)) && (!needle || `${r.hostname} ${r.target}`.toLowerCase().includes(needle)))
      .sort((a, b) => Number(health(b) === "attention") - Number(health(a) === "attention") || a.hostname.localeCompare(b.hostname))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [rows, q, kind, health])

  const serverName = (id: string) => servers.find((s) => s.id === id)?.name ?? "—"

  async function verify(r: Row) {
    setBusy(r.key)
    try {
      await apiFetch(r.verifyPath, { method: "POST" })
      await load()
    } catch (err) {
      setError(errorText(err))
      setOpen(r.key)
    } finally {
      setBusy(null)
    }
  }

  function TlsCell({ r }: { r: Row }) {
    const c = certs.get(r.hostname)
    let tone: Tone = "idle"
    let label = t(`proxy.tlsModes.${r.tls}`)
    if (r.tls === "auto") {
      if (!r.verified) label = t("domains2.awaitingVerify")
      else if (!c) label = t("domains2.certPending")
      else if (!c.ok) [tone, label] = ["fail", t("domains2.certFailing")]
      else if (c.days_left !== undefined && c.days_left < 14) [tone, label] = ["fail", t("domains2.certExpires", { n: c.days_left })]
      else [tone, label] = ["ok", t("domains2.certOk", { n: c.days_left ?? "—" })]
    }
    return <StatusDot tone={tone} label={label} />
  }

  const add = manage && servers.length > 0 ? <AddProxyHost servers={servers} onSaved={load} /> : null

  return (
    <PageBody title={t("nav.domains")} description={t("domains2.lede")} actions={add} bleed>
      <ErrorBand message={error} />
      {rows === null ? (
        <LoadingRows rows={4} />
      ) : rows.length === 0 ? (
        <EmptyState
          className="border-b"
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
      ) : (
        <>
          {attention > 0 ? (
            <p className="flex items-center gap-2 border-b border-line bg-grid-soft px-6 py-2.5 text-sm text-grid-body">
              <ShieldAlertIcon className="size-4 shrink-0 text-grid-danger" />
              {t("domains2.attention", { n: attention })}
            </p>
          ) : null}
          <ListBar q={q} onQ={setQ} placeholder={t("domains2.search")}>
            <FilterPills
              label={t("domains2.kind")}
              value={kind}
              onChange={setKind}
              options={[
                ["", t("home.filterAll"), all.length],
                ["app", t("domains2.kinds.app"), all.filter((r) => r.kind === "app").length],
                ["proxy", t("domains2.kinds.proxy"), all.filter((r) => r.kind === "proxy").length],
                ...(attention ? ([["attention", t("domains2.needsAttention"), attention]] as [string, string, number][]) : []),
              ]}
            />
          </ListBar>
          {shown.length === 0 ? (
            <p className="border-b border-line px-6 py-8 text-center text-sm text-grid-muted">{t("domains2.noMatch")}</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="ps-6">{t("proxy.hostname")}</TableHead>
                  <TableHead>{t("domains2.routesTo")}</TableHead>
                  <TableHead className="max-md:hidden">{t("proxy.server")}</TableHead>
                  <TableHead>{t("domains2.tls")}</TableHead>
                  <TableHead className="pe-6 text-end">
                    <span className="sr-only">{t("backups2.actions")}</span>
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {shown.map((r) => (
                  <Fragment key={r.key}>
                    <TableRow className="align-top">
                      <TableCell className="ps-6">
                        <a href={`https://${r.hostname.replace(/^\*\./, "")}`} target="_blank" rel="noreferrer" dir="ltr" className="flex items-center gap-2 font-medium text-grid-fg hover:underline">
                          {r.tls !== "off" ? <LockIcon className="size-3.5 shrink-0 text-grid-muted" /> : null}
                          <span className="truncate">{r.hostname}</span>
                        </a>
                        <span className="text-xs text-grid-muted">
                          {t(`domains2.kinds.${r.kind}`)}
                          {!r.enabled ? ` · ${t("proxy.disabled")}` : null}
                        </span>
                      </TableCell>
                      <TableCell>
                        {r.targetHref ? (
                          <Link href={r.targetHref} dir="ltr" className="text-sm text-grid-body hover:text-grid-fg hover:underline">
                            {r.target}
                          </Link>
                        ) : (
                          <span className="flex items-center gap-1.5 text-sm text-grid-body">
                            <NetworkIcon className="size-3.5 text-grid-muted" />
                            <code dir="ltr" className="text-xs">
                              {r.target}
                            </code>
                          </span>
                        )}
                      </TableCell>
                      <TableCell className="max-md:hidden">
                        <Link href={`${base}/servers/${r.serverId}`} dir="ltr" className="text-sm text-grid-body hover:underline">
                          {serverName(r.serverId)}
                        </Link>
                      </TableCell>
                      <TableCell className="text-xs">
                        <TlsCell r={r} />
                        {!r.verified ? (
                          <button type="button" className="mt-1 block text-grid-action hover:underline" onClick={() => setOpen(open === r.key ? null : r.key)}>
                            {t("domains2.showRecord")}
                          </button>
                        ) : null}
                      </TableCell>
                      <TableCell className="pe-6 text-end whitespace-nowrap">
                        {!r.verified && manage ? (
                          <Button size="sm" variant="primary" disabled={busy === r.key} onClick={() => void verify(r)}>
                            {t("proxy.verify")}
                          </Button>
                        ) : null}{" "}
                        <Button size="sm" variant="ghost" nativeButton={false} render={<Link href={r.manageHref} />}>
                          {t("domains2.manage")}
                        </Button>
                      </TableCell>
                    </TableRow>
                    {open === r.key && !r.verified ? (
                      <TableRow>
                        <TableCell colSpan={5} className="bg-grid-soft px-6 py-3 text-sm whitespace-normal">
                          <p className="mb-2 text-grid-body">{t("domains2.recordHint")}</p>
                          <dl dir="ltr" className="grid gap-1 font-mono text-xs sm:grid-cols-[auto_1fr] sm:gap-x-4">
                            <dt className="text-grid-muted">TXT</dt>
                            <dd className="break-all text-grid-fg select-all">{r.verifyName}</dd>
                            <dt className="text-grid-muted">value</dt>
                            <dd className="break-all text-grid-fg select-all">{r.verifyValue}</dd>
                          </dl>
                        </TableCell>
                      </TableRow>
                    ) : null}
                  </Fragment>
                ))}
              </TableBody>
            </Table>
          )}
          <p className="px-6 py-3 text-xs text-grid-muted">{t("domains2.addHint")}</p>
        </>
      )}
    </PageBody>
  )
}
