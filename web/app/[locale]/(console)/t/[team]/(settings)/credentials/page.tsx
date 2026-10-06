"use client"

import { Badge, Button, Checkbox, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger, EmptyState, Field, FieldDescription, FieldLabel, Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useState, type FormEvent } from "react"
import { InfoIcon, LockKeyholeIcon, PlusIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { ConfirmButton } from "@/components/confirm-button"
import { CopyField } from "@/components/copy-field"
import { FormError, useErrorText } from "@/components/form-error"
import { ErrorBand, LoadingRows, TableBand } from "@/components/states"









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
      <PageBody title={t("nav.credentials")} description={t("credentials.description")} bleed>
        <div role="note" className="flex items-start gap-3 border-b border-border bg-secondary px-6 py-4 text-sm text-nq-fg-body">
          <InfoIcon className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
          <p>{t("credentials.readOnly")}</p>
        </div>
      </PageBody>
    )
  }

  return (
    <PageBody title={t("nav.credentials")} description={t("credentials.description")} actions={<AddCredential onSaved={load} />} bleed>
      <ErrorBand message={error} />
      {creds === null ? (
        <LoadingRows />
      ) : creds.length === 0 ? (
        <EmptyState className="border-b" icon={LockKeyholeIcon} title={t("credentials.emptyTitle")} description={t("credentials.emptyHint")} />
      ) : (
        <TableBand>
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
                      <Badge variant="neutral">
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
        </TableBand>
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
      <DialogTrigger render={<Button variant="secondary" size="sm" />}>{t("credentials.reveal")}</DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("credentials.revealTitle")}</DialogTitle>
          <DialogDescription>{t("credentials.revealHint")}</DialogDescription>
        </DialogHeader>
        {secret !== null ? <CopyField value={secret} /> : null}
        <FormError message={error} />
        <DialogFooter>
          <Button variant="secondary" onClick={() => setOpen(false)}>
            {secret !== null ? t("common.done") : t("common.cancel")}
          </Button>
          {secret === null ? (
            <Button variant="primary" onClick={reveal} disabled={busy}>
              {busy ? t("common.working") : t("credentials.revealConfirm")}
            </Button>
          ) : null}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

const PROVIDERS = ["proxmox", "cloudflare", "frp", "npm", "tailscale"] as const
type Provider = (typeof PROVIDERS)[number]

type CredField = { key: string; label: string; required: boolean; placeholder: string }

const FRP_FIELDS: CredField[] = [
  { key: "server_addr", label: "credentials.frpServer", required: true, placeholder: "45.129.183.99" },
  { key: "server_port", label: "credentials.frpPort", required: false, placeholder: "7000" },
  { key: "public_ip", label: "credentials.frpPublicIp", required: true, placeholder: "45.129.183.99" },
  { key: "edge_host", label: "credentials.frpEdgeHost", required: false, placeholder: "10.10.10.1" },
  { key: "port_from", label: "credentials.frpPortFrom", required: false, placeholder: "47000" },
  { key: "port_to", label: "credentials.frpPortTo", required: false, placeholder: "47999" },
]

const NPM_FIELDS: CredField[] = [
  { key: "api_url", label: "credentials.npmUrl", required: true, placeholder: "https://npm.example.com" },
  { key: "email", label: "credentials.npmEmail", required: true, placeholder: "admin@example.com" },
  { key: "certificate_id", label: "credentials.npmCert", required: false, placeholder: "2" },
]


const CLOUDFLARE_FIELDS = [
  { key: "account_id", label: "credentials.accountId", required: false, placeholder: "" },
] as const

const FIELDS = [
  { key: "api_url", label: "credentials.apiUrl", required: true, placeholder: "https://pve.example:8006" },
  { key: "token_id", label: "credentials.tokenId", required: true, placeholder: "hosbah@pve!ctl" },
  { key: "node", label: "credentials.node", required: true, placeholder: "pve-3x1" },
  { key: "storage", label: "credentials.storage", required: true, placeholder: "local" },
  { key: "bridge", label: "credentials.bridge", required: false, placeholder: "vmbr1" },
  { key: "pool", label: "credentials.pool", required: false, placeholder: "hosbah" },
  { key: "lxc_template", label: "credentials.lxcTemplate", required: false, placeholder: "local:vztmpl/debian-13-standard_13.6-1_amd64.tar.zst" },
  { key: "template_vmid", label: "credentials.templateVmid", required: false, placeholder: "9000" },
  { key: "tls_fingerprint", label: "credentials.fingerprint", required: false, placeholder: "AB:CD:…" },
] as const
const FIELDS_BY_PROVIDER: Record<string, readonly CredField[]> = {
  proxmox: FIELDS,
  cloudflare: CLOUDFLARE_FIELDS,
  frp: FRP_FIELDS,
  npm: NPM_FIELDS,
  tailscale: [],
}


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
      <DialogTrigger render={<Button variant="primary" />}>
        <PlusIcon />
        {t("credentials.add")}
      </DialogTrigger>
      <DialogContent className="max-h-[90dvh] overflow-y-auto">
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{t("credentials.add")}</DialogTitle>
            <DialogDescription>{t("credentials.addHint")}</DialogDescription>
          </DialogHeader>
          <div className="flex w-full flex-col gap-4">
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
              {provider !== "proxmox" ? <FieldDescription>{t(`credentials.hintFor.${provider}`)}</FieldDescription> : null}
            </Field>
            {(FIELDS_BY_PROVIDER[provider] ?? []).map((f) => (
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
              <FieldLabel htmlFor="cred-secret">{t(`credentials.secretFor.${provider}`)}</FieldLabel>
              <Input id="cred-secret" type="password" dir="ltr" required autoComplete="off" value={secret} onChange={(e) => setSecret(e.target.value)} />
            </Field>
            <Field className="flex-row items-center">
              <Checkbox id="cred-validate" checked={validate} onCheckedChange={(v) => setValidate(v === true)} />
              <FieldLabel htmlFor="cred-validate" className="flex flex-col items-start gap-1 font-normal">
                <span>{t(provider === "cloudflare" ? "credentials.validateCloudflare" : "credentials.validate")}</span>
                <span className="text-xs text-muted-foreground">{t("credentials.validateHint")}</span>
              </FieldLabel>
            </Field>
          </div>
          <FormError message={error} />
          <DialogFooter>
            <Button type="button" variant="secondary" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button variant="primary" type="submit" disabled={busy}>
              {busy ? t("common.working") : t("credentials.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
