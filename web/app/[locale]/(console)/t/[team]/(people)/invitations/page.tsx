"use client"

import { Alert, Badge, Button, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger, EmptyState, Field, FieldLabel, Input, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useState, type FormEvent } from "react"
import { MailPlusIcon, PlusIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { ConfirmButton } from "@/components/confirm-button"
import { CopyField } from "@/components/copy-field"
import { FormError, useErrorText } from "@/components/form-error"
import { ErrorBand, LoadingRows, TableBand } from "@/components/states"
import { RoleSelect } from "@/components/role-select"







import { apiFetch, type Invitation, type Role } from "@/lib/api"
import { useDocumentTitle, useFormatDate, useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

export default function InvitationsPage() {
  const { t } = useI18n()
  const { team, manage } = useTeam()
  useDocumentTitle(`${t("nav.invitations")} — ${team.name}`)
  const fmt = useFormatDate()
  const errorText = useErrorText()
  const [items, setItems] = useState<Invitation[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      const res = await apiFetch<{ invitations: Invitation[] }>(`/api/teams/${team.slug}/invitations`)
      setItems(res.invitations ?? [])
    } catch (err) {
      setError(errorText(err))
      setItems([])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [team.slug])

  useEffect(() => {
    void load()
  }, [load])

  return (
    <PageBody
      title={t("nav.invitations")}
      description={manage ? t("invitations.description") : t("common.readOnly")}
      actions={manage ? <InviteDialog slug={team.slug} onInvited={load} /> : null}
      bleed
    >
      <ErrorBand message={error} />
      {items === null ? (
        <LoadingRows />
      ) : items.length === 0 && !error ? (
        <EmptyState className="border-b" icon={MailPlusIcon} title={t("invitations.emptyTitle")} description={t("invitations.emptyHint")} />
      ) : items.length ? (
        <TableBand>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("members.email")}</TableHead>
                <TableHead>{t("members.role")}</TableHead>
                <TableHead>{t("invitations.statusLabel")}</TableHead>
                <TableHead>{t("invitations.expires")}</TableHead>
                <TableHead className="text-end">{manage ? t("common.actions") : null}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((inv) => (
                <TableRow key={inv.id}>
                  <TableCell dir="ltr" className="text-start">
                    {inv.email}
                  </TableCell>
                  <TableCell>
                    <Badge variant="neutral">{t(`roles.${inv.role}`)}</Badge>
                  </TableCell>
                  <TableCell>
                    <Badge variant="outline">{t(`invitations.status.${inv.status}`, undefined, inv.status)}</Badge>
                  </TableCell>
                  <TableCell dir="ltr" className="text-start">
                    {fmt(inv.expires_at)}
                  </TableCell>
                  <TableCell className="text-end">
                    {manage && inv.status === "pending" ? (
                      <ConfirmButton
                        label={t("invitations.revoke")}
                        title={t("invitations.revokeTitle")}
                        description={t("invitations.revokeHint", { email: inv.email })}
                        confirmLabel={t("invitations.revoke")}
                        onConfirm={async () => {
                          try {
                            await apiFetch(`/api/teams/${team.slug}/invitations/${inv.id}`, { method: "DELETE" })
                          } catch (err) {
                            throw new Error(errorText(err))
                          }
                          await load()
                        }}
                      />
                    ) : null}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </TableBand>
      ) : null}
    </PageBody>
  )
}

function InviteDialog({ slug, onInvited }: { slug: string; onInvited: () => void }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [open, setOpen] = useState(false)
  const [email, setEmail] = useState("")
  const [role, setRole] = useState<Role>("viewer")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [acceptUrl, setAcceptUrl] = useState<string | null>(null)

  function reset() {
    setEmail("")
    setRole("viewer")
    setError(null)
    setAcceptUrl(null)
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const res = await apiFetch<{ accept_url: string }>(`/api/teams/${slug}/invitations`, {
        method: "POST",
        body: { email: email.trim(), role },
      })
      // The API may answer with a path; make it a link someone can open.
      setAcceptUrl(new URL(res.accept_url, window.location.origin).toString())
      onInvited()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        setOpen(o)
        if (!o) reset()
      }}
    >
      <DialogTrigger render={<Button variant="primary" />}>
        <PlusIcon />
        {t("invitations.invite")}
      </DialogTrigger>
      <DialogContent>
        {acceptUrl ? (
          <div className="flex flex-col gap-4">
            <DialogHeader>
              <DialogTitle>{t("invitations.createdTitle")}</DialogTitle>
              <DialogDescription>{t("invitations.createdHint")}</DialogDescription>
            </DialogHeader>
            <Alert tone="info" title={t("invitations.noEmailTitle")}>
{t("invitations.noEmailHint")}
</Alert>
            <CopyField value={acceptUrl} />
            <DialogFooter>
              <Button variant="primary" onClick={() => setOpen(false)}>{t("common.done")}</Button>
            </DialogFooter>
          </div>
        ) : (
          <form onSubmit={submit} className="flex flex-col gap-4">
            <DialogHeader>
              <DialogTitle>{t("invitations.invite")}</DialogTitle>
              <DialogDescription>{t("invitations.inviteHint")}</DialogDescription>
            </DialogHeader>
            <div className="flex w-full flex-col gap-5">
              <FormError message={error} />
              <Field>
                <FieldLabel htmlFor="invite-email">{t("members.email")}</FieldLabel>
                <Input
                  id="invite-email"
                  type="email"
                  dir="ltr"
                  required
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                />
              </Field>
              <Field>
                <FieldLabel htmlFor="invite-role">{t("members.role")}</FieldLabel>
                <RoleSelect id="invite-role" value={role} onChange={setRole} className="w-full" />
              </Field>
            </div>
            <DialogFooter>
              <Button variant="primary" type="submit" disabled={busy || !email.trim()}>
                {busy ? t("common.working") : t("invitations.send")}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  )
}
