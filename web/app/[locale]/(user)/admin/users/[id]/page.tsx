"use client"

import Link from "next/link"
import { useParams, useRouter } from "next/navigation"
import { useState } from "react"
import { BanIcon, CheckCircle2Icon, ShieldIcon, ShieldOffIcon, Trash2Icon } from "lucide-react"
import { toast } from "sonner"

import { Facts, ListEmpty, ListError, LoadingRows, SectionTitle } from "@/components/admin/admin-list"
import { PageBody } from "@/components/app-shell"
import { ConfirmButton } from "@/components/confirm-button"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { useAdminResource, type AdminUserDetail } from "@/lib/admin"
import { apiFetch, ApiError } from "@/lib/api"
import { useDocumentTitle, useFormatDate, useI18n } from "@/lib/i18n"
import { useSession } from "@/lib/session"

export default function AdminUserPage() {
  const { id } = useParams<{ id: string }>()
  const { t, locale } = useI18n()
  const fmt = useFormatDate()
  const router = useRouter()
  const { me } = useSession()
  const user = useAdminResource<AdminUserDetail>(`/api/admin/users/${encodeURIComponent(id)}`)
  const [busy, setBusy] = useState(false)
  useDocumentTitle(user.data?.email ?? t("admin.nav.users"))
  const self = user.data?.id === me.user.id

  async function patch(body: { platform_admin?: boolean; disabled?: boolean }, done: string) {
    setBusy(true)
    try {
      await apiFetch(`/api/admin/users/${encodeURIComponent(id)}`, { method: "PATCH", body })
      toast.success(done)
      await user.reload()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("admin.list.network"))
    } finally {
      setBusy(false)
    }
  }

  async function remove() {
    await apiFetch(`/api/admin/users/${encodeURIComponent(id)}`, { method: "DELETE" })
    toast.success(t("admin.users.deleted"))
    router.replace(`/${locale}/admin/users`)
  }

  if (user.error) {
    return (
      <PageBody eyebrow={t("admin.nav.users")} title={t("admin.users.notFound")} bleed>
        <ListError error={user.error} />
      </PageBody>
    )
  }
  if (!user.data) {
    return (
      <PageBody eyebrow={t("admin.nav.users")} title="…" bleed>
        <LoadingRows rows={3} />
      </PageBody>
    )
  }
  const u = user.data

  return (
    <PageBody
      eyebrow={
        <Link href={`/${locale}/admin/users`} className="hover:underline">
          {t("admin.nav.users")}
        </Link>
      }
      title={<span dir="ltr">{u.email}</span>}
      actions={
        <>
          {u.platform_admin ? <Badge>{t("admin.users.platformAdmin")}</Badge> : null}
          {u.disabled ? <Badge variant="destructive">{t("admin.users.disabled")}</Badge> : null}
        </>
      }
      bleed
    >
      <div className="flex flex-wrap gap-2 px-6 py-6">
        <Button
          variant="outline"
          disabled={busy || self}
          title={self ? t("admin.users.notSelf") : undefined}
          onClick={() =>
            patch(
              { platform_admin: !u.platform_admin },
              u.platform_admin ? t("admin.users.demoted") : t("admin.users.promoted"),
            )
          }
        >
          {u.platform_admin ? <ShieldOffIcon /> : <ShieldIcon />}
          {u.platform_admin ? t("admin.users.demote") : t("admin.users.promote")}
        </Button>
        <Button
          variant="outline"
          disabled={busy || self}
          title={self ? t("admin.users.notSelf") : undefined}
          onClick={() =>
            patch({ disabled: !u.disabled }, u.disabled ? t("admin.users.enabled") : t("admin.users.disabledDone"))
          }
        >
          {u.disabled ? <CheckCircle2Icon /> : <BanIcon />}
          {u.disabled ? t("admin.users.enable") : t("admin.users.disable")}
        </Button>
        {!self ? (
          <ConfirmButton
            size="default"
            icon={<Trash2Icon />}
            label={t("admin.users.delete")}
            title={t("admin.users.deleteTitle")}
            description={t("admin.users.deleteBody", { email: u.email })}
            confirmLabel={t("admin.users.delete")}
            onConfirm={remove}
          />
        ) : null}
      </div>
      {u.disabled ? (
        <p className="border-t border-line px-6 py-3 text-sm text-grid-muted">{t("admin.users.disabledNote")}</p>
      ) : null}

      <div className="border-t border-line">
        <Facts
          items={[
            [t("admin.users.joined"), <span key="j" dir="ltr">{fmt(u.created_at)}</span>],
            [t("admin.users.teams"), <span key="t" className="font-mono">{u.team_count}</span>],
            [t("admin.users.mcpTokens"), <span key="m" className="font-mono">{u.mcp_token_count}</span>],
            [t("admin.users.adminTokens"), <span key="a" className="font-mono">{u.admin_mcp_token_count}</span>],
          ]}
        />
      </div>

      <SectionTitle>{t("admin.users.teams")}</SectionTitle>
      {u.teams.length === 0 ? (
        <ListEmpty title={t("admin.users.noTeams")} />
      ) : (
        <div className="border-y border-line">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="ps-6">{t("admin.teams.name")}</TableHead>
                <TableHead>{t("admin.teams.slug")}</TableHead>
                <TableHead className="pe-6 text-end">{t("admin.users.role")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {u.teams.map((team) => (
                <TableRow key={team.id}>
                  <TableCell className="ps-6">
                    <Link href={`/${locale}/admin/teams/${team.id}`} className="font-medium hover:underline">
                      {team.name}
                    </Link>
                  </TableCell>
                  <TableCell className="font-mono text-grid-muted" dir="ltr">
                    {team.slug}
                  </TableCell>
                  <TableCell className="pe-6 text-end">{t(`roles.${team.role}`)}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </PageBody>
  )
}
