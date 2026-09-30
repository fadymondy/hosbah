"use client"

import { Badge, Button, Checkbox, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger, EmptyState, Field, FieldDescription, FieldLabel, Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, Skeleton, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useState, type FormEvent } from "react"
import { KeyRoundIcon, PlusIcon, TriangleAlertIcon } from "lucide-react"

import { ConfirmButton } from "@/components/confirm-button"
import { CopyField } from "@/components/copy-field"
import { FormError, useErrorText } from "@/components/form-error"


import { ErrorBand, LoadingRows, SectionTitle, SettingsRow, TableBand } from "@/components/states"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@fadymondy/nasaq/web"








import { apiFetch, MCP_SCOPES, type McpToken } from "@/lib/api"
import { useFormatDate, useI18n } from "@/lib/i18n"

const EXPIRY_DAYS = ["30", "90", "365"] as const

/**
 * MCP tokens — team-scoped (with scopes) or platform-admin (no scopes). The plaintext token
 * comes back once, from the create call, and is shown once.
 */
export function McpTokens({
  basePath,
  endpoint,
  withScopes,
  canCreate,
  serverName,
  bleed = false,
}: {
  basePath: string
  endpoint: string
  withScopes: boolean
  canCreate: boolean
  serverName: string
  /** Full-bleed bands (a PageBody with `bleed`) instead of cards in a padded column. */
  bleed?: boolean
}) {
  const { t } = useI18n()
  const fmt = useFormatDate()
  const errorText = useErrorText()
  const [tokens, setTokens] = useState<McpToken[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [plain, setPlain] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      const res = await apiFetch<{ tokens: McpToken[] }>(basePath)
      setTokens(res.tokens ?? [])
      setError(null)
    } catch (err) {
      setError(errorText(err))
      setTokens([])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [basePath])

  useEffect(() => {
    void load()
  }, [load])

  const snippet = JSON.stringify(
    {
      mcpServers: {
        [serverName]: {
          type: "http",
          url: endpoint,
          headers: { Authorization: `Bearer ${plain ?? (withScopes ? "hbu_…" : "hba_…")}` },
        },
      },
    },
    null,
    2,
  )

  function status(tok: McpToken) {
    if (tok.revoked_at) return <Badge variant="outline">{t("tokens.revoked")}</Badge>
    if (tok.expires_at && new Date(tok.expires_at) < new Date()) return <Badge variant="outline">{t("tokens.expired")}</Badge>
    return <Badge variant="neutral">{t("tokens.active")}</Badge>
  }

  const table =
    tokens && tokens.length ? (
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t("tokens.name")}</TableHead>
            {withScopes ? <TableHead>{t("tokens.scopes")}</TableHead> : null}
            <TableHead>{t("tokens.status")}</TableHead>
            <TableHead>{t("tokens.lastUsed")}</TableHead>
            <TableHead>{t("tokens.expires")}</TableHead>
            <TableHead className="text-end">{canCreate ? t("common.actions") : null}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {tokens.map((tok) => (
            <TableRow key={tok.id}>
              <TableCell className="font-medium">{tok.name}</TableCell>
              {withScopes ? (
                <TableCell>
                  <div className="flex flex-wrap gap-1">
                    {(tok.scopes ?? []).map((s) => (
                      <Badge key={s} variant={s === "danger" ? "danger" : "outline"} className="font-mono">
                        <span dir="ltr">{s}</span>
                      </Badge>
                    ))}
                  </div>
                </TableCell>
              ) : null}
              <TableCell>{status(tok)}</TableCell>
              <TableCell dir="ltr" className="text-start">
                {tok.last_used_at ? fmt(tok.last_used_at) : t("tokens.never")}
              </TableCell>
              <TableCell dir="ltr" className="text-start">
                {tok.expires_at ? fmt(tok.expires_at) : t("tokens.never")}
              </TableCell>
              <TableCell className="text-end">
                {canCreate && !tok.revoked_at ? (
                  <ConfirmButton
                    label={t("tokens.revoke")}
                    title={t("tokens.revokeTitle")}
                    description={t("tokens.revokeHint", { name: tok.name })}
                    confirmLabel={t("tokens.revoke")}
                    onConfirm={async () => {
                      await apiFetch(`${basePath}/${tok.id}`, { method: "DELETE" })
                      await load()
                    }}
                  />
                ) : null}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    ) : null

  const empty = (className: string) => (
    <EmptyState className={className} icon={KeyRoundIcon} title={t("tokens.emptyTitle")} description={t("tokens.emptyHint")} />
  )

  const create = canCreate ? (
    <CreateToken
      basePath={basePath}
      withScopes={withScopes}
      onCreated={(token) => {
        setPlain(token)
        void load()
      }}
    />
  ) : null

  if (bleed) {
    return (
      <>
        <SettingsRow label={t("tokens.endpoint")} hint={t("tokens.endpointHint")}>
          <CopyField value={endpoint} />
          <p className="text-sm text-muted-foreground">{t("tokens.snippetHint")}</p>
          <CopyField value={snippet} block />
        </SettingsRow>

        {plain ? (
          <div role="status" className="border-b border-border border-s-2 border-s-nq-accent bg-secondary px-6 py-5">
            <p className="text-sm font-medium text-foreground">{t("tokens.createdTitle")}</p>
            <p className="mt-1 text-xs text-muted-foreground">{t("tokens.createdOnce")}</p>
            <div className="mt-3 flex max-w-xl flex-col gap-3">
              <CopyField value={plain} />
              <div>
                <Button variant="secondary" size="sm" onClick={() => setPlain(null)}>
                  {t("tokens.dismiss")}
                </Button>
              </div>
            </div>
          </div>
        ) : null}

        <SectionTitle
          title={t("tokens.listTitle")}
          hint={!canCreate ? t("common.readOnly") : undefined}
          action={create}
        />
        <ErrorBand message={error} />

        {tokens === null ? (
          <LoadingRows />
        ) : tokens.length === 0 && !error ? (
          empty("border-b")
        ) : table ? (
          <TableBand>{table}</TableBand>
        ) : null}
      </>
    )
  }

  return (
    <div className="flex flex-col gap-6">
      <Card>
        <CardHeader>
          <CardTitle>{t("tokens.endpoint")}</CardTitle>
          <CardDescription>{t("tokens.endpointHint")}</CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          <CopyField value={endpoint} />
          <p className="text-sm text-muted-foreground">{t("tokens.snippetHint")}</p>
          <CopyField value={snippet} block />
        </CardContent>
      </Card>

      {plain ? (
        <Card className="border-nq-accent">
          <CardHeader>
            <CardTitle>{t("tokens.createdTitle")}</CardTitle>
            <CardDescription>{t("tokens.createdOnce")}</CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-3">
            <CopyField value={plain} />
            <div>
              <Button variant="secondary" size="sm" onClick={() => setPlain(null)}>
                {t("tokens.dismiss")}
              </Button>
            </div>
          </CardContent>
        </Card>
      ) : null}

      <div className="flex items-center justify-between gap-4">
        <h2 className="text-base font-medium">{t("tokens.listTitle")}</h2>
        {canCreate ? (
          <CreateToken
            basePath={basePath}
            withScopes={withScopes}
            onCreated={(token) => {
              setPlain(token)
              void load()
            }}
          />
        ) : null}
      </div>
      {!canCreate ? <p className="text-sm text-muted-foreground">{t("common.readOnly")}</p> : null}
      <FormError message={error} />

      {tokens === null ? (
        <div className="flex flex-col gap-2">
          <Skeleton className="h-8 w-full" />
          <Skeleton className="h-8 w-full" />
        </div>
      ) : tokens.length === 0 && !error ? (
        empty("border")
      ) : table ? (
        <div className="border">{table}</div>
      ) : null}
    </div>
  )
}

function CreateToken({
  basePath,
  withScopes,
  onCreated,
}: {
  basePath: string
  withScopes: boolean
  onCreated: (token: string) => void
}) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [open, setOpen] = useState(false)
  const [name, setName] = useState("")
  const [scopes, setScopes] = useState<string[]>(["read"])
  const [days, setDays] = useState<string>("90")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  function reset() {
    setName("")
    setScopes(["read"])
    setDays("90")
    setError(null)
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const body: Record<string, unknown> = { name: name.trim(), expires_in_days: Number(days) }
      if (withScopes) body.scopes = scopes
      const res = await apiFetch<{ token: string }>(basePath, { method: "POST", body })
      setOpen(false)
      reset()
      onCreated(res.token)
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  function toggle(scope: string, on: boolean) {
    setScopes((prev) => (on ? [...new Set([...prev, scope])] : prev.filter((s) => s !== scope)))
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        setOpen(o)
        if (!o) reset()
      }}
    >
      <DialogTrigger render={<Button variant="primary" size="sm" />}>
        <PlusIcon />
        {t("tokens.create")}
      </DialogTrigger>
      <DialogContent>
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{t("tokens.create")}</DialogTitle>
            <DialogDescription>{t("tokens.createHint")}</DialogDescription>
          </DialogHeader>
          <div className="flex w-full flex-col gap-5">
            <FormError message={error} />
            <Field>
              <FieldLabel htmlFor="token-name">{t("tokens.name")}</FieldLabel>
              <Input id="token-name" required maxLength={80} value={name} onChange={(e) => setName(e.target.value)} />
            </Field>
            {withScopes ? (
              <fieldset className="flex flex-col gap-4">
                <legend className="mb-3 text-label">{t("tokens.scopes")}</legend>
                <div className="flex w-full flex-col gap-3">
                  {MCP_SCOPES.map((scope) => (
                    <Field className="flex-row items-center" key={scope}>
                      <Checkbox
                        id={`scope-${scope}`}
                        checked={scopes.includes(scope)}
                        onCheckedChange={(v) => toggle(scope, v === true)}
                      />
                      <FieldLabel htmlFor={`scope-${scope}`} className="flex flex-wrap items-center gap-2 font-normal">
                        <code dir="ltr" className="font-mono text-xs">
                          {scope}
                        </code>
                        <span className="text-muted-foreground">{t(`tokens.scopeHelp.${scope.replace(":", "_")}`)}</span>
                        {scope === "destructive" ? (
                          <Badge variant="danger">
                            <TriangleAlertIcon />
                            {t("tokens.dangerous")}
                          </Badge>
                        ) : null}
                      </FieldLabel>
                    </Field>
                  ))}
                </div>
              </fieldset>
            ) : null}
            <Field>
              <FieldLabel htmlFor="token-expiry">{t("tokens.expiry")}</FieldLabel>
              <Select value={days} onValueChange={(v) => setDays(v ?? "90")}>
                <SelectTrigger id="token-expiry" className="w-full">
                  <SelectValue>{(v: string) => t("tokens.days", { n: v })}</SelectValue>
                </SelectTrigger>
                <SelectContent>
                  {EXPIRY_DAYS.map((d) => (
                    <SelectItem key={d} value={d}>
                      {t("tokens.days", { n: d })}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <FieldDescription>{t("tokens.expiryHint")}</FieldDescription>
            </Field>
          </div>
          <DialogFooter>
            <Button variant="primary" type="submit" disabled={busy || !name.trim() || (withScopes && scopes.length === 0)}>
              {busy ? t("common.working") : t("tokens.create")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
