"use client"

import { useCallback, useEffect, useState, type FormEvent } from "react"
import { KeyRoundIcon, PlusIcon } from "lucide-react"

import { ConfirmButton } from "@/components/confirm-button"
import { CopyField } from "@/components/copy-field"
import { ListSearch, matches } from "@/components/server-tabs/list-tools"
import { useLoadedServer } from "@/components/server-tabs/context"
import { FormError, useErrorText } from "@/components/form-error"
import { ErrorBand, LoadingRows, SectionTitle, TableBand } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Textarea } from "@/components/ui/textarea"
import { apiFetch } from "@/lib/api"
import { useI18n } from "@/lib/i18n"
import type { SFTPUser } from "@/lib/servers"
import { useTeam } from "@/lib/team"

/** The chrooted, sftp-only system users of one server: upload/download access without root. */
export function ServerSFTP({ serverId }: { serverId: string }) {
  const { t } = useI18n()
  const { team, manage } = useTeam()
  const { server } = useLoadedServer()
  const errorText = useErrorText()
  const [users, setUsers] = useState<SFTPUser[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [q, setQ] = useState("")
  const shown = (users ?? []).filter((u) => matches(q, u.username, u.root_dir))
  const host = server.ip_address ?? server.hostname ?? ""

  const load = useCallback(async () => {
    try {
      const r = await apiFetch<{ sftp_users: SFTPUser[] }>(`/api/teams/${team.slug}/servers/${serverId}/sftp-users`)
      setUsers(r.sftp_users ?? [])
    } catch (err) {
      setError(errorText(err))
      setUsers([])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [team.slug, serverId])

  useEffect(() => {
    void load()
  }, [load])

  return (
    <section aria-labelledby="tab-h">
      <SectionTitle
        id="tab-h"
        title={t("sftp.title")}
        hint={t("sftp.hint")}
        action={manage && server.status === "enrolled" ? <AddSFTPUser serverId={serverId} onSaved={load} /> : null}
      />
      <ErrorBand message={error} />
      {host ? (
        <div className="border-b border-line px-6 py-4">
          <p className="mb-2 text-sm text-grid-muted">{t("sftp.connection")}</p>
          <div className="grid gap-2 sm:grid-cols-2">
            <div>
              <p className="mb-1 text-xs text-grid-muted">{t("sftp.host")}</p>
              <CopyField value={host} />
            </div>
            <div>
              <p className="mb-1 text-xs text-grid-muted">{t("sftp.port")}</p>
              <CopyField value="22" />
            </div>
          </div>
        </div>
      ) : null}
      {users === null ? (
        <LoadingRows />
      ) : users.length === 0 ? (
        <Empty className="border-b">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <KeyRoundIcon />
            </EmptyMedia>
            <EmptyTitle>{t("sftp.emptyTitle")}</EmptyTitle>
            <EmptyDescription>{t("sftp.emptyHint")}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <>
          <ListSearch value={q} onChange={setQ} placeholder={t("lists.searchSftp")} shown={shown.length} total={users.length} />
          {shown.length === 0 ? <p className="border-b border-line px-6 py-8 text-center text-sm text-grid-muted">{t("home.noMatch")}</p> : null}
          <TableBand>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t("sftp.username")}</TableHead>
                  <TableHead>{t("sftp.rootDir")}</TableHead>
                  <TableHead>{t("sftp.access")}</TableHead>
                  <TableHead>{t("sftp.statusHeader")}</TableHead>
                  {manage ? <TableHead className="text-end">{t("common.actions")}</TableHead> : null}
                </TableRow>
              </TableHeader>
              <TableBody>
                {shown.map((u) => (
                  <TableRow key={u.id}>
                    <TableCell>
                      <span dir="ltr" className="font-medium">
                        {u.username}
                      </span>
                      <div className="mt-1 flex flex-wrap gap-1">
                        <Badge variant="outline">{u.public_keys.length ? t("sftp.keys", { n: u.public_keys.length }) : t("sftp.noKeys")}</Badge>
                        <Badge variant="outline">{u.has_password ? t("sftp.hasPassword") : t("sftp.noPassword")}</Badge>
                      </div>
                      {u.status === "failed" && u.error ? <p className="mt-1 text-xs text-destructive">{u.error}</p> : null}
                    </TableCell>
                    <TableCell dir="ltr" className="text-start font-mono text-xs">
                      {u.root_dir}
                    </TableCell>
                    <TableCell>
                      <Badge variant={u.read_only ? "outline" : "secondary"}>{u.read_only ? t("sftp.readOnly") : t("sftp.readWrite")}</Badge>
                    </TableCell>
                    <TableCell>
                      <Badge variant={u.status === "active" ? "secondary" : u.status === "failed" ? "destructive" : "outline"}>
                        {t(`sftp.status.${u.status}`)}
                      </Badge>
                    </TableCell>
                    {manage ? (
                      <TableCell>
                        <div className="flex flex-wrap justify-end gap-2">
                          <EditSFTPUser user={u} team={team.slug} serverId={serverId} onSaved={load} />
                          <ConfirmButton
                            label={t("sftp.remove")}
                            title={t("sftp.remove")}
                            description={t("sftp.removeConfirm", { user: u.username, server: server.name })}
                            confirmLabel={t("sftp.remove")}
                            onConfirm={async () => {
                              try {
                                await apiFetch(`/api/teams/${team.slug}/servers/${serverId}/sftp-users/${u.id}`, { method: "DELETE" })
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
          </TableBand>
        </>
      )}
    </section>
  )
}

function AddSFTPUser({ serverId, onSaved }: { serverId: string; onSaved: () => Promise<void> }) {
  const { t } = useI18n()
  const { team } = useTeam()
  const errorText = useErrorText()
  const [open, setOpen] = useState(false)
  const [username, setUsername] = useState("")
  const [rootDir, setRootDir] = useState("")
  const [keys, setKeys] = useState("")
  const [genPassword, setGenPassword] = useState(false)
  const [readOnly, setReadOnly] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [shownPassword, setShownPassword] = useState<string | null>(null)

  function reset() {
    setUsername("")
    setRootDir("")
    setKeys("")
    setGenPassword(false)
    setReadOnly(false)
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const publicKeys = keys
        .split("\n")
        .map((k) => k.trim())
        .filter(Boolean)
      const r = await apiFetch<{ password?: string }>(`/api/teams/${team.slug}/servers/${serverId}/sftp-users`, {
        method: "POST",
        body: { username: username.trim(), root_dir: rootDir.trim(), public_keys: publicKeys, generate_password: genPassword, read_only: readOnly },
      })
      reset()
      await onSaved()
      if (r.password) {
        setShownPassword(r.password)
      } else {
        setOpen(false)
      }
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <Dialog
        open={open}
        onOpenChange={(v) => {
          setOpen(v)
          if (!v) {
            setShownPassword(null)
            setError(null)
          }
        }}
      >
        <DialogTrigger render={<Button />}>
          <PlusIcon />
          {t("sftp.add")}
        </DialogTrigger>
        <DialogContent className="max-h-[90dvh] overflow-y-auto">
          {shownPassword ? (
            <div className="flex flex-col gap-4">
              <DialogHeader>
                <DialogTitle>{t("sftp.add")}</DialogTitle>
                <DialogDescription>{t("sftp.passwordShown")}</DialogDescription>
              </DialogHeader>
              <CopyField value={shownPassword} />
              <DialogFooter>
                <Button
                  type="button"
                  onClick={() => {
                    setOpen(false)
                    setShownPassword(null)
                  }}
                >
                  {t("common.done")}
                </Button>
              </DialogFooter>
            </div>
          ) : (
            <form onSubmit={submit} className="flex flex-col gap-4">
              <DialogHeader>
                <DialogTitle>{t("sftp.add")}</DialogTitle>
                <DialogDescription>{t("sftp.addHint")}</DialogDescription>
              </DialogHeader>
              <FieldGroup className="gap-4">
                <Field>
                  <FieldLabel htmlFor="sf-user">{t("sftp.username")}</FieldLabel>
                  <Input
                    id="sf-user"
                    dir="ltr"
                    required
                    maxLength={32}
                    placeholder="uploads"
                    value={username}
                    onChange={(e) => setUsername(e.target.value)}
                    autoComplete="off"
                  />
                  <FieldDescription>{t("sftp.usernameHint")}</FieldDescription>
                </Field>
                <Field>
                  <FieldLabel htmlFor="sf-root">{t("sftp.rootDir")}</FieldLabel>
                  <Input
                    id="sf-root"
                    dir="ltr"
                    required
                    maxLength={4096}
                    placeholder="/srv/uploads"
                    value={rootDir}
                    onChange={(e) => setRootDir(e.target.value)}
                    autoComplete="off"
                  />
                  <FieldDescription>{t("sftp.rootDirHint")}</FieldDescription>
                </Field>
                <Field>
                  <FieldLabel htmlFor="sf-keys">{t("sftp.publicKeys")}</FieldLabel>
                  <Textarea
                    id="sf-keys"
                    dir="ltr"
                    rows={3}
                    placeholder="ssh-ed25519 AAAA... me@laptop"
                    value={keys}
                    onChange={(e) => setKeys(e.target.value)}
                  />
                  <FieldDescription>{t("sftp.publicKeysHint")}</FieldDescription>
                </Field>
                <Field orientation="horizontal">
                  <Checkbox id="sf-genpw" checked={genPassword} onCheckedChange={(v) => setGenPassword(v === true)} />
                  <FieldLabel htmlFor="sf-genpw" className="font-normal">
                    {t("sftp.generatePassword")}
                  </FieldLabel>
                </Field>
                <Field orientation="horizontal">
                  <Checkbox id="sf-ro" checked={readOnly} onCheckedChange={(v) => setReadOnly(v === true)} />
                  <FieldLabel htmlFor="sf-ro" className="font-normal">
                    {t("sftp.readOnly")}
                  </FieldLabel>
                </Field>
              </FieldGroup>
              <FormError message={error} />
              <DialogFooter>
                <Button type="button" variant="outline" onClick={() => setOpen(false)}>
                  {t("common.cancel")}
                </Button>
                <Button type="submit" disabled={busy || !username.trim() || !rootDir.trim() || (!genPassword && !keys.trim())}>
                  {busy ? t("common.working") : t("sftp.save")}
                </Button>
              </DialogFooter>
            </form>
          )}
        </DialogContent>
      </Dialog>
    </>
  )
}

/** Edit an sftp user's keys, password, root dir or read-only flag. The username is fixed. A
 * rotated password is shown once, so this doesn't use EditDialog (it closes on save). */
function EditSFTPUser({ user, team, serverId, onSaved }: { user: SFTPUser; team: string; serverId: string; onSaved: () => Promise<void> }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [open, setOpen] = useState(false)
  const [rootDir, setRootDir] = useState("")
  const [keys, setKeys] = useState("")
  const [genPassword, setGenPassword] = useState(false)
  const [readOnly, setReadOnly] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [shownPassword, setShownPassword] = useState<string | null>(null)

  function onOpenChange(v: boolean) {
    setOpen(v)
    if (v) {
      setError(null)
      setShownPassword(null)
      setRootDir(user.root_dir)
      setKeys(user.public_keys.join("\n"))
      setGenPassword(false)
      setReadOnly(user.read_only)
    }
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const publicKeys = keys
        .split("\n")
        .map((k) => k.trim())
        .filter(Boolean)
      const r = await apiFetch<{ password?: string }>(`/api/teams/${team}/servers/${serverId}/sftp-users/${user.id}`, {
        method: "PATCH",
        body: { public_keys: publicKeys, generate_password: genPassword, root_dir: rootDir.trim(), read_only: readOnly },
      })
      await onSaved()
      if (r.password) {
        setShownPassword(r.password)
      } else {
        setOpen(false)
      }
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogTrigger render={<Button variant="outline" size="sm" />}>{t("lists.edit")}</DialogTrigger>
      <DialogContent>
        {shownPassword ? (
          <div className="flex flex-col gap-4">
            <DialogHeader>
              <DialogTitle>{user.username}</DialogTitle>
              <DialogDescription>{t("sftp.passwordShown")}</DialogDescription>
            </DialogHeader>
            <CopyField value={shownPassword} />
            <DialogFooter>
              <Button type="button" onClick={() => setOpen(false)}>
                {t("common.done")}
              </Button>
            </DialogFooter>
          </div>
        ) : (
          <form onSubmit={submit} className="flex flex-col gap-4">
            <DialogHeader>
              <DialogTitle>{user.username}</DialogTitle>
              <DialogDescription>{t("sftp.editHint")}</DialogDescription>
            </DialogHeader>
            <FieldGroup>
              <Field>
                <FieldLabel htmlFor="ef-root">{t("sftp.rootDir")}</FieldLabel>
                <Input id="ef-root" dir="ltr" required value={rootDir} onChange={(e) => setRootDir(e.target.value)} />
              </Field>
              <Field>
                <FieldLabel htmlFor="ef-keys">{t("sftp.publicKeys")}</FieldLabel>
                <Textarea id="ef-keys" dir="ltr" rows={3} value={keys} onChange={(e) => setKeys(e.target.value)} />
                <FieldDescription>{t("sftp.publicKeysHint")}</FieldDescription>
              </Field>
              <Field orientation="horizontal">
                <Checkbox id="ef-genpw" checked={genPassword} onCheckedChange={(v) => setGenPassword(v === true)} />
                <FieldLabel htmlFor="ef-genpw" className="font-normal">
                  {t("sftp.rotatePassword")}
                </FieldLabel>
              </Field>
              <Field orientation="horizontal">
                <Checkbox id="ef-ro" checked={readOnly} onCheckedChange={(v) => setReadOnly(v === true)} />
                <FieldLabel htmlFor="ef-ro" className="font-normal">
                  {t("sftp.readOnly")}
                </FieldLabel>
              </Field>
            </FieldGroup>
            <FormError message={error} />
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => setOpen(false)}>
                {t("common.cancel")}
              </Button>
              <Button type="submit" disabled={busy}>
                {busy ? t("common.working") : t("common.save")}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  )
}
