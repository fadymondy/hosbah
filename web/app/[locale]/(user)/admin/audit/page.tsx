"use client"

import Link from "next/link"

import { ListEmpty, ListError, LoadingRows, Pager } from "@/components/admin/admin-list"
import { PageBody } from "@/components/app-shell"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
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
  const list = useAdminList<AdminAuditEntry>("/api/admin/audit")
  const items = list.data?.items ?? []

  return (
    <PageBody eyebrow={t("nav.platform")} title={t("admin.nav.audit")} description={t("admin.audit.hint")} bleed>
      {list.error ? (
        <ListError error={list.error} />
      ) : !list.data ? (
        <LoadingRows />
      ) : items.length === 0 ? (
        <ListEmpty title={t("admin.audit.empty")} />
      ) : (
        <div className="border-b border-line">
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
                        <code className="line-clamp-1 font-mono text-xs text-grid-muted" dir="ltr" title={details}>
                          {details}
                        </code>
                      ) : (
                        <span className="text-grid-muted">—</span>
                      )}
                    </TableCell>
                    <TableCell className="pe-6 text-end text-grid-muted" dir="ltr">
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
