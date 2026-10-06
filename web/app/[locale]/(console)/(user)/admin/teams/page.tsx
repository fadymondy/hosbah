"use client"

import { Badge, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@fadymondy/nasaq/web"
import Link from "next/link"

import { ListEmpty, ListError, ListToolbar, LoadingRows, Pager } from "@/components/admin/admin-list"
import { PageBody } from "@/components/app-shell"


import { useAdminList, type AdminTeam } from "@/lib/admin"
import { useDocumentTitle, useFormatDate, useI18n } from "@/lib/i18n"

export default function AdminTeamsPage() {
  const { t, locale } = useI18n()
  const fmt = useFormatDate()
  useDocumentTitle(t("admin.nav.teams"))
  const list = useAdminList<AdminTeam>("/api/admin/teams")
  const items = list.data?.items ?? []

  return (
    <PageBody eyebrow={t("nav.platform")} title={t("admin.nav.teams")} description={t("admin.teams.hint")} bleed>
      <ListToolbar q={list.q} onQ={list.setQ} placeholder={t("admin.teams.search")} />
      {list.error ? (
        <ListError error={list.error} />
      ) : !list.data ? (
        <LoadingRows />
      ) : items.length === 0 ? (
        <ListEmpty title={list.q ? t("admin.list.noMatch") : t("admin.teams.empty")} />
      ) : (
        <div className="border-b border-border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="ps-6">{t("admin.teams.name")}</TableHead>
                <TableHead>{t("admin.teams.slug")}</TableHead>
                <TableHead>{t("admin.users.status")}</TableHead>
                <TableHead className="text-end">{t("admin.teams.members")}</TableHead>
                <TableHead className="text-end">{t("admin.nav.servers")}</TableHead>
                <TableHead className="text-end">{t("admin.overview.apps")}</TableHead>
                <TableHead className="pe-6 text-end">{t("admin.teams.created")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((team) => (
                <TableRow key={team.id}>
                  <TableCell className="ps-6">
                    <Link href={`/${locale}/admin/teams/${team.id}`} className="font-medium hover:underline">
                      {team.name}
                    </Link>
                  </TableCell>
                  <TableCell className="font-mono text-muted-foreground" dir="ltr">
                    {team.slug}
                  </TableCell>
                  <TableCell>
                    {team.suspended ? (
                      <Badge variant="danger">{t("admin.teams.suspended")}</Badge>
                    ) : (
                      <Badge variant="outline">{t("admin.users.active")}</Badge>
                    )}
                  </TableCell>
                  <TableCell className="text-end font-mono tabular-nums">{team.members}</TableCell>
                  <TableCell className="text-end font-mono tabular-nums">{team.servers}</TableCell>
                  <TableCell className="text-end font-mono tabular-nums">{team.apps}</TableCell>
                  <TableCell className="pe-6 text-end text-muted-foreground" dir="ltr">
                    {fmt(team.created_at)}
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
