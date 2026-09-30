"use client"

import { Input, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@fadymondy/nasaq/web"
import Link from "next/link"
import { useState } from "react"

import { ListEmpty, ListError, ListToolbar, LoadingRows, Pager } from "@/components/admin/admin-list"
import { PageBody } from "@/components/app-shell"

import { useAdminList, type AdminAuditEntry } from "@/lib/admin"
import { useDocumentTitle, useFormatDate, useI18n } from "@/lib/i18n"

/** Where an audit entry's target lives in the admin, when it has a page. */
function targetHref(locale: string, e: AdminAuditEntry) {
  if (e.target_type === "user") return `/${locale}/admin/users/${e.target_id}`
  if (e.target_type === "team") return `/${locale}/admin/teams/${e.target_id}`
  return null
}

export default function AdminAuditPage() {
  const { t, locale } = useI18n()
  const fmt = useFormatDate()
  useDocumentTitle(t("admin.nav.audit"))
  const [actor, setActor] = useState("")
  const [action, setAction] = useState("")
  const [targetType, setTargetType] = useState("")
  const [targetId, setTargetId] = useState("")
  const [since, setSince] = useState("")
  const [until, setUntil] = useState("")
  const list = useAdminList<AdminAuditEntry>("/api/admin/audit", {
    actor: actor.trim() || undefined,
    action: action.trim() || undefined,
    target_type: targetType.trim() || undefined,
    target_id: targetId.trim() || undefined,
    since: since ? new Date(since).toISOString() : undefined,
    until: until ? new Date(until).toISOString() : undefined,
  })
  const items = list.data?.items ?? []
  const filtered = Boolean(actor || action || targetType || targetId || since || until || list.q)

  return (
    <PageBody eyebrow={t("nav.platform")} title={t("admin.nav.audit")} description={t("admin.audit.hint")} bleed>
      <ListToolbar q={list.q} onQ={list.setQ} placeholder={t("admin.audit.filters.search")}>
        <Input
          value={actor}
          onChange={(e) => setActor(e.target.value)}
          placeholder={t("admin.audit.filters.actor")}
          aria-label={t("admin.audit.filters.actor")}
          dir="ltr"
          className="w-36 font-mono" />
        <Input
          value={action}
          onChange={(e) => setAction(e.target.value)}
          placeholder={t("admin.audit.filters.action")}
          aria-label={t("admin.audit.filters.action")}
          dir="ltr"
          className="w-32 font-mono" />
        <Input
          value={targetType}
          onChange={(e) => setTargetType(e.target.value)}
          placeholder={t("admin.audit.filters.targetType")}
          aria-label={t("admin.audit.filters.targetType")}
          dir="ltr"
          className="w-28 font-mono" />
        <Input
          value={targetId}
          onChange={(e) => setTargetId(e.target.value)}
          placeholder={t("admin.audit.filters.targetId")}
          aria-label={t("admin.audit.filters.targetId")}
          dir="ltr"
          className="w-28 font-mono" />
        <Input
          type="date"
          value={since}
          onChange={(e) => setSince(e.target.value)}
          aria-label={t("admin.audit.filters.since")}
          dir="ltr"
          />
        <Input
          type="date"
          value={until}
          onChange={(e) => setUntil(e.target.value)}
          aria-label={t("admin.audit.filters.until")}
          dir="ltr"
          />
      </ListToolbar>
      {list.error ? (
        <ListError error={list.error} />
      ) : !list.data ? (
        <LoadingRows />
      ) : items.length === 0 ? (
        <ListEmpty title={filtered ? t("admin.list.noMatch") : t("admin.audit.empty")} />
      ) : (
        <div className="border-b border-border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="ps-6">{t("admin.audit.action")}</TableHead>
                <TableHead>{t("admin.audit.actor")}</TableHead>
                <TableHead>{t("admin.audit.target")}</TableHead>
                <TableHead>{t("admin.audit.details")}</TableHead>
                <TableHead className="pe-6 text-end">{t("admin.audit.when")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((e) => {
                const href = targetHref(locale, e)
                const details = e.details && Object.keys(e.details).length ? JSON.stringify(e.details) : ""
                return (
                  <TableRow key={e.id}>
                    <TableCell className="ps-6">{t(`admin.audit.actions.${e.action.replace(".", "_")}`, undefined, e.action)}</TableCell>
                    <TableCell dir="ltr" className="text-start">
                      {e.actor_email}
                    </TableCell>
                    <TableCell>
                      {href ? (
                        <Link href={href} className="hover:underline">
                          {t(`admin.audit.targets.${e.target_type}`, undefined, e.target_type)}
                        </Link>
                      ) : (
                        t(`admin.audit.targets.${e.target_type}`, undefined, e.target_type)
                      )}
                    </TableCell>
                    <TableCell className="max-w-sm">
                      {details ? (
                        <code className="line-clamp-1 font-mono text-xs text-muted-foreground" dir="ltr" title={details}>
                          {details}
                        </code>
                      ) : (
                        <span className="text-muted-foreground">—</span>
                      )}
                    </TableCell>
                    <TableCell className="pe-6 text-end text-muted-foreground" dir="ltr">
                      {fmt(e.created_at)}
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        </div>
      )}
      {list.data ? <Pager offset={list.offset} pageSize={list.pageSize} total={list.data.total} onOffset={list.setOffset} /> : null}
    </PageBody>
  )
}
