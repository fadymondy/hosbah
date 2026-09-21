"use client"

import { useCallback, useEffect, useState } from "react"
import { CloudIcon, LockIcon, RefreshCwIcon, ShieldCheckIcon, Trash2Icon, WrenchIcon } from "lucide-react"
import { toast } from "sonner"

import { ConfirmButton } from "@/components/confirm-button"
import { useErrorText } from "@/components/form-error"
import { useLoadedSite } from "@/components/site/context"
import { ErrorBand, LoadingRows, SectionTitle, TableBand } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
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

const TONE: Record<Status, "default" | "secondary" | "destructive" | "outline"> = {
  ok: "default",
  missing: "destructive",
  mismatch: "destructive",
  unmanaged: "outline",
  private: "secondary",
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
          <p className="flex items-center gap-2 border-b border-line px-6 py-6 text-sm text-grid-muted">
            <CloudIcon className="size-4" />
            {t("site.dns.none")}
          </p>
        ) : (
          <TableBand>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t("proxy.hostname")}</TableHead>
                  <TableHead>{t("lists.status")}</TableHead>
                  <TableHead>{t("site.dns.record")}</TableHead>
                  <TableHead>{t("site.dns.proxied")}</TableHead>
                  {manage ? <TableHead className="text-end">{t("common.actions")}</TableHead> : null}
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.map((r) => {
                  const foreign = !!r.record && !r.record.managed_by_hosbah
                  return (
                    <TableRow key={r.hostname}>
                      <TableCell>
                        <span dir="ltr" className="font-medium">
                          {r.hostname}
                        </span>
                        <div className="text-xs text-grid-muted">{r.credential ? `${r.zone} · ${r.credential.name}` : t("site.dns.noZone")}</div>
                      </TableCell>
                      <TableCell>
                        <Badge variant={TONE[r.status]}>{t(`site.dns.status.${r.status}`)}</Badge>
                        {r.error ? (
                          <p dir="auto" className="mt-1 max-w-xs text-xs text-grid-danger">
                            {r.error}
                          </p>
                        ) : null}
                      </TableCell>
                      <TableCell dir="ltr" className="text-start font-mono text-xs">
                        {r.record ? `${r.record.type} ${r.record.content}` : "—"}
                        {r.status === "mismatch" && r.expected.content ? (
                          <div className="text-grid-muted">
                            → {r.expected.type} {r.expected.content}
                          </div>
                        ) : null}
                        {foreign ? <div className="font-sans text-grid-muted">{t("site.dns.notOurs")}</div> : null}
                      </TableCell>
                      <TableCell>{r.record ? (r.record.proxied ? t("site.dns.proxiedOn") : t("site.dns.proxiedOff")) : "—"}</TableCell>
                      {manage ? (
                        <TableCell>
                          <div className="flex flex-wrap justify-end gap-2">
                            {r.managed && (r.status === "missing" || (r.status === "mismatch" && !foreign)) ? (
                              <Button size="sm" disabled={busy === r.hostname} onClick={() => void put(r.hostname, {}).catch(() => {})}>
                                <WrenchIcon />
                                {r.status === "missing" ? t("site.dns.create") : t("site.dns.repair")}
                              </Button>
                            ) : null}
                            {r.managed && r.status === "mismatch" && foreign ? (
                              <ConfirmButton
                                icon={<WrenchIcon />}
                                label={t("site.dns.takeover")}
                                title={t("site.dns.takeoverTitle", { host: r.hostname })}
                                description={t("site.dns.takeoverBody", { old: `${r.record?.type} ${r.record?.content}`, next: `${r.expected.type} ${r.expected.content}` })}
                                confirmLabel={t("site.dns.takeover")}
                                onConfirm={async () => {
                                  try {
                                    await put(r.hostname, { takeover: true })
                                  } catch (err) {
                                    throw new Error(errorText(err))
                                  }
                                }}
                              />
                            ) : null}
                            {r.record && r.managed ? (
                              <Button
                                size="sm"
                                variant="outline"
                                disabled={busy === r.hostname}
                                onClick={() => void put(r.hostname, { proxied: !r.record?.proxied }).catch(() => {})}
                              >
                                <CloudIcon />
                                {r.record.proxied ? t("site.dns.dnsOnly") : t("site.dns.proxy")}
                              </Button>
                            ) : null}
                            {r.record?.managed_by_hosbah ? (
                              <ConfirmButton
                                icon={<Trash2Icon />}
                                label={t("common.delete")}
                                title={t("site.dns.deleteTitle", { host: r.hostname })}
                                description={t("site.dns.deleteBody")}
                                confirmLabel={t("common.delete")}
                                onConfirm={async () => {
                                  try {
                                    await apiFetch(`${path}/dns/${encodeURIComponent(r.hostname)}`, { method: "DELETE" })
                                  } catch (err) {
                                    throw new Error(errorText(err))
                                  }
                                  await loadDNS()
                                }}
                              />
                            ) : null}
                          </div>
                        </TableCell>
                      ) : null}
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          </TableBand>
        )}
      </section>

      <section aria-labelledby="ssl-h">
        <SectionTitle id="ssl-h" title={t("site.dns.certs")} hint={t("site.dns.certsHint")} />
        {certNote ? <p className="border-b border-line px-6 py-4 text-sm text-grid-muted">{certNote}</p> : null}
        {certs === null ? (
          <LoadingRows rows={2} />
        ) : certs.length === 0 ? (
          certNote ? null : <p className="border-b border-line px-6 py-6 text-sm text-grid-muted">{t("site.dns.none")}</p>
        ) : (
          <TableBand>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t("proxy.hostname")}</TableHead>
                  <TableHead>{t("site.dns.issuer")}</TableHead>
                  <TableHead>{t("site.dns.expires")}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {certs.map((c) => {
                  const soon = c.days_left != null && c.days_left <= 14
                  return (
                    <TableRow key={c.hostname}>
                      <TableCell>
                        <span className="inline-flex items-center gap-2 font-medium" dir="ltr">
                          {c.found ? <ShieldCheckIcon className="size-4 text-[var(--grid-ok,#3fb950)]" /> : <LockIcon className="size-4 text-grid-muted" />}
                          {c.hostname}
                        </span>
                      </TableCell>
                      <TableCell>
                        {c.found ? (
                          <span className="flex flex-wrap items-center gap-2">
                            <span dir="ltr">{c.issuer ?? "—"}</span>
                            <Badge variant={c.managed ? "secondary" : "outline"}>{c.managed ? t("site.dns.public") : t("site.dns.internal")}</Badge>
                          </span>
                        ) : (
                          <span className="text-grid-muted">{c.error ?? t("site.dns.noCert")}</span>
                        )}
                      </TableCell>
                      <TableCell>
                        {c.not_after ? (
                          <span className={soon ? "text-grid-danger" : undefined}>
                            <span dir="ltr">{fmt(c.not_after)}</span>
                            {c.days_left != null ? <span className="ms-2 text-xs text-grid-muted">{t("site.dns.daysLeft", { n: c.days_left })}</span> : null}
                          </span>
                        ) : (
                          "—"
                        )}
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          </TableBand>
        )}
      </section>
    </>
  )
}
