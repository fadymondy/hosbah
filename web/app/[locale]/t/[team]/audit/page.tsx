"use client"

import { EmptyState, Input, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useRef, useState } from "react"
import { ScrollTextIcon } from "lucide-react"

import { ListToolbar, Pager } from "@/components/admin/admin-list"
import { PageBody } from "@/components/app-shell"
import { useErrorText } from "@/components/form-error"
import { ErrorBand, LoadingRows, TableBand } from "@/components/states"


import { apiFetch, type AuditEntry } from "@/lib/api"
import { useDocumentTitle, useFormatDate, useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

const PAGE_SIZE = 100

export default function AuditPage() {
  const { t } = useI18n()
  const { team } = useTeam()
  useDocumentTitle(`${t("nav.audit")} — ${team.name}`)
  const fmt = useFormatDate()
  const errorText = useErrorText()

  const [q, setQ] = useState("")
  const [debouncedQ, setDebouncedQ] = useState("")
  const [actor, setActor] = useState("")
  const [action, setAction] = useState("")
  const [subjectType, setSubjectType] = useState("")
  const [subjectId, setSubjectId] = useState("")
  const [since, setSince] = useState("")
  const [until, setUntil] = useState("")
  const [offset, setOffset] = useState(0)

  const [entries, setEntries] = useState<AuditEntry[] | null>(null)
  const [total, setTotal] = useState(0)
  const [error, setError] = useState<string | null>(null)
  const seq = useRef(0)

  useEffect(() => {
    const id = setTimeout(() => setDebouncedQ(q.trim()), 250)
    return () => clearTimeout(id)
  }, [q])

  const filters = {
    actor: actor.trim(),
    action: action.trim(),
    subject_type: subjectType.trim(),
    subject_id: subjectId.trim(),
    since: since ? new Date(since).toISOString() : "",
    until: until ? new Date(until).toISOString() : "",
    q: debouncedQ,
  }
  const filterKey = JSON.stringify(filters)

  useEffect(() => setOffset(0), [filterKey])

  const load = useCallback(async () => {
    const mine = ++seq.current
    const params = new URLSearchParams({ limit: String(PAGE_SIZE), offset: String(offset) })
    for (const [k, v] of Object.entries(JSON.parse(filterKey) as Record<string, string>)) {
      if (v) params.set(k, v)
    }
    try {
      const r = await apiFetch<{ entries: AuditEntry[]; total: number }>(`/api/teams/${team.slug}/audit?${params}`)
      if (mine !== seq.current) return
      setEntries(r.entries ?? [])
      setTotal(r.total ?? 0)
      setError(null)
    } catch (err) {
      if (mine !== seq.current) return
      setError(errorText(err))
      setEntries([])
      setTotal(0)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [team.slug, offset, filterKey])

  useEffect(() => {
    void load()
  }, [load])

  const filtered = Boolean(actor || action || subjectType || subjectId || since || until || debouncedQ)

  return (
    <PageBody title={t("nav.audit")} description={t("audit.description")} bleed>
      <ListToolbar q={q} onQ={setQ} placeholder={t("audit.filters.search")}>
        <Input
          value={actor}
          onChange={(e) => setActor(e.target.value)}
          placeholder={t("audit.filters.actor")}
          aria-label={t("audit.filters.actor")}
          dir="ltr"
          className="w-36 font-mono" />
        <Input
          value={action}
          onChange={(e) => setAction(e.target.value)}
          placeholder={t("audit.filters.action")}
          aria-label={t("audit.filters.action")}
          dir="ltr"
          className="w-32 font-mono" />
        <Input
          value={subjectType}
          onChange={(e) => setSubjectType(e.target.value)}
          placeholder={t("audit.filters.subjectType")}
          aria-label={t("audit.filters.subjectType")}
          dir="ltr"
          className="w-28 font-mono" />
        <Input
          value={subjectId}
          onChange={(e) => setSubjectId(e.target.value)}
          placeholder={t("audit.filters.subjectId")}
          aria-label={t("audit.filters.subjectId")}
          dir="ltr"
          className="w-28 font-mono" />
        <Input
          type="date"
          value={since}
          onChange={(e) => setSince(e.target.value)}
          aria-label={t("audit.filters.since")}
          dir="ltr"
          />
        <Input
          type="date"
          value={until}
          onChange={(e) => setUntil(e.target.value)}
          aria-label={t("audit.filters.until")}
          dir="ltr"
          />
      </ListToolbar>
      <ErrorBand message={error} />
      {entries === null ? (
        <LoadingRows />
      ) : entries.length === 0 && !error ? (
        <EmptyState className="border-b" icon={ScrollTextIcon} title={filtered ? t("audit.noMatch") : t("audit.emptyTitle")} description={t("audit.emptyHint")} />
      ) : entries.length ? (
        <TableBand>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("audit.when")}</TableHead>
                <TableHead>{t("audit.action")}</TableHead>
                <TableHead>{t("audit.subject")}</TableHead>
                <TableHead>{t("audit.by")}</TableHead>
                <TableHead>{t("audit.ip")}</TableHead>
                <TableHead>{t("audit.changes")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {entries.map((e) => (
                <TableRow key={e.id} className="align-top">
                  <TableCell dir="ltr" className="text-start whitespace-nowrap">
                    {fmt(e.created_at)}
                  </TableCell>
                  <TableCell>
                    <code dir="ltr" className="font-mono text-xs">
                      {e.action}
                    </code>
                  </TableCell>
                  <TableCell>
                    <code dir="ltr" className="font-mono text-xs">
                      {e.subject_type}
                      {e.subject_id != null ? `#${e.subject_id}` : ""}
                    </code>
                  </TableCell>
                  <TableCell dir="ltr" className="text-start">
                    {e.causer_email ?? t("audit.system")}
                  </TableCell>
                  <TableCell dir="ltr" className="text-start font-mono text-xs">
                    {e.ip ?? "—"}
                  </TableCell>
                  <TableCell className="max-w-72">
                    {e.changes && Object.keys(e.changes as object).length ? (
                      <details>
                        <summary className="cursor-pointer text-xs text-muted-foreground">{t("audit.show")}</summary>
                        <pre dir="ltr" className="mt-2 overflow-x-auto text-start font-mono text-xs">
                          {JSON.stringify(e.changes, null, 2)}
                        </pre>
                      </details>
                    ) : (
                      "—"
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </TableBand>
      ) : null}
      {entries !== null && !error ? <Pager offset={offset} pageSize={PAGE_SIZE} total={total} onOffset={setOffset} /> : null}
    </PageBody>
  )
}
