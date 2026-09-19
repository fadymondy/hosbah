"use client"

import Link from "next/link"
import { ServerIcon, SlidersHorizontalIcon, UsersIcon, UsersRoundIcon, WorkflowIcon } from "lucide-react"

import { Facts, ListError, LoadingRows, SectionTitle } from "@/components/admin/admin-list"
import { PageBody } from "@/components/app-shell"
import { Button } from "@/components/ui/button"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { useAdminResource, type AdminOverview } from "@/lib/admin"
import { useDocumentTitle, useFormatDate, useI18n } from "@/lib/i18n"

export default function AdminOverviewPage() {
  const { t, locale } = useI18n()
  const fmt = useFormatDate()
  useDocumentTitle(t("admin.title"))
  const o = useAdminResource<AdminOverview>("/api/admin/overview")
  const base = `/${locale}/admin`

  const figure = (n: number, note?: string, warn = false) => (
    <span className="flex items-baseline gap-2">
      <span className="font-mono text-xl tabular-nums">{n}</span>
      {note ? <span className={warn ? "text-xs text-grid-danger" : "text-xs text-grid-muted"}>{note}</span> : null}
    </span>
  )

  return (
    <PageBody eyebrow={t("nav.platform")} title={t("admin.title")} description={t("admin.overviewHint")} bleed>
      <div className="flex flex-wrap gap-2 px-6 py-6">
        <Button nativeButton={false} render={<Link href={`${base}/users`} />}>
          <UsersIcon />
          {t("admin.nav.users")}
        </Button>
        <Button variant="outline" nativeButton={false} render={<Link href={`${base}/teams`} />}>
          <UsersRoundIcon />
          {t("admin.nav.teams")}
        </Button>
        <Button variant="outline" nativeButton={false} render={<Link href={`${base}/servers`} />}>
          <ServerIcon />
          {t("admin.nav.servers")}
        </Button>
        <Button variant="outline" nativeButton={false} render={<Link href={`${base}/operations`} />}>
          <WorkflowIcon />
          {t("admin.nav.operations")}
        </Button>
        <Button variant="outline" nativeButton={false} render={<Link href={`${base}/settings`} />}>
          <SlidersHorizontalIcon />
          {t("admin.nav.settings")}
        </Button>
      </div>

      {o.error ? (
        <ListError error={o.error} />
      ) : !o.data ? (
        <LoadingRows rows={2} />
      ) : (
        <>
          <div className="border-t border-line">
            <Facts
              items={[
                [t("admin.nav.users"), figure(o.data.users, t("admin.overview.admins", { count: o.data.platform_admins }))],
                [t("admin.nav.teams"), figure(o.data.teams)],
                [
                  t("admin.nav.servers"),
                  figure(o.data.servers.total, t("admin.overview.online", { count: o.data.servers.online })),
                ],
                [t("admin.overview.apps"), figure(o.data.apps)],
              ]}
            />
            <Facts
              items={[
                [
                  t("admin.overview.deploys24h"),
                  figure(
                    o.data.deployments_last_24h.total,
                    t("admin.overview.failed", { count: o.data.deployments_last_24h.failed }),
                    o.data.deployments_last_24h.failed > 0,
                  ),
                ],
                [
                  t("admin.overview.operations24h"),
                  figure(
                    o.data.operations_last_24h.total,
                    t("admin.overview.failed", { count: o.data.operations_last_24h.failed }),
                    o.data.operations_last_24h.failed > 0,
                  ),
                ],
                [t("admin.overview.offline"), figure(o.data.servers.offline, undefined, o.data.servers.offline > 0)],
                [t("admin.teams.databases"), figure(o.data.databases)],
              ]}
            />
          </div>

          <SectionTitle
            action={
              <Button variant="ghost" size="sm" nativeButton={false} render={<Link href={`${base}/users`} />}>
                {t("admin.overview.allUsers")}
              </Button>
            }
          >
            {t("admin.overview.recentSignups")}
          </SectionTitle>
          <div className="border-y border-line">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="ps-6">{t("admin.users.email")}</TableHead>
                  <TableHead className="pe-6 text-end">{t("admin.users.joined")}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {o.data.recent_signups.map((u) => (
                  <TableRow key={u.id}>
                    <TableCell className="ps-6">
                      <Link href={`${base}/users/${u.id}`} className="font-medium hover:underline" dir="ltr">
                        {u.email}
                      </Link>
                    </TableCell>
                    <TableCell className="pe-6 text-end text-grid-muted" dir="ltr">
                      {fmt(u.created_at)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        </>
      )}
    </PageBody>
  )
}
