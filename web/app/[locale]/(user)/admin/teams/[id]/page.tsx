"use client"

import Link from "next/link"
import { useParams } from "next/navigation"
import { useState } from "react"
import { PauseCircleIcon, PlayCircleIcon } from "lucide-react"
import { toast } from "sonner"

import { Facts, ListEmpty, ListError, LoadingRows, SectionTitle, ServerStatus } from "@/components/admin/admin-list"
import { QuotaEditor } from "@/components/admin/quota-editor"
import { PageBody } from "@/components/app-shell"
import { ConfirmButton } from "@/components/confirm-button"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { useAdminResource, type AdminTeamDetail } from "@/lib/admin"
import { apiFetch, ApiError } from "@/lib/api"
import { useDocumentTitle, useFormatDate, useI18n } from "@/lib/i18n"

export default function AdminTeamPage() {
  const { id } = useParams<{ id: string }>()
  const { t, locale } = useI18n()
  const fmt = useFormatDate()
  const res = useAdminResource<AdminTeamDetail>(`/api/admin/teams/${encodeURIComponent(id)}`)
  const [busy, setBusy] = useState(false)
  useDocumentTitle(res.data?.team.name ?? t("admin.nav.teams"))

  async function setSuspended(suspended: boolean) {
    setBusy(true)
    try {
      await apiFetch(`/api/admin/teams/${encodeURIComponent(id)}`, { method: "PATCH", body: { suspended } })
      toast.success(suspended ? t("admin.teams.suspendedDone") : t("admin.teams.reinstated"))
      await res.reload()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("admin.list.network"))
      throw e
    } finally {
      setBusy(false)
    }
  }

  const back = (
    <Link href={`/${locale}/admin/teams`} className="hover:underline">
      {t("admin.nav.teams")}
    </Link>
  )
  if (res.error) {
    return (
      <PageBody eyebrow={back} title={t("admin.teams.notFound")} bleed>
        <ListError error={res.error} />
      </PageBody>
    )
  }
  if (!res.data) {
    return (
      <PageBody eyebrow={back} title="…" bleed>
        <LoadingRows rows={3} />
      </PageBody>
    )
  }
  const { team, members, servers, apps, counts } = res.data

  return (
    <PageBody
      eyebrow={back}
      title={team.name}
      description={
        <span dir="ltr" className="font-mono">
          /{team.slug}
        </span>
      }
      actions={team.suspended ? <Badge variant="destructive">{t("admin.teams.suspended")}</Badge> : null}
      bleed
    >
      <div className="flex flex-wrap gap-2 px-6 py-6">
        {team.suspended ? (
          <Button variant="outline" disabled={busy} onClick={() => setSuspended(false).catch(() => {})}>
            <PlayCircleIcon />
            {t("admin.teams.reinstate")}
          </Button>
        ) : (
          <ConfirmButton
            size="default"
            icon={<PauseCircleIcon />}
            label={t("admin.teams.suspend")}
            title={t("admin.teams.suspendTitle")}
            description={t("admin.teams.suspendBody", { name: team.name })}
            confirmLabel={t("admin.teams.suspend")}
            onConfirm={() => setSuspended(true)}
          />
        )}
      </div>
      {team.suspended && team.suspended_at ? (
        <p className="border-t border-line px-6 py-3 text-sm text-grid-muted">
          {t("admin.teams.suspendedSince")} <span dir="ltr">{fmt(team.suspended_at)}</span>
        </p>
      ) : null}

      <div className="border-t border-line">
        <Facts
          items={[
            [t("admin.teams.members"), <span key="m" className="font-mono">{counts.members}</span>],
            [t("admin.nav.servers"), <span key="s" className="font-mono">{counts.servers}</span>],
            [t("admin.overview.apps"), <span key="a" className="font-mono">{counts.apps}</span>],
            [t("admin.teams.databases"), <span key="d" className="font-mono">{counts.databases}</span>],
          ]}
        />
      </div>

      <QuotaEditor teamId={team.id} />

      <SectionTitle>{t("admin.teams.members")}</SectionTitle>
      {members.length === 0 ? (
        <ListEmpty title={t("admin.teams.noMembers")} />
      ) : (
        <div className="border-y border-line">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="ps-6">{t("admin.users.email")}</TableHead>
                <TableHead className="pe-6 text-end">{t("admin.users.role")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {members.map((m) => (
                <TableRow key={m.user_id}>
                  <TableCell className="ps-6">
                    <Link href={`/${locale}/admin/users/${m.user_id}`} className="font-medium hover:underline" dir="ltr">
                      {m.email}
                    </Link>
                  </TableCell>
                  <TableCell className="pe-6 text-end">{t(`roles.${m.role}`)}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}

      <SectionTitle>{t("admin.nav.servers")}</SectionTitle>
      {servers.length === 0 ? (
        <ListEmpty title={t("admin.servers.empty")} />
      ) : (
        <div className="border-y border-line">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="ps-6">{t("admin.servers.name")}</TableHead>
                <TableHead>{t("admin.servers.provider")}</TableHead>
                <TableHead>{t("admin.users.status")}</TableHead>
                <TableHead className="pe-6 text-end">{t("admin.servers.lastSeen")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {servers.map((s) => (
                <TableRow key={s.id}>
                  <TableCell className="ps-6 font-medium">{s.name}</TableCell>
                  <TableCell className="text-grid-muted">
                    {s.provider} · {s.kind}
                  </TableCell>
                  <TableCell>
                    <ServerStatus online={s.online} status={s.status} />
                  </TableCell>
                  <TableCell className="pe-6 text-end text-grid-muted" dir="ltr">
                    {fmt(s.last_seen_at)}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}

      <SectionTitle>{t("admin.overview.apps")}</SectionTitle>
      {apps.length === 0 ? (
        <ListEmpty title={t("admin.teams.noApps")} />
      ) : (
        <div className="border-y border-line">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="ps-6">{t("admin.servers.name")}</TableHead>
                <TableHead className="pe-6 text-end">{t("admin.servers.server")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {apps.map((a) => (
                <TableRow key={a.id}>
                  <TableCell className="ps-6 font-medium">{a.name}</TableCell>
                  <TableCell className="pe-6 text-end text-grid-muted">{a.server_name}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </PageBody>
  )
}

