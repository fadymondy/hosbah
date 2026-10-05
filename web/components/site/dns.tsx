"use client"

import { Badge, Button, toast, useConfirm, type DataTableRowAction } from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useState } from "react"
import { CloudIcon, LockIcon, RefreshCwIcon, ShieldCheckIcon, Trash2Icon, WrenchIcon } from "lucide-react"

import { useErrorText } from "@/components/form-error"
import { useLoadedSite } from "@/components/site/context"
import { NasaqTable } from "@/components/lists/nasaq-table"
import { ErrorBand, LoadingRows, SectionTitle } from "@/components/states"



import { apiFetch, ApiError } from "@/lib/api"
import { useFormatDate, useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

type Status = "ok" | "missing" | "mismatch" | "unmanaged" | "private"
type Row = {
  hostname: string
  zone: string | null
  credential: { id: string; name: string } | null
  managed: boolean
  record: { id: string; type: string; content: string; proxied: boolean; ttl: number; managed_by_hosbah: boolean } | null
  expected: { type?: string; content?: string }
  status: Status
  error: string | null
}
type Cert = {
  hostname: string
  found: boolean
  issuer: string | null
  not_before: string | null
  not_after: string | null
  days_left: number | null
  managed: boolean
  error: string | null
}

const TONE: Record<Status, "success" | "neutral" | "danger" | "outline"> = {
  ok: "success",
  missing: "danger",
  mismatch: "danger",
  unmanaged: "outline",
  private: "neutral",
}

/**
 * DNS & SSL: for each of the site's hostnames, its Cloudflare record (checked against where the
 * server is, created / repaired / proxied from here) and the certificate Caddy serves for it.
 * A record Hosbah did not create is never overwritten or removed without saying so.
 */
export function SiteDNS() {
  const { t } = useI18n()
  const { manage } = useTeam()
  const fmt = useFormatDate()
  const errorText = useErrorText()
  const { path } = useLoadedSite()
  const [rows, setRows] = useState<Row[] | null>(null)
  const [server, setServer] = useState<{ name: string; address: string | null } | null>(null)
  const [certs, setCerts] = useState<Cert[] | null>(null)
  const [certNote, setCertNote] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const confirm = useConfirm()

  const loadDNS = useCallback(async () => {
    try {
      const r = await apiFetch<{ server: { name: string; address: string | null }; records: Row[] }>(`${path}/dns`)
      setRows(r.records ?? [])
      setServer(r.server)
    } catch (err) {
      setError(errorText(err))
      setRows((x) => x ?? [])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path])

  const loadCerts = useCallback(async () => {
    setCertNote(null)
    try {
      const r = await apiFetch<{ certificates: Cert[] }>(`${path}/certificates`)
      setCerts(r.certificates ?? [])
    } catch (err) {
      setCerts([])
      // The server being offline or slow is a state of the server, not a page error.
      setCertNote(err instanceof ApiError && (err.status === 409 || err.status === 504) ? t("site.dns.certsOffline") : errorText(err))
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path])

  const reload = useCallback(async () => {
    setLoading(true)
    setError(null)
    await Promise.all([loadDNS(), loadCerts()])
    setLoading(false)
  }, [loadDNS, loadCerts])

  useEffect(() => {
    void reload()
  }, [reload])

  async function put(host: string, body: Record<string, unknown>) {
    setBusy(host)
    setError(null)
    try {
      const row = await apiFetch<Row>(`${path}/dns/${encodeURIComponent(host)}`, { method: "PUT", body })
      setRows((cur) => (cur ?? []).map((r) => (r.hostname === host ? row : r)))
      toast.success(t("site.dns.saved"))
    } catch (err) {
      setError(errorText(err))
      throw err
    } finally {
      setBusy(null)
    }
  }

  // Create or repair, take over a record someone else made, switch the Cloudflare proxy, delete: behind each row's ⋯ menu.
  function rowActions(r: Row): DataTableRowAction[] {
    const foreign = !!r.record && !r.record.managed_by_hosbah
    const actions: DataTableRowAction[] = []
    if (r.managed && (r.status === "missing" || (r.status === "mismatch" && !foreign))) {
      actions.push({
        id: "repair",
        label: r.status === "missing" ? t("site.dns.create") : t("site.dns.repair"),
        icon: WrenchIcon,
        disabled: busy === r.hostname,
        onSelect: () => void put(r.hostname, {}).catch(() => {}),
      })
    }
    if (r.managed && r.status === "mismatch" && foreign) {
      actions.push({
        id: "takeover",
        label: t("site.dns.takeover"),
        icon: WrenchIcon,
        disabled: busy === r.hostname,
        onSelect: async () => {
          const ok = await confirm({
            title: t("site.dns.takeoverTitle", { host: r.hostname }),
            description: t("site.dns.takeoverBody", { old: `${r.record?.type} ${r.record?.content}`, next: `${r.expected.type} ${r.expected.content}` }),
            confirmLabel: t("site.dns.takeover"),
          })
          if (ok) await put(r.hostname, { takeover: true }).catch(() => {})
        },
      })
    }
    if (r.record && r.managed) {
      actions.push({
        id: "proxy",
        label: r.record.proxied ? t("site.dns.dnsOnly") : t("site.dns.proxy"),
        icon: CloudIcon,
        disabled: busy === r.hostname,
        onSelect: () => void put(r.hostname, { proxied: !r.record?.proxied }).catch(() => {}),
      })
    }
    if (r.record?.managed_by_hosbah) {
      actions.push({
        id: "delete",
        label: t("common.delete"),
        icon: Trash2Icon,
        danger: true,
        group: "danger",
        onSelect: async () => {
          const ok = await confirm({ title: t("site.dns.deleteTitle", { host: r.hostname }), description: t("site.dns.deleteBody"), confirmLabel: t("common.delete") })
          if (!ok) return
          setError(null)
          try {
            await apiFetch(`${path}/dns/${encodeURIComponent(r.hostname)}`, { method: "DELETE" })
            await loadDNS()
          } catch (err) {
            setError(errorText(err))
          }
        },
      })
    }
    return actions
  }

  return (
    <>
      <section aria-labelledby="dns-h">
        <SectionTitle
          id="dns-h"
          title={t("site.dns.records")}
          hint={server?.address ? t("site.dns.recordsHint", { address: server.address }) : t("site.dns.recordsHintPrivate")}
          action={
            <Button variant="ghost" size="icon-sm" aria-label={t("github.refresh")} disabled={loading} onClick={() => void reload()}>
              <RefreshCwIcon className={loading ? "animate-spin" : ""} />
            </Button>
          }
        />
        <ErrorBand message={error} />
        {rows === null ? (
          <LoadingRows rows={2} />
        ) : rows.length === 0 ? (
          <p className="flex items-center gap-2 nq-band py-6 text-sm text-muted-foreground">
            <CloudIcon className="size-4" />
            {t("site.dns.none")}
          </p>
        ) : (
          <NasaqTable
            data={rows}
            rowId={(r) => r.hostname}
            rowLabel={(r) => r.hostname}
            label={t("site.dns.records")}
            search={rows.length > 8 ? t("proxy.hostname") : undefined}
            rowActions={manage ? rowActions : undefined}
            columns={[
              {
                id: "host",
                header: t("proxy.hostname"),
                cell: (r) => (
                  <>
                    <span dir="ltr" className="font-medium">
                      {r.hostname}
                    </span>
                    <div className="text-xs text-muted-foreground">{r.credential ? `${r.zone} · ${r.credential.name}` : t("site.dns.noZone")}</div>
                  </>
                ),
                sortValue: (r) => r.hostname,
                searchValue: (r) => r.hostname,
              },
              {
                id: "status",
                header: t("lists.status"),
                cell: (r) => (
                  <>
                    <Badge variant={TONE[r.status]}>{t(`site.dns.status.${r.status}`)}</Badge>
                    {r.error ? (
                      <p dir="auto" className="mt-1 max-w-xs text-xs text-nq-danger-text">
                        {r.error}
                      </p>
                    ) : null}
                  </>
                ),
                sortValue: (r) => r.status,
              },
              {
                id: "record",
                header: t("site.dns.record"),
                cell: (r) => (
                  <div dir="ltr" className="text-start font-mono text-xs">
                    {r.record ? `${r.record.type} ${r.record.content}` : "—"}
                    {r.status === "mismatch" && r.expected.content ? (
                      <div className="text-muted-foreground">
                        → {r.expected.type} {r.expected.content}
                      </div>
                    ) : null}
                    {r.record && !r.record.managed_by_hosbah ? <div className="font-sans text-muted-foreground">{t("site.dns.notOurs")}</div> : null}
                  </div>
                ),
              },
              { id: "proxied", header: t("site.dns.proxied"), cell: (r) => (r.record ? (r.record.proxied ? t("site.dns.proxiedOn") : t("site.dns.proxiedOff")) : "—") },
            ]}
          />
        )}
      </section>

      <section aria-labelledby="ssl-h">
        <SectionTitle id="ssl-h" title={t("site.dns.certs")} hint={t("site.dns.certsHint")} />
        {certNote ? <p className="nq-band py-4 text-sm text-muted-foreground">{certNote}</p> : null}
        {certs === null ? (
          <LoadingRows rows={2} />
        ) : certs.length === 0 ? (
          certNote ? null : <p className="nq-band py-6 text-sm text-muted-foreground">{t("site.dns.none")}</p>
        ) : (
          <NasaqTable
            data={certs}
            rowId={(c) => c.hostname}
            rowLabel={(c) => c.hostname}
            label={t("site.dns.certs")}
            defaultSort={{ id: "expires", direction: "asc" }}
            columns={[
              {
                id: "host",
                header: t("proxy.hostname"),
                cell: (c) => (
                  <span className="inline-flex items-center gap-2 font-medium" dir="ltr">
                    {c.found ? <ShieldCheckIcon className="size-4 text-nq-success" /> : <LockIcon className="size-4 text-muted-foreground" />}
                    {c.hostname}
                  </span>
                ),
                sortValue: (c) => c.hostname,
              },
              {
                id: "issuer",
                header: t("site.dns.issuer"),
                cell: (c) =>
                  c.found ? (
                    <span className="flex flex-wrap items-center gap-2">
                      <span dir="ltr">{c.issuer ?? "—"}</span>
                      <Badge variant={c.managed ? "neutral" : "outline"}>{c.managed ? t("site.dns.public") : t("site.dns.internal")}</Badge>
                    </span>
                  ) : (
                    <span className="text-muted-foreground">{c.error ?? t("site.dns.noCert")}</span>
                  ),
              },
              {
                id: "expires",
                header: t("site.dns.expires"),
                cell: (c) =>
                  c.not_after ? (
                    <span className={c.days_left != null && c.days_left <= 14 ? "text-nq-danger-text" : undefined}>
                      <span dir="ltr">{fmt(c.not_after)}</span>
                      {c.days_left != null ? <span className="ms-2 text-xs text-muted-foreground">{t("site.dns.daysLeft", { n: c.days_left })}</span> : null}
                    </span>
                  ) : (
                    "—"
                  ),
                sortValue: (c) => c.days_left ?? Number.MAX_SAFE_INTEGER,
              },
            ]}
          />
        )}
      </section>
    </>
  )
}
