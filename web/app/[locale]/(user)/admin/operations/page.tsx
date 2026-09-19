"use client"

import Link from "next/link"
import { useState } from "react"

import { FilterSelect, ListEmpty, ListError, ListToolbar, LoadingRows, Pager } from "@/components/admin/admin-list"
import { PageBody } from "@/components/app-shell"
import { Badge } from "@/components/ui/badge"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { useAdminList, type AdminOperation } from "@/lib/admin"
import { useDocumentTitle, useFormatDate, useI18n } from "@/lib/i18n"

const STATUSES = ["pending", "sent", "running", "ok", "failed", "timeout"] as const

function StatusBadge({ status }: { status: string }) {
  const { t } = useI18n()
  const label = t(`admin.operations.status.${status}`, undefined, status)
  if (status === "failed" || status === "timeout") return <Badge variant="destructive">{label}</Badge>
  if (status === "ok") return <Badge variant="outline">{label}</Badge>
  return <Badge variant="secondary">{label}</Badge>
}

export default function AdminOperationsPage() {
  const { t, locale } = useI18n()
  const fmt = useFormatDate()
  useDocumentTitle(t("admin.nav.operations"))
  const [status, setStatus] = useState("")
  const [kind, setKind] = useState("")
  const list = useAdminList<AdminOperation>("/api/admin/operations", { status, kind: kind.trim() || undefined })
  const items = list.data?.items ?? []

  return (
    <PageBody eyebrow={t("nav.platform")} title={t("admin.nav.operations")} description={t("admin.operations.hint")} bleed>
      <ListToolbar>
        <input
          value={kind}
          onChange={(e) => setKind(e.target.value)}
          placeholder={t("admin.operations.kind")}
          aria-label={t("admin.operations.kind")}
          dir="ltr"
          className="h-8 w-44 border border-line bg-background px-2 font-mono text-sm text-grid-fg placeholder:font-sans placeholder:text-grid-muted"
        />
        <FilterSelect
          value={status}
          onValue={setStatus}
          label={t("admin.users.status")}
          options={STATUSES.map((s) => ({ value: s, label: t(`admin.operations.status.${s}`) }))}
        />
      </ListToolbar>
      {list.error ? (
        <ListError error={list.error} />
      ) : !list.data ? (
        <LoadingRows />
      ) : items.length === 0 ? (
        <ListEmpty title={status || kind ? t("admin.list.noMatch") : t("admin.operations.empty")} />
      ) : (
        <div className="border-b border-line">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="ps-6">{t("admin.operations.kind")}</TableHead>
                <TableHead>{t("admin.users.status")}</TableHead>
                <TableHead>{t("admin.servers.team")}</TableHead>
                <TableHead>{t("admin.servers.server")}</TableHead>
                <TableHead>{t("admin.operations.error")}</TableHead>
                <TableHead className="pe-6 text-end">{t("admin.operations.started")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((op) => (
                <TableRow key={op.id}>
                  <TableCell className="ps-6 font-mono text-xs" dir="ltr">
                    {op.kind}
                  </TableCell>
                  <TableCell>
                    <StatusBadge status={op.status} />
                    {op.exit_code !== null && op.exit_code !== 0 ? (
                      <span className="ms-2 font-mono text-xs text-grid-muted" dir="ltr">
                        exit {op.exit_code}
                      </span>
                    ) : null}
                  </TableCell>
                  <TableCell>
                    <Link href={`/${locale}/admin/teams/${op.team_id}`} className="hover:underline">
                      {op.team_name}
                    </Link>
                  </TableCell>
                  <TableCell className="text-grid-muted">{op.server_name}</TableCell>
                  <TableCell className="max-w-xs">
                    {op.error ? (
                      <span className="line-clamp-2 font-mono text-xs text-grid-danger" dir="ltr" title={op.error}>
                        {op.error}
                      </span>
                    ) : (
                      <span className="text-grid-muted">—</span>
                    )}
                  </TableCell>
                  <TableCell className="pe-6 text-end text-grid-muted" dir="ltr">
                    {fmt(op.created_at)}
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
