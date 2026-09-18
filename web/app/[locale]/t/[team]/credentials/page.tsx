"use client"

import { useCallback, useEffect, useState, type FormEvent } from "react"
import { InfoIcon, LockKeyholeIcon, PlusIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { ConfirmButton } from "@/components/confirm-button"
import { CopyField } from "@/components/copy-field"
import { FormError, useErrorText } from "@/components/form-error"
import { Alert, AlertDescription } from "@/components/ui/alert"
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
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { apiFetch } from "@/lib/api"
import { useDocumentTitle, useFormatDate, useI18n } from "@/lib/i18n"
import type { Credential } from "@/lib/servers"
import { useTeam } from "@/lib/team"

export default function CredentialsPage() {
  const { t } = useI18n()
  const { team, manage } = useTeam()
  useDocumentTitle(`${t("nav.credentials")} — ${team.name}`)
  const fmt = useFormatDate()
  const errorText = useErrorText()
  const [creds, setCreds] = useState<Credential[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      const res = await apiFetch<{ credentials: Credential[] }>(`/api/teams/${team.slug}/credentials`)
      setCreds(res.credentials ?? [])
    } catch (err) {
      setError(errorText(err))
      setCreds([])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [team.slug])

  useEffect(() => {
    if (manage) void load()
  }, [load, manage])

  if (!manage) {
    return (
      <PageBody title={t("nav.credentials")} description={t("credentials.description")}>
        <Alert>
          <InfoIcon />
          <AlertDescription>{t("credentials.readOnly")}</AlertDescription>
        </Alert>
      </PageBody>
    )
  }

  return (
    <PageBody title={t("nav.credentials")} description={t("credentials.description")} actions={<AddCredential onSaved={load} />}>
      <FormError message={error} />
      {creds === null ? (
        <Skeleton className="h-16 w-full" />
      ) : creds.length === 0 ? (
        <Empty className="border">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <LockKeyholeIcon />
            </EmptyMedia>
            <EmptyTitle>{t("credentials.emptyTitle")}</EmptyTitle>
            <EmptyDescription>{t("credentials.emptyHint")}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <div className="border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("credentials.name")}</TableHead>
                <TableHead>{t("credentials.apiUrl")}</TableHead>
                <TableHead>{t("credentials.node")}</TableHead>
                <TableHead>{t("credentials.tokenId")}</TableHead>
                <TableHead>{t("credentials.validated")}</TableHead>
                <TableHead className="text-end">{t("common.actions")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {creds.map((c) => (
                <TableRow key={c.id}>
                  <TableCell>
                    <span className="font-medium">{c.name}</span>
                    <div className="text-xs text-muted-foreground">{t(`servers.providers.${c.provider}`)}</div>
                  </TableCell>
                  <TableCell dir="ltr" className="text-start font-mono text-xs">
                    {c.config.api_url ?? "—"}
                  </TableCell>
                  <TableCell dir="ltr" className="text-start font-mono text-xs">
                    {c.config.node ?? "—"}
                  </TableCell>
                  <TableCell dir="ltr" className="text-start font-mono text-xs">
                    {c.config.token_id ?? "—"}
                  </TableCell>
                  <TableCell>
                    {c.last_validated_at ? (
                      <Badge variant="secondary">
                        {t("credentials.validated")} · <span dir="ltr">{fmt(c.last_validated_at)}</span>
                      </Badge>
                    ) : (
                      <Badge variant="outline">{t("credentials.notValidated")}</Badge>
                    )}
                  </TableCell>
                  <TableCell>
                    <div className="flex justify-end gap-2">
                      <Reveal team={team.slug} cred={c} />
                      <ConfirmButton
                        label={t("credentials.delete")}
                        title={t("credentials.deleteTitle")}
                        description={t("credentials.deleteHint", { name: c.name })}
                        confirmLabel={t("credentials.delete")}
                        onConfirm={async () => {
                          try {
                            await apiFetch(`/api/teams/${team.slug}/credentials/${c.id}`, { method: "DELETE" })
                          } catch (err) {
                            throw new Error(errorText(err))
                          }
                          await load()
                        }}
                      />
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </PageBody>
  )
}

/** Reveal is audited: a confirmation first, then the secret once, in the dialog. */
function Reveal({ team, cred }: { team: string; cred: Credential }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [open, setOpen] = useState(false)
  const [secret, setSecret] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function reveal() {
    setBusy(true)
    setError(null)
    try {
      const r = await apiFetch<{ secret: string }>(`/api/teams/${team}/credentials/${cred.id}/reveal`, { method: "POST" })
      setSecret(r.secret)
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
        if (!o) setSecret(null)
      }}
    >
      <DialogTrigger render={<Button variant="outline" size="sm" />}>{t("credentials.reveal")}</DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("credentials.revealTitle")}</DialogTitle>
          <DialogDescription>{t("credentials.revealHint")}</DialogDescription>
        </DialogHeader>
        {secret !== null ? <CopyField value={secret} /> : null}
        <FormError message={error} />
        <DialogFooter>
          <Button variant="outline" onClick={() => setOpen(false)}>
            {secret !== null ? t("common.done") : t("common.cancel")}
          </Button>
          {secret === null ? (
            <Button onClick={reveal} disabled={busy}>
              {busy ? t("common.working") : t("credentials.revealConfirm")}
            </Button>
          ) : null}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

const PROVIDERS = ["proxmox", "cloudflare"] as const
type Provider = (typeof PROVIDERS)[number]

const CLOUDFLARE_FIELDS = [
  { key: "account_id", label: "credentials.accountId", required: false, placeholder: "" },
] as const

const FIELDS = [
  { key: "api_url", label: "credentials.apiUrl", required: true, placeholder: "https://pve.example:8006" },
  { key: "token_id", label: "credentials.tokenId", required: true, placeholder: "cloudy@pve!ctl" },
  { key: "node", label: "credentials.node", required: true, placeholder: "pve-3x1" },
  { key: "storage", label: "credentials.storage", required: true, placeholder: "local" },
  { key: "bridge", label: "credentials.bridge", required: false, placeholder: "vmbr1" },
  { key: "pool", label: "credentials.pool", required: false, placeholder: "cloudy" },
  { key: "lxc_template", label: "credentials.lxcTemplate", required: false, placeholder: "local:vztmpl/debian-13-standard_13.6-1_amd64.tar.zst" },
  { key: "template_vmid", label: "credentials.templateVmid", required: false, placeholder: "9000" },
  { key: "tls_fingerprint", label: "credentials.fingerprint", required: false, placeholder: "AB:CD:…" },
] as const

function AddCredential({ onSaved }: { onSaved: () => Promise<void> }) {
  const { t } = useI18n()
  const { team } = useTeam()
  const errorText = useErrorText()
  const [open, setOpen] = useState(false)
  const [provider, setProvider] = useState<Provider>("proxmox")
  const [name, setName] = useState("")
  const [secret, setSecret] = useState("")
  const [config, setConfig] = useState<Record<string, string>>({})
  const [validate, setValidate] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await apiFetch(`/api/teams/${team.slug}/credentials`, {
        method: "POST",
        body: { provider, name, config, secret, skip_validation: !validate },
      })
      setOpen(false)
      setName("")
      setSecret("")
      setConfig({})
      await onSaved()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger render={<Button size="sm" />}>
        <PlusIcon />
        {t("credentials.add")}
      </DialogTrigger>
      <DialogContent className="max-h-[90dvh] overflow-y-auto">
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{t("credentials.add")}</DialogTitle>
            <DialogDescription>{t("credentials.addHint")}</DialogDescription>
          </DialogHeader>
          <FieldGroup className="gap-4">
            <Field>
              <FieldLabel htmlFor="cred-name">{t("credentials.name")}</FieldLabel>
              <Input id="cred-name" required maxLength={80} value={name} onChange={(e) => setName(e.target.value)} />
            </Field>
            <Field>
              <FieldLabel htmlFor="cred-provider">{t("credentials.provider")}</FieldLabel>
              <Select
                value={provider}
                onValueChange={(v) => {
                  if (!v) return
                  setProvider(v as Provider)
                  setConfig({})
                }}
              >
                <SelectTrigger id="cred-provider" className="w-full">
                  <SelectValue>{(v: string) => t(`servers.providers.${v}`)}</SelectValue>
                </SelectTrigger>
                <SelectContent>
                  {PROVIDERS.map((p) => (
                    <SelectItem key={p} value={p}>
                      {t(`servers.providers.${p}`)}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              {provider === "cloudflare" ? <FieldDescription>{t("credentials.cloudflareHint")}</FieldDescription> : null}
            </Field>
            {(provider === "proxmox" ? FIELDS : CLOUDFLARE_FIELDS).map((f) => (
              <Field key={f.key}>
                <FieldLabel htmlFor={`cred-${f.key}`}>
                  {t(f.label)}
                  {f.required ? null : <span className="font-normal text-muted-foreground"> ({t("credentials.optional")})</span>}
                </FieldLabel>
                <Input
                  id={`cred-${f.key}`}
                  dir="ltr"
                  required={f.required}
                  placeholder={f.placeholder}
                  value={config[f.key] ?? ""}
                  onChange={(e) => setConfig((c) => ({ ...c, [f.key]: e.target.value }))}
                />
                {f.key === "tls_fingerprint" ? <FieldDescription>{t("credentials.fingerprintHint")}</FieldDescription> : null}
              </Field>
            ))}
            <Field>
              <FieldLabel htmlFor="cred-secret">{t(provider === "cloudflare" ? "credentials.apiToken" : "credentials.secret")}</FieldLabel>
              <Input id="cred-secret" type="password" dir="ltr" required autoComplete="off" value={secret} onChange={(e) => setSecret(e.target.value)} />
            </Field>
            <Field orientation="horizontal">
              <Checkbox id="cred-validate" checked={validate} onCheckedChange={(v) => setValidate(v === true)} />
              <FieldLabel htmlFor="cred-validate" className="flex flex-col items-start gap-1 font-normal">
                <span>{t(provider === "cloudflare" ? "credentials.validateCloudflare" : "credentials.validate")}</span>
                <span className="text-xs text-muted-foreground">{t("credentials.validateHint")}</span>
              </FieldLabel>
            </Field>
          </FieldGroup>
          <FormError message={error} />
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={busy}>
              {busy ? t("common.working") : t("credentials.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
