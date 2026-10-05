"use client"

import { Badge, Button, Checkbox, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, EmptyState, Field, FieldDescription, FieldLabel, Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, Textarea, useConfirm } from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useState, type FormEvent } from "react"
import { KeyRoundIcon, PlusIcon, KeySquareIcon, ShieldCheckIcon, ShieldOffIcon, SquareTerminalIcon, Trash2Icon } from "lucide-react"

import { CopyField } from "@/components/copy-field"
import { FormError, useErrorText } from "@/components/form-error"
import { NasaqTable } from "@/components/lists/nasaq-table"
import { useLoadedServer } from "@/components/server-tabs/context"
import { ErrorBand, LoadingRows, SectionTitle } from "@/components/states"

import { apiFetch } from "@/lib/api"
import { useFormatDate, useI18n } from "@/lib/i18n"
import type { ServerSSHKey, SSHUser } from "@/lib/servers"
import { useTeam } from "@/lib/team"

type Access = { users: SSHUser[]; keys: ServerSSHKey[] }

/** Who can reach the server over SSH: shell logins Hosbah created there, and the library keys
 * installed for them (or for root). A password is shown once, when it is set. */
export function ServerSSH({ serverId }: { serverId: string }) {
  const { t } = useI18n()
  const fmt = useFormatDate()
  const { team, manage } = useTeam()
  const { server } = useLoadedServer()
  const errorText = useErrorText()
  const confirm = useConfirm()
  const [access, setAccess] = useState<Access | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [adding, setAdding] = useState(false)
  const [addingKey, setAddingKey] = useState(false)
  const [resetting, setResetting] = useState<SSHUser | null>(null)
  const host = server.ip_address ?? server.hostname ?? ""
  const base = `/api/teams/${team.slug}/servers/${serverId}`
  const enrolled = server.status === "enrolled"

  const load = useCallback(async () => {
    try {
      const r = await apiFetch<Access>(`${base}/ssh-access`)
      setAccess({ users: r.users ?? [], keys: r.keys ?? [] })
      setError(null)
    } catch (err) {
      setError(errorText(err))
      setAccess((a) => a ?? { users: [], keys: [] })
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [base])

  useEffect(() => {
    void load()
  }, [load])

  async function setSudo(u: SSHUser, sudo: boolean) {
    const ok = await confirm({
      title: sudo ? t("servers.ssh.grantSudo") : t("servers.ssh.revokeSudo"),
      description: t(sudo ? "servers.ssh.grantSudoHint" : "servers.ssh.revokeSudoHint", { user: u.username }),
      confirmLabel: sudo ? t("servers.ssh.grantSudo") : t("servers.ssh.revokeSudo"),
    })
    if (!ok) return
    try {
      await apiFetch(`${base}/ssh-users/${encodeURIComponent(u.username)}`, { method: "PATCH", body: { sudo } })
      await load()
    } catch (err) {
      setError(errorText(err))
    }
  }

  async function removeUser(u: SSHUser) {
    const ok = await confirm({
      title: t("servers.ssh.removeUser"),
      description: t("servers.ssh.removeUserHint", { user: u.username, server: server.name }),
      confirmLabel: t("servers.ssh.removeUser"),
    })
    if (!ok) return
    try {
      await apiFetch(`${base}/ssh-users/${encodeURIComponent(u.username)}`, { method: "DELETE" })
      await load()
    } catch (err) {
      setError(errorText(err))
    }
  }

  async function uninstallKey(k: ServerSSHKey) {
    const ok = await confirm({
      title: t("servers.ssh.removeKey"),
      description: t("servers.ssh.removeKeyHint", { key: k.name, user: k.unix_user }),
      confirmLabel: t("servers.ssh.removeKey"),
    })
    if (!ok) return
    try {
      await apiFetch(`/api/teams/${team.slug}/ssh-keys/${k.key_id}/servers/${serverId}/uninstall`, { method: "POST", body: { user: k.unix_user } })
      await load()
    } catch (err) {
      setError(errorText(err))
    }
  }

  const canManage = manage && enrolled
  const firstUser = access?.users[0]?.username

  return (
    <section aria-labelledby="tab-h" data-stack>
      <SectionTitle
        id="tab-h"
        title={t("servers.ssh.title")}
        hint={t("servers.ssh.hint")}
        action={
          canManage ? (
            <>
              <Button variant="secondary" onClick={() => setAddingKey(true)}>
                <KeyRoundIcon />
                {t("servers.ssh.addKey")}
              </Button>
              <Button variant="primary" onClick={() => setAdding(true)}>
                <PlusIcon />
                {t("servers.ssh.addUser")}
              </Button>
            </>
          ) : null
        }
      />
      <ErrorBand message={error} />
      {host ? (
        <div className="rounded-lg border border-border bg-card px-5 py-4 shadow-xs">
          <p className="mb-2 text-sm text-muted-foreground">{t("servers.ssh.connect")}</p>
          <CopyField value={`ssh ${firstUser ?? "root"}@${host}`} />
          <p className="mt-2 text-xs text-muted-foreground">{t("servers.ssh.connectHint")}</p>
        </div>
      ) : null}

      <SectionTitle title={t("servers.ssh.users")} hint={t("servers.ssh.usersHint")} />
      {access === null ? (
        <LoadingRows />
      ) : access.users.length === 0 ? (
        <EmptyState className="nq-empty" icon={SquareTerminalIcon} title={t("servers.ssh.noUsers")} description={t("servers.ssh.noUsersHint")} />
      ) : (
        <NasaqTable
          data={access.users}
          rowId={(u) => u.username}
          rowLabel={(u) => u.username}
          label={t("servers.ssh.users")}
          rowActions={
            canManage
              ? (u) => [
                  u.sudo
                    ? { id: "unsudo", label: t("servers.ssh.revokeSudo"), icon: ShieldOffIcon, onSelect: () => void setSudo(u, false) }
                    : { id: "sudo", label: t("servers.ssh.grantSudo"), icon: ShieldCheckIcon, onSelect: () => void setSudo(u, true) },
                  { id: "password", label: t("servers.ssh.resetPassword"), icon: KeySquareIcon, onSelect: () => setResetting(u) },
                  { id: "remove", label: t("servers.ssh.removeUser"), icon: Trash2Icon, danger: true, group: "danger", onSelect: () => void removeUser(u) },
                ]
              : undefined
          }
          columns={[
            {
              id: "username",
              header: t("servers.ssh.username"),
              sortValue: (u) => u.username,
              cell: (u) => (
                <span className="block min-w-0">
                  <span dir="ltr" className="font-medium">
                    {u.username}
                  </span>
                  {u.status === "failed" && u.last_error ? <span className="mt-1 block text-caption text-nq-danger-text">{u.last_error}</span> : null}
                </span>
              ),
            },
            {
              id: "role",
              header: t("servers.ssh.role"),
              sortValue: (u) => (u.sudo ? 1 : 0),
              cell: (u) => <Badge variant={u.sudo ? "brand" : "outline"}>{u.sudo ? t("servers.ssh.admin") : t("servers.ssh.regular")}</Badge>,
            },
            {
              id: "status",
              header: t("lists.status"),
              sortValue: (u) => u.status,
              cell: (u) => <Badge variant={u.status === "ready" ? "success" : "danger"}>{t(`servers.ssh.status.${u.status}`)}</Badge>,
            },
            {
              id: "created",
              header: t("servers.ssh.created"),
              sortValue: (u) => u.created_at,
              cell: (u) => (
                <span dir="ltr" className="text-xs">
                  {fmt(u.created_at)}
                </span>
              ),
            },
          ]}
        />
      )}

      <SectionTitle title={t("servers.ssh.keys")} hint={t("servers.ssh.keysHint")} />
      {access === null ? (
        <LoadingRows />
      ) : access.keys.length === 0 ? (
        <EmptyState className="nq-empty" icon={KeyRoundIcon} title={t("servers.ssh.noKeys")} description={t("servers.ssh.noKeysHint")} />
      ) : (
        <NasaqTable
          data={access.keys}
          rowId={(k) => `${k.key_id}:${k.unix_user}`}
          rowLabel={(k) => k.name}
          label={t("servers.ssh.keys")}
          rowActions={
            canManage ? (k) => [{ id: "remove", label: t("servers.ssh.removeKey"), icon: Trash2Icon, danger: true, group: "danger", onSelect: () => void uninstallKey(k) }] : undefined
          }
          columns={[
            {
              id: "name",
              header: t("servers.ssh.keyName"),
              sortValue: (k) => k.name,
              searchValue: (k) => `${k.name} ${k.fingerprint} ${k.unix_user}`,
              cell: (k) => (
                <span className="block min-w-0">
                  <span dir="auto" className="font-medium">
                    {k.name}
                  </span>
                  <span dir="ltr" className="mt-1 block truncate text-start font-mono text-caption text-muted-foreground">
                    {k.key_type} {k.fingerprint}
                  </span>
                  {k.status === "failed" && k.last_error ? <span className="mt-1 block text-caption text-nq-danger-text">{k.last_error}</span> : null}
                </span>
              ),
            },
            {
              id: "user",
              header: t("servers.ssh.forUser"),
              sortValue: (k) => k.unix_user,
              cell: (k) => (
                <span dir="ltr" className="font-mono text-xs">
                  {k.unix_user}
                </span>
              ),
            },
            {
              id: "status",
              header: t("lists.status"),
              sortValue: (k) => k.status,
              cell: (k) => <Badge variant={k.status === "installed" ? "success" : "danger"}>{t(`servers.ssh.keyStatus.${k.status}`)}</Badge>,
            },
          ]}
        />
      )}

      {adding ? <AddUser base={base} onClose={() => setAdding(false)} onSaved={load} /> : null}
      {addingKey ? <AddKey base={base} users={access?.users ?? []} onClose={() => setAddingKey(false)} onSaved={load} /> : null}
      {resetting ? <ResetPassword base={base} user={resetting} onClose={() => setResetting(null)} /> : null}
    </section>
  )
}

/** The password the server just got, shown this once: Hosbah keeps no copy. */
function PasswordOnce({ title, password, onDone }: { title: string; password: string; onDone: () => void }) {
  const { t } = useI18n()
  return (
    <div className="flex flex-col gap-4">
      <DialogHeader>
        <DialogTitle>{title}</DialogTitle>
        <DialogDescription>{t("servers.ssh.passwordShown")}</DialogDescription>
      </DialogHeader>
      <CopyField value={password} />
      <DialogFooter>
        <Button variant="primary" type="button" onClick={onDone}>
          {t("common.done")}
        </Button>
      </DialogFooter>
    </div>
  )
}

/** A password: typed (12+ characters), generated by Hosbah, or none (key only). */
function PasswordFields({
  idp,
  password,
  setPassword,
  generate,
  setGenerate,
}: {
  idp: string
  password: string
  setPassword: (v: string) => void
  generate: boolean
  setGenerate: (v: boolean) => void
}) {
  const { t } = useI18n()
  return (
    <>
      <Field className="flex-row items-center">
        <Checkbox id={`${idp}-gen`} checked={generate} onCheckedChange={(v) => setGenerate(v === true)} />
        <FieldLabel htmlFor={`${idp}-gen`} className="font-normal">
          {t("servers.ssh.generatePassword")}
        </FieldLabel>
      </Field>
      {generate ? null : (
        <Field>
          <FieldLabel htmlFor={`${idp}-pw`}>{t("servers.ssh.password")}</FieldLabel>
          <Input
            id={`${idp}-pw`}
            dir="ltr"
            type="password"
            minLength={12}
            maxLength={128}
            autoComplete="new-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
          <FieldDescription>{t("servers.ssh.passwordHint")}</FieldDescription>
        </Field>
      )}
    </>
  )
}

function AddUser({ base, onClose, onSaved }: { base: string; onClose: () => void; onSaved: () => Promise<void> }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [username, setUsername] = useState("")
  const [sudo, setSudo] = useState(false)
  const [publicKey, setPublicKey] = useState("")
  const [password, setPassword] = useState("")
  const [generate, setGenerate] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [shown, setShown] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const r = await apiFetch<{ password?: string }>(`${base}/ssh-users`, {
        method: "POST",
        body: {
          username: username.trim(),
          sudo,
          public_key: publicKey.trim() || undefined,
          password: generate ? undefined : password || undefined,
          generate_password: generate || undefined,
        },
      })
      await onSaved()
      if (r.password) setShown(r.password)
      else onClose()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  const ready = username.trim() !== "" && (publicKey.trim() !== "" || generate || password.length >= 12)
  return (
    <Dialog open onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="max-h-[90dvh] overflow-y-auto">
        {shown ? (
          <PasswordOnce title={username} password={shown} onDone={onClose} />
        ) : (
          <form onSubmit={submit} className="flex flex-col gap-4">
            <DialogHeader>
              <DialogTitle>{t("servers.ssh.addUser")}</DialogTitle>
              <DialogDescription>{t("servers.ssh.addUserHint")}</DialogDescription>
            </DialogHeader>
            <div className="flex w-full flex-col gap-4">
              <Field>
                <FieldLabel htmlFor="su-user">{t("servers.ssh.username")}</FieldLabel>
                <Input
                  id="su-user"
                  dir="ltr"
                  required
                  maxLength={32}
                  pattern="[a-z_][a-z0-9_\-]*"
                  placeholder="deploy"
                  autoComplete="off"
                  value={username}
                  onChange={(e) => setUsername(e.target.value.toLowerCase())}
                />
                <FieldDescription>{t("servers.ssh.usernameHint")}</FieldDescription>
              </Field>
              <Field className="flex-row items-center">
                <Checkbox id="su-sudo" checked={sudo} onCheckedChange={(v) => setSudo(v === true)} />
                <FieldLabel htmlFor="su-sudo" className="font-normal">
                  {t("servers.ssh.sudo")}
                </FieldLabel>
              </Field>
              <Field>
                <FieldLabel htmlFor="su-key">{t("servers.ssh.publicKey")}</FieldLabel>
                <Textarea id="su-key" dir="ltr" rows={3} placeholder="ssh-ed25519 AAAA... me@laptop" value={publicKey} onChange={(e) => setPublicKey(e.target.value)} />
                <FieldDescription>{t("servers.ssh.publicKeyHint")}</FieldDescription>
              </Field>
              <PasswordFields idp="su" password={password} setPassword={setPassword} generate={generate} setGenerate={setGenerate} />
            </div>
            <FormError message={error} />
            <DialogFooter>
              <Button type="button" variant="secondary" onClick={onClose}>
                {t("common.cancel")}
              </Button>
              <Button variant="primary" type="submit" disabled={busy || !ready}>
                {busy ? t("common.working") : t("servers.ssh.create")}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  )
}

function AddKey({ base, users, onClose, onSaved }: { base: string; users: SSHUser[]; onClose: () => void; onSaved: () => Promise<void> }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [name, setName] = useState("")
  const [publicKey, setPublicKey] = useState("")
  const [user, setUser] = useState(users[0]?.username ?? "root")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await apiFetch(`${base}/ssh-keys`, { method: "POST", body: { name: name.trim() || undefined, public_key: publicKey.trim(), user } })
      await onSaved()
      onClose()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="max-h-[90dvh] overflow-y-auto">
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{t("servers.ssh.addKey")}</DialogTitle>
            <DialogDescription>{t("servers.ssh.addKeyHint")}</DialogDescription>
          </DialogHeader>
          <div className="flex w-full flex-col gap-4">
            <Field>
              <FieldLabel htmlFor="sk-key">{t("servers.ssh.publicKey")}</FieldLabel>
              <Textarea id="sk-key" dir="ltr" rows={3} required placeholder="ssh-ed25519 AAAA... me@laptop" value={publicKey} onChange={(e) => setPublicKey(e.target.value)} />
            </Field>
            <Field>
              <FieldLabel htmlFor="sk-name">{t("servers.ssh.keyName")}</FieldLabel>
              <Input id="sk-name" dir="auto" maxLength={80} placeholder={t("servers.ssh.keyNamePlaceholder")} value={name} onChange={(e) => setName(e.target.value)} />
            </Field>
            <Field>
              <FieldLabel htmlFor="sk-user">{t("servers.ssh.forUser")}</FieldLabel>
              <Select value={user} onValueChange={(v) => setUser(String(v ?? "root"))}>
                <SelectTrigger id="sk-user" dir="ltr" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {users.map((u) => (
                    <SelectItem key={u.username} value={u.username}>
                      {u.username}
                    </SelectItem>
                  ))}
                  <SelectItem value="root">root</SelectItem>
                </SelectContent>
              </Select>
              <FieldDescription>{t("servers.ssh.forUserHint")}</FieldDescription>
            </Field>
          </div>
          <FormError message={error} />
          <DialogFooter>
            <Button type="button" variant="secondary" onClick={onClose}>
              {t("common.cancel")}
            </Button>
            <Button variant="primary" type="submit" disabled={busy || !publicKey.trim()}>
              {busy ? t("common.working") : t("servers.ssh.install")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function ResetPassword({ base, user, onClose }: { base: string; user: SSHUser; onClose: () => void }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [password, setPassword] = useState("")
  const [generate, setGenerate] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [shown, setShown] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const r = await apiFetch<{ password?: string }>(`${base}/ssh-users/${encodeURIComponent(user.username)}`, {
        method: "PATCH",
        body: { sudo: user.sudo, password: generate ? undefined : password, generate_password: generate || undefined },
      })
      if (r.password) setShown(r.password)
      else onClose()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open onOpenChange={(v) => !v && onClose()}>
      <DialogContent>
        {shown ? (
          <PasswordOnce title={user.username} password={shown} onDone={onClose} />
        ) : (
          <form onSubmit={submit} className="flex flex-col gap-4">
            <DialogHeader>
              <DialogTitle>{t("servers.ssh.resetPassword")}</DialogTitle>
              <DialogDescription>{t("servers.ssh.resetPasswordHint", { user: user.username })}</DialogDescription>
            </DialogHeader>
            <div className="flex w-full flex-col gap-4">
              <PasswordFields idp="sr" password={password} setPassword={setPassword} generate={generate} setGenerate={setGenerate} />
            </div>
            <FormError message={error} />
            <DialogFooter>
              <Button type="button" variant="secondary" onClick={onClose}>
                {t("common.cancel")}
              </Button>
              <Button variant="primary" type="submit" disabled={busy || (!generate && password.length < 12)}>
                {busy ? t("common.working") : t("common.save")}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  )
}
