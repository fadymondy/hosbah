"use client"

import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@fadymondy/nasaq/web"
import Link from "next/link"
import { useState } from "react"

import { FilterSelect, ListEmpty, ListError, ListToolbar, LoadingRows, Pager, ServerStatus } from "@/components/admin/admin-list"
import { PageBody } from "@/components/app-shell"

import { useAdminList, type AdminServer } from "@/lib/admin"
import { useDocumentTitle, useFormatDate, useI18n } from "@/lib/i18n"

const STATUSES = ["online", "offline", "enrolled", "provisioning", "pending", "failed"] as const

export default function AdminServersPage() {
  const { t, locale } = useI18n()
  const fmt = useFormatDate()
  useDocumentTitle(t("admin.nav.servers"))
  const [status, setStatus] = useState("")
  const list = useAdminList<AdminServer>("/api/admin/servers", { status })
  const items = list.data?.items ?? []

  return (
    <PageBody eyebrow={t("nav.platform")} title={t("admin.nav.servers")} description={t("admin.servers.hint")} bleed>
      <ListToolbar q={list.q} onQ={list.setQ} placeholder={t("admin.servers.search")}>
        <FilterSelect
          value={status}
          onValue={setStatus}
          label={t("admin.users.status")}
          options={STATUSES.map((s) => ({ value: s, label: t(`admin.servers.status.${s}`) }))}
        />
      </ListToolbar>
      {list.error ? (
        <ListError error={list.error} />
      ) : !list.data ? (
        <LoadingRows />
      ) : items.length === 0 ? (
        <ListEmpty title={list.q || status ? t("admin.list.noMatch") : t("admin.servers.empty")} />
      ) : (
        <div className="border-b border-border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="ps-6">{t("admin.servers.name")}</TableHead>
                <TableHead>{t("admin.servers.team")}</TableHead>
                <TableHead>{t("admin.servers.provider")}</TableHead>
                <TableHead>{t("admin.users.status")}</TableHead>
                <TableHead>{t("admin.servers.address")}</TableHead>
                <TableHead>{t("admin.servers.agent")}</TableHead>
                <TableHead className="pe-6 text-end">{t("admin.servers.lastSeen")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((s) => (
                <TableRow key={s.id}>
                  <TableCell className="ps-6 font-medium">{s.name}</TableCell>
                  <TableCell>
                    <Link href={`/${locale}/admin/teams/${s.team_id}`} className="hover:underline">
                      {s.team_name}
                    </Link>
                  </TableCell>
                  <TableCell className="text-muted-foreground">
                    {s.provider} · {s.kind}
                  </TableCell>
                  <TableCell>
                    <ServerStatus online={s.online} status={s.status} />
                  </TableCell>
                  <TableCell className="font-mono text-muted-foreground" dir="ltr">
                    {s.ip ?? "—"}
                  </TableCell>
                  <TableCell className="font-mono text-xs text-muted-foreground" dir="ltr">
                    {s.agent_version ?? "—"}
                  </TableCell>
                  <TableCell className="pe-6 text-end text-muted-foreground" dir="ltr">
                    {fmt(s.last_seen_at)}
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
