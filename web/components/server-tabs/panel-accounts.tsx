"use client"

import { Badge, Button, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger, Field, FieldDescription, FieldLabel, Input, SettingsSection, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@fadymondy/nasaq/web"
import Link from "next/link"
import { useCallback, useEffect, useState, type FormEvent } from "react"
import { ExternalLinkIcon, KeyRoundIcon, PlusIcon, Trash2Icon } from "lucide-react"

import { ConfirmButton } from "@/components/confirm-button"
import { FormError, useErrorText } from "@/components/form-error"
import { useLoadedServer } from "@/components/server-tabs/context"
import { EditDialog } from "@/components/server-tabs/list-tools"
import { ErrorBand, LoadingRows } from "@/components/states"
import { apiFetch } from "@/lib/api"
import { useFormatDate, useI18n } from "@/lib/i18n"

type PanelAccount = { id: string; email: string; created_at: string; disabled: boolean }

/**
 * The server's panel accounts (MH-1141): customers who sign in to this server's panel only, with an
 * email and password. They are refused by the Hosbah console.
 */
export function PanelAccounts({ manage }: { manage: boolean }) {
  const { t, locale } = useI18n()
  const errorText = useErrorText()
  const fmt = useFormatDate()
  const { server, path } = useLoadedServer()
  const [accounts, setAccounts] = useState<PanelAccount[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [password, setPassword] = useState("")

  const load = useCallback(async () => {
    try {
      setAccounts((await apiFetch<{ accounts: PanelAccount[] | null }>(`${path}/panel-accounts`)).accounts ?? [])
      setError(null)
    } catch (err) {
      setError(errorText(err))
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path])

  useEffect(() => {
    void load()
  }, [load])

  return (
    <SettingsSection
      title={t("panel.accounts.title")}
      description={t("panel.accounts.hint")}
      actions={
        <div className="flex flex-wrap items-center gap-2">
          <Button variant="secondary" size="sm" nativeButton={false} render={<Link href={`/${locale}/panel/${server.id}`} />}>
            <ExternalLinkIcon />
            {t("panel.accounts.open")}
          </Button>
          {manage ? <AddAccount path={path} onAdded={load} /> : null}
        </div>
      }
    >
      <ErrorBand message={error} />
      {accounts === null ? (
        <LoadingRows rows={2} />
      ) : accounts.length === 0 ? (
        <p className="px-4 py-3 text-sm text-muted-foreground">{t("panel.accounts.empty")}</p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t("panel.accounts.email")}</TableHead>
              <TableHead>{t("panel.accounts.created")}</TableHead>
              {manage ? <TableHead className="text-end" /> : null}
            </TableRow>
          </TableHeader>
          <TableBody>
            {accounts.map((a) => (
              <TableRow key={a.id}>
                <TableCell>
                  <span dir="ltr" className="font-mono text-sm">
                    {a.email}
                  </span>
                  {a.disabled ? (
                    <Badge variant="neutral" className="ms-2">
                      {t("panel.accounts.disabled")}
                    </Badge>
                  ) : null}
                </TableCell>
                <TableCell className="text-muted-foreground">{fmt(a.created_at)}</TableCell>
                {manage ? (
                  <TableCell className="text-end">
                    <div className="inline-flex gap-2">
                      <EditDialog
                        title={t("panel.accounts.resetTitle")}
                        description={a.email}
                        onOpen={() => setPassword("")}
                        onSave={async () => {
                          await apiFetch(`${path}/panel-accounts/${a.id}`, { method: "PATCH", body: { password } })
                        }}
                      >
                        <Field>
                          <FieldLabel htmlFor={`pw-${a.id}`}>
                            <KeyRoundIcon className="size-4" />
                            {t("panel.accounts.reset")}
                          </FieldLabel>
                          <Input id={`pw-${a.id}`} type="password" autoComplete="new-password" minLength={10} required value={password} onChange={(e) => setPassword(e.target.value)} />
                          <FieldDescription>{t("panel.accounts.passwordHint")}</FieldDescription>
                        </Field>
                      </EditDialog>
                      <ConfirmButton
                        icon={<Trash2Icon />}
                        label={t("panel.accounts.remove")}
                        title={t("panel.accounts.removeTitle", { email: a.email })}
                        description={t("panel.accounts.removeHint")}
                        confirmLabel={t("panel.accounts.remove")}
                        onConfirm={async () => {
                          try {
                            await apiFetch(`${path}/panel-accounts/${a.id}`, { method: "DELETE" })
                          } catch (err) {
                            throw new Error(errorText(err))
                          }
                          await load()
                        }}
                      />
                    </div>
                  </TableCell>
                ) : null}
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </SettingsSection>
  )
}

function AddAccount({ path, onAdded }: { path: string; onAdded: () => Promise<void> }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [open, setOpen] = useState(false)
  const [email, setEmail] = useState("")
  const [password, setPassword] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await apiFetch(`${path}/panel-accounts`, { method: "POST", body: { email: email.trim(), password } })
      await onAdded()
      setOpen(false)
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        setOpen(v)
        if (v) {
          setEmail("")
          setPassword("")
          setError(null)
        }
      }}
    >
      <DialogTrigger render={<Button variant="primary" size="sm" />}>
        <PlusIcon />
        {t("panel.accounts.add")}
      </DialogTrigger>
      <DialogContent>
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{t("panel.accounts.add")}</DialogTitle>
            <DialogDescription>{t("panel.accounts.hint")}</DialogDescription>
          </DialogHeader>
          <Field>
            <FieldLabel htmlFor="panel-email">{t("panel.accounts.email")}</FieldLabel>
            <Input id="panel-email" type="email" dir="ltr" autoComplete="off" required value={email} onChange={(e) => setEmail(e.target.value)} />
          </Field>
          <Field>
            <FieldLabel htmlFor="panel-password">{t("panel.accounts.password")}</FieldLabel>
            <Input id="panel-password" type="password" autoComplete="new-password" minLength={10} required value={password} onChange={(e) => setPassword(e.target.value)} />
            <FieldDescription>{t("panel.accounts.passwordHint")}</FieldDescription>
          </Field>
          <FormError message={error} />
          <DialogFooter>
            <Button type="button" variant="secondary" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button variant="primary" type="submit" disabled={busy}>
              {busy ? t("common.working") : t("panel.accounts.add")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
