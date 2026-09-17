"use client"

import { useCallback, useEffect, useState, type FormEvent } from "react"
import { EyeIcon, InfoIcon, PencilIcon } from "lucide-react"

import { ConfirmButton } from "@/components/confirm-button"
import { FormError, useErrorText } from "@/components/form-error"
import { Alert, AlertDescription } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Textarea } from "@/components/ui/textarea"
import { apiFetch } from "@/lib/api"
import { ENV_NAME, type EnvVar } from "@/lib/apps"
import { useFormatDate, useI18n } from "@/lib/i18n"

/** The app's environment: names only, values write-only (owners/admins can reveal one, audited). */
export function EnvPanel({ path, manage }: { path: string; manage: boolean }) {
  const { t } = useI18n()
  const fmt = useFormatDate()
  const errorText = useErrorText()
  const [vars, setVars] = useState<EnvVar[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [name, setName] = useState("")
  const [value, setValue] = useState("")
  const [busy, setBusy] = useState(false)
  const [revealed, setRevealed] = useState<Record<string, string>>({})

  const load = useCallback(async () => {
    try {
      const r = await apiFetch<{ variables: EnvVar[] }>(`${path}/env`)
      setVars(r.variables ?? [])
    } catch (err) {
      setError(errorText(err))
      setVars((v) => v ?? [])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path])

  useEffect(() => {
    void load()
  }, [load])

  const nameOK = ENV_NAME.test(name)

  async function save(e: FormEvent) {
    e.preventDefault()
    if (!nameOK) return
    setBusy(true)
    setError(null)
    try {
      await apiFetch(`${path}/env/${encodeURIComponent(name)}`, { method: "PUT", body: { value } })
      setRevealed(({ [name]: _, ...rest }) => rest)
      setName("")
      setValue("")
      await load()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  async function reveal(n: string) {
    setError(null)
    try {
      const r = await apiFetch<{ name: string; value: string }>(`${path}/env/${encodeURIComponent(n)}/reveal`, {
        method: "POST",
      })
      setRevealed((cur) => ({ ...cur, [n]: r.value }))
    } catch (err) {
      setError(errorText(err))
    }
  }

  return (
    <div className="flex flex-col gap-4" data-testid="env">
      <Alert>
        <InfoIcon />
        <AlertDescription>{t("apps.env.applies")}</AlertDescription>
      </Alert>
      <FormError message={error} />
      {vars === null ? (
        <Skeleton className="h-16 w-full" />
      ) : vars.length === 0 ? (
        <p className="border p-4 text-sm text-muted-foreground">{t("apps.env.empty")}</p>
      ) : (
        <div className="border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("apps.env.name")}</TableHead>
                <TableHead>{t("apps.env.value")}</TableHead>
                <TableHead>{t("apps.env.updated")}</TableHead>
                {manage ? <TableHead className="text-end">{t("common.actions")}</TableHead> : null}
              </TableRow>
            </TableHeader>
            <TableBody>
              {vars.map((v) => (
                <TableRow key={v.name} data-var={v.name}>
                  <TableCell dir="ltr" className="text-start font-mono text-xs">
                    {v.name}
                  </TableCell>
                  <TableCell dir="ltr" className="max-w-80 text-start font-mono text-xs break-all whitespace-pre-wrap">
                    {v.name in revealed ? revealed[v.name] : "••••••••"}
                  </TableCell>
                  <TableCell dir="ltr" className="text-start text-xs">
                    {fmt(v.updated_at)}
                  </TableCell>
                  {manage ? (
                    <TableCell>
                      <div className="flex justify-end gap-2">
                        {!(v.name in revealed) ? (
                          <Button size="sm" variant="outline" onClick={() => reveal(v.name)} title={t("apps.env.revealHint")}>
                            <EyeIcon />
                            {t("apps.env.reveal")}
                          </Button>
                        ) : null}
                        <Button
                          size="sm"
                          variant="outline"
                          onClick={() => {
                            setName(v.name)
                            setValue("")
                          }}
                        >
                          <PencilIcon />
                          {t("apps.env.edit")}
                        </Button>
                        <ConfirmButton
                          label={t("apps.env.remove")}
                          title={t("apps.env.removeTitle", { name: v.name })}
                          description={t("apps.env.removeHint")}
                          confirmLabel={t("apps.env.remove")}
                          onConfirm={async () => {
                            try {
                              await apiFetch(`${path}/env/${encodeURIComponent(v.name)}`, { method: "DELETE" })
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
        </div>
      )}
      {manage ? (
        <>
          <p className="text-xs text-muted-foreground">{t("apps.env.revealHint")}</p>
          <form onSubmit={save} className="flex flex-col gap-4 border p-4">
            <h3 className="font-medium">{t("apps.env.setTitle")}</h3>
            <FieldGroup>
              <Field>
                <FieldLabel htmlFor="env-name">{t("apps.env.name")}</FieldLabel>
                <Input
                  id="env-name"
                  dir="ltr"
                  required
                  maxLength={128}
                  value={name}
                  placeholder="DATABASE_URL"
                  onChange={(e) => setName(e.target.value)}
                  aria-invalid={name !== "" && !nameOK}
                  autoComplete="off"
                />
                <FieldDescription>{t("apps.env.nameHint")}</FieldDescription>
              </Field>
              <Field>
                <FieldLabel htmlFor="env-value">{t("apps.env.value")}</FieldLabel>
                <Textarea
                  id="env-value"
                  dir="ltr"
                  rows={3}
                  value={value}
                  maxLength={32768}
                  onChange={(e) => setValue(e.target.value)}
                  className="font-mono text-xs"
                  autoComplete="off"
                />
                {vars?.some((v) => v.name === name) ? (
                  <FieldDescription>{t("apps.env.overwrite")}</FieldDescription>
                ) : null}
              </Field>
              <div>
                <Button type="submit" disabled={busy || !nameOK}>
                  {busy ? t("common.working") : t("common.save")}
                </Button>
              </div>
            </FieldGroup>
          </form>
        </>
      ) : (
        <p className="text-sm text-muted-foreground">{t("common.readOnly")}</p>
      )}
    </div>
  )
}
