"use client"

import { Badge, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useState } from "react"

import { PageBody } from "@/components/app-shell"
import { ConfirmButton } from "@/components/confirm-button"
import { useErrorText } from "@/components/form-error"
import { ErrorBand, LoadingRows, TableBand } from "@/components/states"
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

  async function changeRole(m: Member, role: Role) {
    setError(null)
    try {
      await apiFetch(`/api/teams/${team.slug}/members/${m.id}`, { method: "PATCH", body: { role } })
      await load()
    } catch (err) {
      setError(errorText(err))
    }
  }

  return (
    <PageBody title={t("nav.members")} description={manage ? t("members.description") : t("common.readOnly")} bleed>
      <ErrorBand message={error} />
      {members === null ? (
        <LoadingRows />
      ) : (
        <TableBand>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("members.email")}</TableHead>
                <TableHead>{t("members.role")}</TableHead>
                <TableHead>{t("members.joined")}</TableHead>
                <TableHead className="text-end">{manage ? t("common.actions") : null}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {members.map((m) => {
                const self = String(m.user_id) === String(me.user.id)
                return (
                  <TableRow key={m.id}>
                    <TableCell>
                      <span dir="ltr">{m.email}</span>
                      {self ? (
                        <Badge variant="outline" className="ms-2">
                          {t("members.you")}
                        </Badge>
                      ) : null}
                    </TableCell>
                    <TableCell>
                      {manage && !self ? (
                        <RoleSelect value={m.role} onChange={(r) => changeRole(m, r)} />
                      ) : (
                        <Badge variant="neutral">{t(`roles.${m.role}`)}</Badge>
                      )}
                    </TableCell>
                    <TableCell dir="ltr" className="text-start">
                      {fmt(m.created_at)}
                    </TableCell>
                    <TableCell className="text-end">
                      {manage && !self ? (
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
                      ) : null}
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        </TableBand>
      )}
    </PageBody>
  )
}
