"use client"

import Link from "next/link"

import { ListEmpty, ListError, ListToolbar, LoadingRows, Pager } from "@/components/admin/admin-list"
import { PageBody } from "@/components/app-shell"
import { Badge } from "@/components/ui/badge"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { useAdminList, type AdminUser } from "@/lib/admin"
import { useDocumentTitle, useFormatDate, useI18n } from "@/lib/i18n"

export default function AdminUsersPage() {
  const { t, locale } = useI18n()
  const fmt = useFormatDate()
  useDocumentTitle(t("admin.nav.users"))
  const list = useAdminList<AdminUser>("/api/admin/users")
  const items = list.data?.items ?? []

  return (
    <PageBody eyebrow={t("nav.platform")} title={t("admin.nav.users")} description={t("admin.users.hint")} bleed>
      <ListToolbar q={list.q} onQ={list.setQ} placeholder={t("admin.users.search")} />
      {list.error ? (
        <ListError error={list.error} />
      ) : !list.data ? (
        <LoadingRows />
      ) : items.length === 0 ? (
        <ListEmpty title={list.q ? t("admin.list.noMatch") : t("admin.users.empty")} />
      ) : (
        <div className="border-b border-line">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="ps-6">{t("admin.users.email")}</TableHead>
                <TableHead>{t("admin.users.role")}</TableHead>
                <TableHead>{t("admin.users.status")}</TableHead>
                <TableHead className="text-end">{t("admin.users.teams")}</TableHead>
                <TableHead className="pe-6 text-end">{t("admin.users.joined")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((u) => (
                <TableRow key={u.id}>
                  <TableCell className="ps-6">
                    <Link href={`/${locale}/admin/users/${u.id}`} className="font-medium hover:underline" dir="ltr">
                      {u.email}
                    </Link>
                  </TableCell>
                  <TableCell>
                    {u.platform_admin ? <Badge>{t("admin.users.platformAdmin")}</Badge> : (
                      <span className="text-grid-muted">{t("admin.users.member")}</span>
                    )}
                  </TableCell>
                  <TableCell>
                    {u.disabled ? (
                      <Badge variant="destructive">{t("admin.users.disabled")}</Badge>
                    ) : (
                      <Badge variant="outline">{t("admin.users.active")}</Badge>
                    )}
                  </TableCell>
                  <TableCell className="text-end font-mono tabular-nums">{u.team_count}</TableCell>
                  <TableCell className="pe-6 text-end text-grid-muted" dir="ltr">
                    {fmt(u.created_at)}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
      {list.data ? <Pager offset={list.offset} pageSize={list.pageSize} total={list.data.total} onOffset={list.setOffset} /> : null}
    </PageBody>
  )
}
