"use client"

import { Badge, type DataTableColumn } from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useMemo, useState } from "react"
import { UserIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { ConfirmButton } from "@/components/confirm-button"
import { useErrorText } from "@/components/form-error"
import { NasaqTable } from "@/components/lists/nasaq-table"
import { ErrorBand, LoadingRows } from "@/components/states"
import { RoleSelect } from "@/components/role-select"
import { apiFetch, type Member, type Role } from "@/lib/api"
import { useFormatDate, useDocumentTitle, useI18n } from "@/lib/i18n"
import { useSession } from "@/lib/session"
import { useTeam } from "@/lib/team"

export default function MembersPage() {
  const { t } = useI18n()
  const { team, manage } = useTeam()
  const { me } = useSession()
  useDocumentTitle(`${t("nav.members")} — ${team.name}`)
  const fmt = useFormatDate()
  const errorText = useErrorText()
  const [members, setMembers] = useState<Member[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      const res = await apiFetch<{ members: Member[] }>(`/api/teams/${team.slug}/members`)
      setMembers(res.members ?? [])
    } catch (err) {
      setError(errorText(err))
      setMembers([])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [team.slug])

  useEffect(() => {
    void load()
  }, [load])

  const changeRole = useCallback(
    async (m: Member, role: Role) => {
      setError(null)
      try {
        await apiFetch(`/api/teams/${team.slug}/members/${m.id}`, { method: "PATCH", body: { role } })
        await load()
      } catch (err) {
        setError(errorText(err))
      }
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [team.slug, load],
  )

  const isSelf = useCallback((m: Member) => String(m.user_id) === String(me.user.id), [me.user.id])

  const columns = useMemo<DataTableColumn<Member>[]>(
    () => [
      {
        id: "email",
        header: t("members.email"),
        label: t("members.email"),
        sortValue: (m) => m.email,
        searchValue: (m) => m.email,
        cell: (m) => (
          <span className="flex min-w-0 items-center gap-3">
            <UserIcon className="size-4 shrink-0 text-muted-foreground" />
            <span dir="ltr" className="truncate font-medium">
              {m.email}
            </span>
            {isSelf(m) ? <Badge variant="outline">{t("members.you")}</Badge> : null}
          </span>
        ),
      },
      {
        id: "role",
        header: t("members.role"),
        label: t("members.role"),
        sortValue: (m) => m.role,
        filterValue: (m) => m.role,
        cell: (m) =>
          manage && !isSelf(m) ? (
            <span onClick={(e) => e.stopPropagation()}>
              <RoleSelect value={m.role} onChange={(r) => changeRole(m, r)} />
            </span>
          ) : (
            <Badge variant="neutral">{t(`roles.${m.role}`)}</Badge>
          ),
      },
      {
        id: "joined",
        header: t("members.joined"),
        label: t("members.joined"),
        sortValue: (m) => m.created_at,
        className: "max-md:hidden",
        headerClassName: "max-md:hidden",
        cell: (m) => (
          <span dir="ltr" className="text-caption text-muted-foreground">
            {fmt(m.created_at)}
          </span>
        ),
      },
      {
        id: "actions",
        header: <span className="sr-only">{t("common.actions")}</span>,
        label: t("common.actions"),
        cell: (m) =>
          manage && !isSelf(m) ? (
            <div className="flex justify-end">
              <ConfirmButton
                label={t("members.remove")}
                title={t("members.removeTitle")}
                description={t("members.removeHint", { email: m.email })}
                confirmLabel={t("members.remove")}
                onConfirm={async () => {
                  try {
                    await apiFetch(`/api/teams/${team.slug}/members/${m.id}`, { method: "DELETE" })
                  } catch (err) {
                    throw new Error(errorText(err))
                  }
                  await load()
                }}
              />
            </div>
          ) : null,
      },
    ],
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [t, manage, fmt, isSelf, changeRole, team.slug, load],
  )

  const roles = useMemo(() => [...new Set((members ?? []).map((m) => m.role))], [members])

  return (
    <PageBody title={t("nav.members")} description={manage ? t("members.description") : t("common.readOnly")} bleed divider={false}>
      <ErrorBand message={error} />
      {members === null ? (
        <LoadingRows />
      ) : (
        <div className="flex flex-col gap-3 px-6 pb-6">
          <NasaqTable
            data={members}
            columns={columns}
            rowId={(m) => String(m.id)}
            label={t("nav.members")}
            rowLabel={(m) => m.email}
            search={t("members.email")}
            facets={[{ column: "role", title: t("members.role"), options: roles.map((r) => ({ value: r, label: t(`roles.${r}`) })) }]}
            empty={t("home.noMatch")}
          />
        </div>
      )}
    </PageBody>
  )
}
