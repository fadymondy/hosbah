"use client"

import { Badge, Button, Checkbox, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger, Field, FieldDescription, FieldLabel, Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, Table, TableBody, TableCell, TableHead, TableHeader, TableRow, toast, SettingsSection } from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useState, type FormEvent } from "react"
import { PlusIcon, RefreshCwIcon, Trash2Icon } from "lucide-react"

import { ConfirmButton } from "@/components/confirm-button"
import { CopyField } from "@/components/copy-field"
import { useDialogOpen, type Controlled } from "@/components/dialog-state"
import { FormError, useErrorText } from "@/components/form-error"
import { ErrorBand, LoadingRows, TableBand } from "@/components/states"








import { apiFetch } from "@/lib/api"
import { useI18n } from "@/lib/i18n"
import type {
  Fail2banBannedResult,
  Fail2banInstallResult,
  Fail2banSettings,
  FirewallApplyResult,
  FirewallRule,
  FirewallSettings,
  FirewallStatusResult,
} from "@/lib/servers"
import { useTeam } from "@/lib/team"

const ACTIONS = ["allow", "deny"] as const
const PROTOS = ["tcp", "udp", "any"] as const
type RuleForm = { action: (typeof ACTIONS)[number]; proto: (typeof PROTOS)[number]; port_start: string; port_end: string; source_cidr: string; comment: string; enabled: boolean }
const EMPTY_RULE: RuleForm = { action: "allow", proto: "tcp", port_start: "", port_end: "", source_cidr: "", comment: "", enabled: true }

/** MH-580 J1/J2: this server's nftables firewall (rules stay pending until Apply loads them) and
 * its fail2ban sshd jail. Owners and admins manage both; other roles see them read-only. */
export function ServerSecurity({ team, serverId }: { team: string; serverId: string }) {
  return (
    <>
      <AccessSection team={team} serverId={serverId} />
      <FirewallSection team={team} serverId={serverId} />
      <Fail2banSection team={team} serverId={serverId} />
    </>
  )
}

type DeployKey = { public_key?: string; fingerprint?: string; created_at?: string }

/** MH-578 C9/C11: resetting the server's root password (chpasswd, sealed end to end, shown once)
 * and its outbound git deploy key (generated on the server; add it to a GitHub repo through the
 * team's App installation, or copy the public half by hand). Owner/admin only on the server. */
function AccessSection({ team, serverId }: { team: string; serverId: string }) {
  const { t } = useI18n()
  const { manage } = useTeam()
  const errorText = useErrorText()
  const base = `/api/teams/${team}/servers/${serverId}`

  const [key, setKey] = useState<DeployKey | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [genBusy, setGenBusy] = useState(false)

  const load = useCallback(async () => {
    try {
      const k = await apiFetch<DeployKey>(`${base}/deploy-key`)
      setKey(k)
      setError(null)
    } catch (err) {
      setError(errorText(err))
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [base])

  useEffect(() => {
    void load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [base])

  async function generate() {
    setGenBusy(true)
    setError(null)
    try {
      const k = await apiFetch<DeployKey>(`${base}/deploy-key/generate`, { method: "POST" })
      setKey(k)
      toast.success(t("servers.security.access.deployKeyGenerated"))
    } catch (err) {
      setError(errorText(err))
    } finally {
      setGenBusy(false)
    }
  }

  return (
    <SettingsSection title={t("servers.security.access.title")} description={t("servers.security.access.hint")}>
      <ErrorBand message={error} />
      {!manage ? <p className="pb-3 text-sm text-muted-foreground">{t("servers.security.readOnly")}</p> : null}

      <div className="border-t border-border first:border-t-0 py-4">
        <h3 className="mb-2 text-sm font-medium text-foreground">{t("servers.security.access.rootPassword")}</h3>
        <p className="mb-3 text-sm text-muted-foreground">{t("servers.security.access.rootPasswordHint")}</p>
        {manage ? <ResetPasswordDialog base={base} /> : null}
      </div>

      <div className="border-t border-border py-4">
        <h3 className="mb-2 text-sm font-medium text-foreground">{t("servers.security.access.deployKey")}</h3>
        <p className="mb-3 text-sm text-muted-foreground">{t("servers.security.access.deployKeyHint")}</p>
        {key === null ? (
          <LoadingRows rows={1} />
        ) : !key.public_key ? (
          <div className="flex flex-wrap items-center gap-2">
            <p className="text-sm text-muted-foreground">{t("servers.security.access.deployKeyEmpty")}</p>
            {manage ? (
              <Button size="sm" variant="secondary" disabled={genBusy} onClick={() => void generate()}>
                {genBusy ? t("common.working") : t("servers.security.access.deployKeyGenerate")}
              </Button>
            ) : null}
          </div>
        ) : (
          <div className="flex flex-col gap-3">
            <CopyField value={key.public_key} block />
            <p dir="ltr" className="text-xs text-muted-foreground">
              {key.fingerprint}
            </p>
            {manage ? (
              <div className="flex flex-wrap gap-2">
                <Button size="sm" variant="secondary" disabled={genBusy} onClick={() => void generate()}>
                  {genBusy ? t("common.working") : t("servers.security.access.deployKeyRegenerate")}
                </Button>
                <AddToGitHubDialog base={base} onAdded={load} />
              </div>
            ) : null}
          </div>
        )}
      </div>
    </SettingsSection>
  )
}

/** Reset the server's root password. Optionally caller-supplied (min 12 chars), otherwise
 * generated. The new password is shown exactly once, from this response — never read back. */
function ResetPasswordDialog({ base }: { base: string }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [open, setOpen] = useState(false)
  const [custom, setCustom] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [result, setResult] = useState<{ user: string; password: string } | null>(null)

  function reset() {
    setCustom("")
    setError(null)
    setResult(null)
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    if (custom && custom.length < 12) {
      setError(t("servers.security.access.passwordTooShort"))
      return
    }
    setBusy(true)
    setError(null)
    try {
      const r = await apiFetch<{ user: string; password: string }>(`${base}/root-password/reset`, {
        method: "POST",
        body: custom ? { password: custom } : {},
      })
      setResult(r)
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
        reset()
      }}
    >
      <DialogTrigger render={<Button size="sm" variant="danger" />}>{t("servers.security.access.resetPassword")}</DialogTrigger>
      <DialogContent>
        {result ? (
          <>
            <DialogHeader>
              <DialogTitle>{t("servers.security.access.resetPasswordDone")}</DialogTitle>
              <DialogDescription>{t("servers.security.access.resetPasswordDoneHint")}</DialogDescription>
            </DialogHeader>
            <CopyField value={result.password} />
            <DialogFooter>
              <Button variant="primary" onClick={() => setOpen(false)}>{t("common.done")}</Button>
            </DialogFooter>
          </>
        ) : (
          <form onSubmit={submit} className="flex flex-col gap-4">
            <DialogHeader>
              <DialogTitle>{t("servers.security.access.resetPasswordTitle")}</DialogTitle>
              <DialogDescription>{t("servers.security.access.resetPasswordHint")}</DialogDescription>
            </DialogHeader>
            <Field>
              <FieldLabel htmlFor="rp-custom">{t("servers.security.access.customPassword")}</FieldLabel>
              <Input
                id="rp-custom"
                dir="ltr"
                type="password"
                minLength={12}
                placeholder={t("servers.security.access.customPasswordPlaceholder")}
                value={custom}
                onChange={(e) => setCustom(e.target.value)}
              />
              <FieldDescription>{t("servers.security.access.customPasswordHint")}</FieldDescription>
            </Field>
            <FormError message={error} />
            <DialogFooter>
              <Button type="button" variant="secondary" onClick={() => setOpen(false)}>
                {t("common.cancel")}
              </Button>
              <Button type="submit" variant="danger" disabled={busy}>
                {busy ? t("common.working") : t("servers.security.access.resetPasswordConfirm")}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  )
}

/** Add the server's deploy key to a GitHub repo through the team's App installation. */
function AddToGitHubDialog({ base, onAdded }: { base: string; onAdded: () => Promise<void> }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [open, setOpen] = useState(false)
  const [repo, setRepo] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await apiFetch(`${base}/deploy-key/github-add`, { method: "POST", body: { repo: repo.trim() } })
      toast.success(t("servers.security.access.githubAdded", { repo: repo.trim() }))
      setOpen(false)
      setRepo("")
      await onAdded()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger render={<Button size="sm" variant="secondary" />}>{t("servers.security.access.addToGithub")}</DialogTrigger>
      <DialogContent>
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{t("servers.security.access.addToGithub")}</DialogTitle>
            <DialogDescription>{t("servers.security.access.addToGithubHint")}</DialogDescription>
          </DialogHeader>
          <Field>
            <FieldLabel htmlFor="gh-repo">{t("servers.security.access.repo")}</FieldLabel>
            <Input id="gh-repo" dir="ltr" required placeholder="owner/name" value={repo} onChange={(e) => setRepo(e.target.value)} />
          </Field>
          <FormError message={error} />
          <DialogFooter>
            <Button type="button" variant="secondary" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button variant="primary" type="submit" disabled={busy || !repo.trim()}>
              {busy ? t("common.working") : t("servers.security.access.addToGithubConfirm")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function FirewallSection({ team, serverId }: { team: string; serverId: string }) {
  const { t } = useI18n()
  const { manage } = useTeam()
  const errorText = useErrorText()
  const base = `/api/teams/${team}/servers/${serverId}/firewall`

  const [rules, setRules] = useState<FirewallRule[] | null>(null)
  const [settings, setSettings] = useState<FirewallSettings | null>(null)
  const [status, setStatus] = useState<FirewallStatusResult | null>(null)
  const [statusError, setStatusError] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [policyDraft, setPolicyDraft] = useState<"accept" | "drop">("accept")
  const [sshPortDraft, setSshPortDraft] = useState("22")
  const [savingSettings, setSavingSettings] = useState(false)
  const [confirmDrop, setConfirmDrop] = useState(false)
  const [applyOpen, setApplyOpen] = useState(false)

  const load = useCallback(async () => {
    try {
      const [r, s] = await Promise.all([apiFetch<{ rules: FirewallRule[] }>(base), apiFetch<FirewallSettings>(`${base}/settings`)])
      setRules(r.rules ?? [])
      setSettings(s)
      setPolicyDraft(s.default_policy)
      setSshPortDraft(String(s.ssh_port))
      setError(null)
    } catch (err) {
      setError(errorText(err))
      setRules((x) => x ?? [])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [base])

  const loadStatus = useCallback(async () => {
    try {
      const r = await apiFetch<FirewallStatusResult>(`${base}/status`)
      setStatus(r)
      setStatusError(null)
    } catch (err) {
      setStatus(null)
      setStatusError(errorText(err))
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [base])

  useEffect(() => {
    void load()
    void loadStatus()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [base])

  async function saveSettings(confirm?: string) {
    setSavingSettings(true)
    setError(null)
    try {
      const s = await apiFetch<FirewallSettings>(`${base}/settings`, {
        method: "PUT",
        body: { default_policy: policyDraft, ssh_port: Number(sshPortDraft) || 22, confirm },
      })
      setSettings(s)
      toast.success(t("servers.security.firewall.saved"))
    } catch (err) {
      setError(errorText(err))
    } finally {
      setSavingSettings(false)
    }
  }

  function onPolicyChange(v: string) {
    if (v === "drop") {
      setConfirmDrop(true)
      return
    }
    setPolicyDraft("accept")
  }

  async function deleteRule(rule: FirewallRule) {
    await apiFetch(`${base}/${rule.id}`, { method: "DELETE" })
    toast.success(t("servers.security.firewall.ruleDeleted"))
    await load()
  }

  async function addPreset(name: string, sourceCidr?: string) {
    try {
      await apiFetch(`${base}/presets/${name}`, { method: "POST", body: { source_cidr: sourceCidr ?? "" } })
      toast.success(t("servers.security.firewall.presetAdded"))
      await load()
    } catch (err) {
      toast.error(errorText(err))
    }
  }

  const statusBadge = status ? (
    <Badge variant={status.active ? "brand" : "neutral"}>
      {status.active ? t("servers.security.firewall.statusLive") : t("servers.security.firewall.statusInactive")}
    </Badge>
  ) : statusError ? (
    <Badge variant="outline">{t("servers.security.firewall.statusUnknown")}</Badge>
  ) : null

  return (
    <SettingsSection
      className="mt-4"
      title={t("servers.security.firewall.title")}
      description={t("servers.security.firewall.hint")}
      action={
        <div className="flex flex-wrap items-center gap-2">
        {statusBadge}
        <Button size="sm" variant="secondary" onClick={() => void loadStatus()}>
          <RefreshCwIcon />
          {t("common.refresh")}
        </Button>
        </div>
      }
    >
      <ErrorBand message={error} />
      {!manage ? <p className="pb-3 text-sm text-muted-foreground">{t("servers.security.readOnly")}</p> : null}

      <div className="grid gap-4 border-t border-border first:border-t-0 py-4 sm:grid-cols-3">
        <Field>
          <FieldLabel htmlFor="fw-policy">{t("servers.security.firewall.defaultPolicy")}</FieldLabel>
          <Select value={policyDraft} onValueChange={(v) => manage && onPolicyChange(v as string)} disabled={!manage}>
            <SelectTrigger id="fw-policy" className="w-full">
              <SelectValue>{(v: string) => t(`servers.security.firewall.policy${v === "drop" ? "Drop" : "Accept"}`)}</SelectValue>
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="accept">{t("servers.security.firewall.policyAccept")}</SelectItem>
              <SelectItem value="drop">{t("servers.security.firewall.policyDrop")}</SelectItem>
            </SelectContent>
          </Select>
        </Field>
        <Field>
          <FieldLabel htmlFor="fw-ssh">{t("servers.security.firewall.sshPort")}</FieldLabel>
          <Input
            id="fw-ssh"
            dir="ltr"
            type="number"
            min={1}
            max={65535}
            disabled={!manage}
            value={sshPortDraft}
            onChange={(e) => setSshPortDraft(e.target.value)}
          />
        </Field>
        {manage ? (
          <div className="flex items-end">
            <Button variant="primary" size="sm" disabled={savingSettings || !settings} onClick={() => void saveSettings(policyDraft === "drop" ? "drop" : undefined)}>
              {savingSettings ? t("common.working") : t("servers.security.firewall.save")}
            </Button>
          </div>
        ) : null}
      </div>

      <Dialog open={confirmDrop} onOpenChange={setConfirmDrop}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("servers.security.firewall.dropConfirmTitle")}</DialogTitle>
            <DialogDescription>{t("servers.security.firewall.dropConfirmBody")}</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              variant="secondary"
              onClick={() => {
                setConfirmDrop(false)
              }}
            >
              {t("common.cancel")}
            </Button>
            <Button
              variant="danger"
              onClick={() => {
                setPolicyDraft("drop")
                setConfirmDrop(false)
              }}
            >
              {t("servers.security.firewall.dropConfirmAction")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {rules === null ? (
        <LoadingRows rows={3} />
      ) : (
        <>
          <div className="flex flex-wrap items-center gap-2 border-t border-border first:border-t-0 py-3">
            <h3 className="me-auto text-sm font-medium text-foreground">{t("servers.security.firewall.rules")}</h3>
            {manage ? (
              <>
                <Button size="sm" variant="secondary" onClick={() => void addPreset("web")}>
                  {t("servers.security.firewall.presetWeb")}
                </Button>
                <Button size="sm" variant="secondary" onClick={() => void addPreset("ssh")}>
                  {t("servers.security.firewall.presetSsh")}
                </Button>
                <PostgresPresetDialog onAdd={(cidr) => void addPreset("postgres-from-cidr", cidr)} />
                <RuleDialog base={base} onSaved={load} />
                <Button variant="primary" size="sm" onClick={() => setApplyOpen(true)}>
                  {t("servers.security.firewall.apply")}
                </Button>
              </>
            ) : null}
          </div>

          {rules.length === 0 ? (
            <p className="border-t border-border first:border-t-0 py-6 text-sm text-muted-foreground">{t("servers.security.firewall.empty")}</p>
          ) : (
            <TableBand>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t("servers.security.firewall.action")}</TableHead>
                    <TableHead>{t("servers.security.firewall.proto")}</TableHead>
                    <TableHead>{t("servers.security.firewall.portStart")}/{t("servers.security.firewall.portEnd")}</TableHead>
                    <TableHead>{t("servers.security.firewall.source")}</TableHead>
                    <TableHead>{t("servers.security.firewall.comment")}</TableHead>
                    {manage ? <TableHead className="text-end">{t("common.actions")}</TableHead> : null}
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {rules.map((r) => (
                    <TableRow key={r.id}>
                      <TableCell>
                        <Badge variant={r.action === "allow" ? "neutral" : "danger"}>
                          {t(`servers.security.firewall.actions.${r.action}`)}
                        </Badge>
                        {!r.enabled ? <span className="ms-1 text-xs text-muted-foreground">({t("servers.security.firewall.disabled")})</span> : null}
                      </TableCell>
                      <TableCell dir="ltr" className="text-start">
                        {t(`servers.security.firewall.protos.${r.proto}`)}
                      </TableCell>
                      <TableCell dir="ltr" className="text-start font-mono text-xs">
                        {r.port_start === r.port_end ? r.port_start : `${r.port_start}-${r.port_end}`}
                      </TableCell>
                      <TableCell dir="ltr" className="text-start font-mono text-xs">
                        {r.source_cidr}
                      </TableCell>
                      <TableCell dir="auto" className="max-w-xs truncate text-sm text-muted-foreground">
                        {r.comment || "—"}
                      </TableCell>
                      {manage ? (
                        <TableCell>
                          <div className="flex flex-wrap justify-end gap-2">
                            <RuleDialog base={base} rule={r} onSaved={load} />
                            <ConfirmButton
                              icon={<Trash2Icon />}
                              label={t("servers.security.firewall.delete")}
                              title={t("servers.security.firewall.deleteTitle")}
                              description={t("servers.security.firewall.deleteHint")}
                              confirmLabel={t("servers.security.firewall.delete")}
                              onConfirm={async () => {
                                try {
                                  await deleteRule(r)
                                } catch (err) {
                                  throw new Error(errorText(err))
                                }
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
          )}
        </>
      )}

      {applyOpen && settings ? (
        <ApplyDialog base={base} settings={settings} rules={rules ?? []} onClose={() => setApplyOpen(false)} onApplied={async () => { await load(); await loadStatus() }} />
      ) : null}
    </SettingsSection>
  )
}

/** Add or edit one rule; not applied to the server until Apply runs. */
function RuleDialog({ base, rule, onSaved, ...controlled }: { base: string; rule?: FirewallRule; onSaved: () => Promise<void> } & Controlled) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [form, setForm] = useState<RuleForm>(EMPTY_RULE)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const { open, setOpen, controlled: fromCaller } = useDialogOpen(controlled, reset)

  function reset() {
    setError(null)
    setForm(
      rule
        ? {
            action: rule.action,
            proto: rule.proto,
            port_start: String(rule.port_start),
            port_end: String(rule.port_end),
            source_cidr: rule.source_cidr,
            comment: rule.comment,
            enabled: rule.enabled,
          }
        : EMPTY_RULE,
    )
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    const portStart = Number(form.port_start)
    const portEnd = Number(form.port_end) || portStart
    if (!portStart || portStart < 1 || portStart > 65535 || portEnd < portStart || portEnd > 65535) {
      setError(t("servers.security.firewall.portRangeInvalid"))
      return
    }
    setBusy(true)
    setError(null)
    try {
      const body = {
        action: form.action,
        proto: form.proto,
        port_start: portStart,
        port_end: portEnd,
        source_cidr: form.source_cidr.trim(),
        comment: form.comment.trim(),
        enabled: form.enabled,
      }
      await apiFetch(rule ? `${base}/${rule.id}` : base, { method: rule ? "PATCH" : "POST", body })
      setOpen(false)
      toast.success(rule ? t("servers.security.firewall.ruleUpdated") : t("servers.security.firewall.ruleCreated"))
      await onSaved()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      {fromCaller ? null : (
        <DialogTrigger render={<Button size={rule ? "sm" : "md"} variant={rule ? "secondary" : "primary"} />}>
          <PlusIcon />
          {rule ? t("lists.edit") : t("servers.security.firewall.add")}
        </DialogTrigger>
      )}
      <DialogContent className="max-h-[90dvh] overflow-y-auto">
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{rule ? t("lists.edit") : t("servers.security.firewall.addTitle")}</DialogTitle>
            <DialogDescription>{t("servers.security.firewall.addHint")}</DialogDescription>
          </DialogHeader>
          <div className="flex w-full flex-col gap-4">
            <div className="grid gap-4 sm:grid-cols-2">
              <Field>
                <FieldLabel htmlFor="rule-action">{t("servers.security.firewall.action")}</FieldLabel>
                <Select value={form.action} onValueChange={(v) => v && setForm((f) => ({ ...f, action: v as RuleForm["action"] }))}>
                  <SelectTrigger id="rule-action">
                    <SelectValue>{(v: string) => t(`servers.security.firewall.actions.${v}`)}</SelectValue>
                  </SelectTrigger>
                  <SelectContent>
                    {ACTIONS.map((a) => (
                      <SelectItem key={a} value={a}>
                        {t(`servers.security.firewall.actions.${a}`)}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
              <Field>
                <FieldLabel htmlFor="rule-proto">{t("servers.security.firewall.proto")}</FieldLabel>
                <Select value={form.proto} onValueChange={(v) => v && setForm((f) => ({ ...f, proto: v as RuleForm["proto"] }))}>
                  <SelectTrigger id="rule-proto">
                    <SelectValue>{(v: string) => t(`servers.security.firewall.protos.${v}`)}</SelectValue>
                  </SelectTrigger>
                  <SelectContent>
                    {PROTOS.map((p) => (
                      <SelectItem key={p} value={p}>
                        {t(`servers.security.firewall.protos.${p}`)}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
            </div>
            <div className="grid gap-4 sm:grid-cols-2">
              <Field>
                <FieldLabel htmlFor="rule-ps">{t("servers.security.firewall.portStart")}</FieldLabel>
                <Input
                  id="rule-ps"
                  dir="ltr"
                  type="number"
                  min={1}
                  max={65535}
                  required
                  value={form.port_start}
                  onChange={(e) => setForm((f) => ({ ...f, port_start: e.target.value }))}
                />
              </Field>
              <Field>
                <FieldLabel htmlFor="rule-pe">{t("servers.security.firewall.portEnd")}</FieldLabel>
                <Input
                  id="rule-pe"
                  dir="ltr"
                  type="number"
                  min={1}
                  max={65535}
                  placeholder={form.port_start}
                  value={form.port_end}
                  onChange={(e) => setForm((f) => ({ ...f, port_end: e.target.value }))}
                />
              </Field>
            </div>
            <Field>
              <FieldLabel htmlFor="rule-src">{t("servers.security.firewall.source")}</FieldLabel>
              <Input
                id="rule-src"
                dir="ltr"
                placeholder="any"
                maxLength={64}
                value={form.source_cidr}
                onChange={(e) => setForm((f) => ({ ...f, source_cidr: e.target.value }))}
              />
              <FieldDescription>{t("servers.security.firewall.sourceHint")}</FieldDescription>
            </Field>
            <Field>
              <FieldLabel htmlFor="rule-comment">{t("servers.security.firewall.comment")}</FieldLabel>
              <Input
                id="rule-comment"
                dir="auto"
                maxLength={200}
                value={form.comment}
                onChange={(e) => setForm((f) => ({ ...f, comment: e.target.value }))}
              />
            </Field>
            <Field className="flex-row items-center">
              <Checkbox id="rule-enabled" checked={form.enabled} onCheckedChange={(v) => setForm((f) => ({ ...f, enabled: v === true }))} />
              <FieldLabel htmlFor="rule-enabled" className="font-normal">
                {t("servers.security.firewall.enabled")}
              </FieldLabel>
            </Field>
          </div>
          <FormError message={error} />
          <DialogFooter>
            <Button type="button" variant="secondary" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button variant="primary" type="submit" disabled={busy}>
              {busy ? t("common.working") : t("common.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function PostgresPresetDialog({ onAdd }: { onAdd: (cidr: string) => void }) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const [cidr, setCidr] = useState("")

  function submit(e: FormEvent) {
    e.preventDefault()
    onAdd(cidr.trim())
    setOpen(false)
    setCidr("")
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger render={<Button size="sm" variant="secondary" />}>{t("servers.security.firewall.presetPostgres")}</DialogTrigger>
      <DialogContent>
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{t("servers.security.firewall.presetPostgresTitle")}</DialogTitle>
            <DialogDescription>{t("servers.security.firewall.presetPostgresHint")}</DialogDescription>
          </DialogHeader>
          <Field>
            <FieldLabel htmlFor="pg-cidr">{t("servers.security.firewall.presetCidr")}</FieldLabel>
            <Input id="pg-cidr" dir="ltr" required placeholder="203.0.113.0/24" value={cidr} onChange={(e) => setCidr(e.target.value)} />
          </Field>
          <DialogFooter>
            <Button type="button" variant="secondary" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button variant="primary" type="submit" disabled={!cidr.trim()}>
              {t("common.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

/** A preview of what Apply would render (from the saved rules and settings) next to what is
 * currently live on the server, before actually loading anything. */
function ApplyDialog({
  base,
  settings,
  rules,
  onClose,
  onApplied,
}: {
  base: string
  settings: FirewallSettings
  rules: FirewallRule[]
  onClose: () => void
  onApplied: () => Promise<void>
}) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [keepWebPorts, setKeepWebPorts] = useState(true)
  const [live, setLive] = useState<FirewallStatusResult | null | undefined>(undefined)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const enabled = rules.filter((r) => r.enabled)

  useEffect(() => {
    apiFetch<FirewallStatusResult>(`${base}/status`)
      .then(setLive)
      .catch(() => setLive(null))
  }, [base])

  async function apply() {
    setBusy(true)
    setError(null)
    try {
      await apiFetch<FirewallApplyResult>(`${base}/apply`, { method: "POST", body: { keep_web_ports: keepWebPorts } })
      toast.success(t("servers.security.firewall.applied"))
      onClose()
      await onApplied()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="max-h-[90dvh] max-w-2xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{t("servers.security.firewall.applyTitle")}</DialogTitle>
          <DialogDescription>{t("servers.security.firewall.applyHint")}</DialogDescription>
        </DialogHeader>
        <div className="grid gap-4 sm:grid-cols-2">
          <div>
            <h4 className="mb-2 text-sm font-medium text-foreground">{t("servers.security.firewall.pending")}</h4>
            <div className="border border-border p-3 text-xs">
              <p dir="ltr" className="mb-2 font-mono">
                {t(`servers.security.firewall.policy${settings.default_policy === "drop" ? "Drop" : "Accept"}`)} · ssh {settings.ssh_port}
              </p>
              {enabled.length === 0 ? (
                <p className="text-muted-foreground">{t("servers.security.firewall.empty")}</p>
              ) : (
                <ul dir="ltr" className="space-y-1 font-mono">
                  {enabled.map((r) => (
                    <li key={r.id}>
                      {r.action} {r.proto} {r.port_start === r.port_end ? r.port_start : `${r.port_start}-${r.port_end}`} from {r.source_cidr}
                    </li>
                  ))}
                </ul>
              )}
            </div>
          </div>
          <div>
            <h4 className="mb-2 text-sm font-medium text-foreground">{t("servers.security.firewall.live")}</h4>
            <div className="border border-border p-3 text-xs">
              {live === undefined ? (
                <p className="text-muted-foreground">{t("common.loading")}</p>
              ) : live === null || !live.active ? (
                <p className="text-muted-foreground">{t("servers.security.firewall.notAppliedYet")}</p>
              ) : (
                <pre dir="ltr" className="max-h-48 overflow-auto font-mono whitespace-pre-wrap">
                  {live.ruleset}
                </pre>
              )}
            </div>
          </div>
        </div>
        <Field className="flex-row items-center">
          <Checkbox id="apply-keep-web" checked={keepWebPorts} onCheckedChange={(v) => setKeepWebPorts(v === true)} />
          <FieldLabel htmlFor="apply-keep-web" className="font-normal">
            {t("servers.security.firewall.keepWebPorts")}
          </FieldLabel>
        </Field>
        <FormError message={error} />
        <DialogFooter>
          <Button variant="secondary" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button variant="primary" onClick={() => void apply()} disabled={busy}>
            {busy ? t("common.working") : t("servers.security.firewall.applyConfirm")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function Fail2banSection({ team, serverId }: { team: string; serverId: string }) {
  const { t } = useI18n()
  const { manage } = useTeam()
  const errorText = useErrorText()
  const base = `/api/teams/${team}/servers/${serverId}/fail2ban`

  const [settings, setSettings] = useState<Fail2banSettings | null>(null)
  const [banned, setBanned] = useState<Fail2banBannedResult | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [enabledDraft, setEnabledDraft] = useState(false)
  const [bantime, setBantime] = useState("600")
  const [findtime, setFindtime] = useState("600")
  const [maxretry, setMaxretry] = useState("5")
  const [busy, setBusy] = useState(false)
  const [unbanning, setUnbanning] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      const s = await apiFetch<Fail2banSettings>(`${base}/settings`)
      setSettings(s)
      setEnabledDraft(s.enabled)
      setBantime(String(s.bantime))
      setFindtime(String(s.findtime))
      setMaxretry(String(s.maxretry))
      setError(null)
    } catch (err) {
      setError(errorText(err))
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [base])

  const loadBanned = useCallback(async () => {
    try {
      const r = await apiFetch<Fail2banBannedResult>(`${base}/banned`)
      setBanned(r)
    } catch (err) {
      setBanned(null)
      setError(errorText(err))
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [base])

  useEffect(() => {
    void load()
    void loadBanned()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [base])

  async function save(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await apiFetch<Fail2banInstallResult>(`${base}/settings`, {
        method: "POST",
        body: { enabled: enabledDraft, bantime: Number(bantime) || 600, findtime: Number(findtime) || 600, maxretry: Number(maxretry) || 5 },
      })
      toast.success(t("servers.security.fail2ban.saved"))
      await load()
      await loadBanned()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  async function unban(ip: string) {
    setUnbanning(ip)
    setError(null)
    try {
      await apiFetch(`${base}/unban`, { method: "POST", body: { ip } })
      toast.success(t("servers.security.fail2ban.unbanned", { ip }))
      await loadBanned()
    } catch (err) {
      toast.error(errorText(err))
    } finally {
      setUnbanning(null)
    }
  }

  return (
    <SettingsSection
      className="mt-4"
      title={t("servers.security.fail2ban.title")}
      description={t("servers.security.fail2ban.hint")}
      action={
        <Button size="sm" variant="secondary" onClick={() => void loadBanned()}>
        <RefreshCwIcon />
        {t("servers.security.fail2ban.refresh")}
        </Button>
      }
    >
      <ErrorBand message={error} />
      {!manage ? <p className="pb-3 text-sm text-muted-foreground">{t("servers.security.readOnly")}</p> : null}

      {settings === null ? (
        <LoadingRows rows={2} />
      ) : (
        <form onSubmit={save} className="grid gap-4 border-t border-border first:border-t-0 py-4 sm:grid-cols-4 sm:items-end">
          <Field className="flex-row items-center">
            <Checkbox id="f2b-enabled" disabled={!manage} checked={enabledDraft} onCheckedChange={(v) => setEnabledDraft(v === true)} />
            <FieldLabel htmlFor="f2b-enabled" className="font-normal">
              {t("servers.security.fail2ban.enabled")}
            </FieldLabel>
          </Field>
          <Field>
            <FieldLabel htmlFor="f2b-bantime">{t("servers.security.fail2ban.bantime")}</FieldLabel>
            <Input id="f2b-bantime" dir="ltr" type="number" min={30} max={31536000} disabled={!manage} value={bantime} onChange={(e) => setBantime(e.target.value)} />
          </Field>
          <Field>
            <FieldLabel htmlFor="f2b-findtime">{t("servers.security.fail2ban.findtime")}</FieldLabel>
            <Input id="f2b-findtime" dir="ltr" type="number" min={10} max={31536000} disabled={!manage} value={findtime} onChange={(e) => setFindtime(e.target.value)} />
          </Field>
          <Field>
            <FieldLabel htmlFor="f2b-maxretry">{t("servers.security.fail2ban.maxretry")}</FieldLabel>
            <Input id="f2b-maxretry" dir="ltr" type="number" min={1} max={100} disabled={!manage} value={maxretry} onChange={(e) => setMaxretry(e.target.value)} />
          </Field>
          {manage ? (
            <div className="sm:col-span-4">
              <p className="mb-2 text-caption text-muted-foreground">{t("servers.security.fail2ban.installHint")}</p>
              <Button variant="primary" type="submit" size="sm" disabled={busy}>
                {busy ? t("common.working") : t("servers.security.fail2ban.save")}
              </Button>
            </div>
          ) : null}
        </form>
      )}

      <div className="border-t border-border first:border-t-0 py-4">
        <h3 className="mb-3 text-sm font-medium text-foreground">{t("servers.security.fail2ban.banned")}</h3>
        {banned === null ? (
          <LoadingRows rows={2} />
        ) : !banned.active ? (
          <p className="text-sm text-muted-foreground">{t("servers.security.fail2ban.jailInactive")}</p>
        ) : banned.banned.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t("servers.security.fail2ban.bannedEmpty")}</p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("servers.security.fail2ban.ip")}</TableHead>
                {manage ? <TableHead className="text-end">{t("common.actions")}</TableHead> : null}
              </TableRow>
            </TableHeader>
            <TableBody>
              {banned.banned.map((ip) => (
                <TableRow key={ip}>
                  <TableCell dir="ltr" className="font-mono text-sm">
                    {ip}
                  </TableCell>
                  {manage ? (
                    <TableCell>
                      <div className="flex justify-end">
                        <Button size="sm" variant="secondary" disabled={unbanning === ip} onClick={() => void unban(ip)}>
                          {unbanning === ip ? t("common.working") : t("servers.security.fail2ban.unban")}
                        </Button>
                      </div>
                    </TableCell>
                  ) : null}
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </div>
    </SettingsSection>
  )
}
