"use client"

import { useCallback, useEffect, useMemo, useState, type FormEvent } from "react"
import { KeyRoundIcon, PlusIcon, ServerCogIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { ConfirmButton } from "@/components/confirm-button"
import { FormError, useErrorText } from "@/components/form-error"
import { ErrorBand, LoadingRows, TableBand } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
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
import { useDocumentTitle, useFormatDate, useI18n } from "@/lib/i18n"
import type { Server, SSHKey, SSHKeyInstall } from "@/lib/servers"
import { useTeam } from "@/lib/team"

// A well-formed OpenSSH authorized_keys line: type, base64 body, optional comment. Client-side
// only — the server derives the real type and fingerprint and is the source of truth (422 if this
// is wrong in some way the regex missed).
const AUTHORIZED_KEY_RE = /^(ssh-ed25519|ssh-rsa|ecdsa-sha2-nistp(256|384|521))\s+[A-Za-z0-9+/]+=*(\s+.*)?$/

export default function SSHKeysPage() {
  const { t } = useI18n()
  const { team, manage } = useTeam()
  useDocumentTitle(`${t("nav.sshKeys")} — ${team.name}`)
  const fmt = useFormatDate()
  const errorText = useErrorText()
  const [keys, setKeys] = useState<SSHKey[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      const res = await apiFetch<{ keys: SSHKey[] }>(`/api/teams/${team.slug}/ssh-keys`)
      setKeys(res.keys ?? [])
    } catch (err) {
      setError(errorText(err))
      setKeys([])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [team.slug])

  useEffect(() => {
    void load()
  }, [load])

  return (
    <PageBody
      title={t("nav.sshKeys")}
      description={t("sshKeys.description")}
      actions={manage ? <AddKey onSaved={load} /> : undefined}
      bleed
    >
      <ErrorBand message={error} />
      {keys === null ? (
        <LoadingRows />
      ) : keys.length === 0 ? (
        <Empty className="border-b">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <KeyRoundIcon />
            </EmptyMedia>
            <EmptyTitle>{t("sshKeys.emptyTitle")}</EmptyTitle>
            <EmptyDescription>{t("sshKeys.emptyHint")}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <TableBand>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("sshKeys.name")}</TableHead>
                <TableHead>{t("sshKeys.type")}</TableHead>
                <TableHead>{t("sshKeys.fingerprint")}</TableHead>
                <TableHead>{t("sshKeys.created")}</TableHead>
                <TableHead className="text-end">{t("common.actions")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {keys.map((k) => (
                <TableRow key={k.id}>
                  <TableCell className="font-medium">{k.name}</TableCell>
                  <TableCell>
                    <Badge variant="outline" dir="ltr">
                      {k.key_type}
                    </Badge>
                  </TableCell>
                  <TableCell dir="ltr" className="text-start font-mono text-xs text-grid-muted">
                    {k.fingerprint}
                  </TableCell>
                  <TableCell dir="ltr" className="text-start text-sm text-grid-muted">
                    {fmt(k.created_at)}
                  </TableCell>
                  <TableCell>
                    <div className="flex justify-end gap-2">
                      <Installs sshKey={k} manage={manage} />
                      {manage ? (
                        <ConfirmButton
                          label={t("sshKeys.delete")}
                          title={t("sshKeys.deleteTitle")}
                          description={t("sshKeys.deleteHint", { name: k.name })}
                          confirmLabel={t("sshKeys.delete")}
                          onConfirm={async () => {
                            try {
                              await apiFetch(`/api/teams/${team.slug}/ssh-keys/${k.id}`, { method: "DELETE" })
                            } catch (err) {
                              throw new Error(errorText(err))
                            }
                            await load()
                          }}
                        />
                      ) : null}
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </TableBand>
      )}
    </PageBody>
  )
}

function AddKey({ onSaved }: { onSaved: () => Promise<void> }) {
  const { t } = useI18n()
  const { team } = useTeam()
  const errorText = useErrorText()
  const [open, setOpen] = useState(false)
  const [name, setName] = useState("")
  const [publicKey, setPublicKey] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const looksWellFormed = publicKey.trim() === "" || AUTHORIZED_KEY_RE.test(publicKey.trim())

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await apiFetch(`/api/teams/${team.slug}/ssh-keys`, {
        method: "POST",
        body: { name, public_key: publicKey.trim() },
      })
      setOpen(false)
      setName("")
      setPublicKey("")
      await onSaved()
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
        if (!o) setError(null)
      }}
    >
      <DialogTrigger render={<Button />}>
        <PlusIcon />
        {t("sshKeys.add")}
      </DialogTrigger>
      <DialogContent className="max-h-[90dvh] overflow-y-auto">
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{t("sshKeys.add")}</DialogTitle>
            <DialogDescription>{t("sshKeys.addHint")}</DialogDescription>
          </DialogHeader>
          <FieldGroup className="gap-4">
            <Field>
              <FieldLabel htmlFor="key-name">{t("sshKeys.name")}</FieldLabel>
              <Input id="key-name" required maxLength={80} value={name} onChange={(e) => setName(e.target.value)} />
            </Field>
            <Field>
              <FieldLabel htmlFor="key-public">{t("sshKeys.publicKey")}</FieldLabel>
              <Textarea
                id="key-public"
                dir="ltr"
                required
                rows={4}
                maxLength={8192}
                placeholder="ssh-ed25519 AAAA... you@example.com"
                className="font-mono text-xs"
                value={publicKey}
                onChange={(e) => setPublicKey(e.target.value)}
              />
              <FieldDescription>
                {looksWellFormed ? t("sshKeys.publicKeyHint") : <span className="text-grid-danger">{t("sshKeys.publicKeyFormatWarning")}</span>}
              </FieldDescription>
            </Field>
          </FieldGroup>
          <FormError message={error} />
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={busy || !name.trim() || !publicKey.trim()}>
              {busy ? t("common.working") : t("sshKeys.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

/** Servers × installed status for one key: install or uninstall on a chosen unix user. */
function Installs({ sshKey, manage }: { sshKey: SSHKey; manage: boolean }) {
  const { t } = useI18n()
  const { team } = useTeam()
  const errorText = useErrorText()
  const [open, setOpen] = useState(false)
  const [servers, setServers] = useState<Server[] | null>(null)
  const [installs, setInstalls] = useState<SSHKeyInstall[] | null>(null)
  const [users, setUsers] = useState<Record<string, string>>({})
  const [busyServer, setBusyServer] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      const [srvRes, instRes] = await Promise.all([
        apiFetch<{ servers: Server[] }>(`/api/teams/${team.slug}/servers`),
        apiFetch<{ installs: SSHKeyInstall[] }>(`/api/teams/${team.slug}/ssh-keys/${sshKey.id}/installs`),
      ])
      setServers(srvRes.servers ?? [])
      setInstalls(instRes.installs ?? [])
    } catch (err) {
      setError(errorText(err))
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [team.slug, sshKey.id])

  useEffect(() => {
    if (open) void load()
  }, [open, load])

  const installsByServerUser = useMemo(() => {
    const m = new Map<string, SSHKeyInstall>()
    for (const i of installs ?? []) m.set(`${i.server_id}:${i.unix_user}`, i)
    return m
  }, [installs])

  async function run(serverId: string, uninstall: boolean) {
    const user = (users[serverId] ?? "root").trim() || "root"
    setBusyServer(serverId)
    setError(null)
    try {
      await apiFetch(`/api/teams/${team.slug}/ssh-keys/${sshKey.id}/servers/${serverId}/${uninstall ? "uninstall" : "install"}`, {
        method: "POST",
        body: { user },
      })
      await load()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusyServer(null)
    }
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger render={<Button variant="outline" size="sm" />}>
        <ServerCogIcon />
        {t("sshKeys.installs")}
      </DialogTrigger>
      <DialogContent className="max-h-[90dvh] max-w-2xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{t("sshKeys.installsTitle", { name: sshKey.name })}</DialogTitle>
          <DialogDescription>{t("sshKeys.installsHint")}</DialogDescription>
        </DialogHeader>
        <FormError message={error} />
        {servers === null || installs === null ? (
          <LoadingRows rows={2} />
        ) : servers.length === 0 ? (
          <p className="text-sm text-grid-muted">{t("sshKeys.noServers")}</p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("sshKeys.server")}</TableHead>
                <TableHead>{t("sshKeys.user")}</TableHead>
                <TableHead>{t("sshKeys.status")}</TableHead>
                <TableHead className="text-end">{t("common.actions")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {servers.map((s) => {
                const user = users[s.id] ?? "root"
                const install = installsByServerUser.get(`${s.id}:${user}`)
                const installed = install?.status === "installed"
                return (
                  <TableRow key={s.id}>
                    <TableCell dir="ltr">{s.name}</TableCell>
                    <TableCell>
                      <Input
                        dir="ltr"
                        className="h-8 w-32 font-mono text-xs"
                        value={user}
                        disabled={!manage}
                        onChange={(e) => setUsers((u) => ({ ...u, [s.id]: e.target.value }))}
                      />
                    </TableCell>
                    <TableCell>
                      {!install ? (
                        <Badge variant="outline">{t("sshKeys.notInstalled")}</Badge>
                      ) : install.status === "installed" ? (
                        <Badge variant="outline" className="text-grid-ok">
                          {t("sshKeys.installed")}
                        </Badge>
                      ) : install.status === "failed" ? (
                        <Badge variant="outline" className="text-grid-danger" title={install.last_error}>
                          {t("sshKeys.failed")}
                        </Badge>
                      ) : (
                        <Badge variant="outline">{t("sshKeys.removed")}</Badge>
                      )}
                    </TableCell>
                    <TableCell className="text-end">
                      {manage ? (
                        <Button
                          variant="outline"
                          size="sm"
                          disabled={busyServer === s.id || !user.trim()}
                          onClick={() => void run(s.id, installed)}
                        >
                          {busyServer === s.id ? t("common.working") : installed ? t("sshKeys.uninstall") : t("sshKeys.install")}
                        </Button>
                      ) : null}
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={() => setOpen(false)}>
            {t("common.done")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
