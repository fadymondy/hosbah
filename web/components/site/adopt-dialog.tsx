"use client"

import { Button, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, Field, FieldDescription, FieldLabel, Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@fadymondy/nasaq/web"
import { useState, type FormEvent } from "react"

import { useDialogOpen, type Controlled } from "@/components/dialog-state"
import { FormError, useErrorText } from "@/components/form-error"





import { apiFetch } from "@/lib/api"
import { useI18n } from "@/lib/i18n"

type AdoptField = { value?: string; confidence?: "high" | "medium" | "low" | ""; source?: string }
type AdoptProposal = {
  repo_url: AdoptField
  branch: AdoptField
  build: AdoptField
  port: AdoptField
  missing: string[]
  container_ok: boolean
}

const BUILDS = ["auto", "dockerfile", "railpack"] as const
type Build = (typeof BUILDS)[number]

/**
 * "Adopt into Hosbah builds": turns an imported app into a normal, Hosbah-built git app in place —
 * no rebuild required to take it over (internal/deploy/adopt.go). Opens pre-filled with whatever
 * GET .../adopt/proposal could guess from the workload's own configuration (container.inspect, a
 * connected repo's name); a field it could not guess is simply left blank, never blocking submission.
 */
export function AdoptDialog({ path, onAdopted, ...controlled }: { path: string; onAdopted: () => Promise<void> | void } & Controlled) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [loading, setLoading] = useState(false)
  const [repoUrl, setRepoUrl] = useState("")
  const [branch, setBranch] = useState("")
  const [build, setBuild] = useState<Build>("auto")
  const [port, setPort] = useState("")
  const [fields, setFields] = useState<AdoptProposal | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const { open, setOpen } = useDialogOpen(controlled, () => {
    setError(null)
    setFields(null)
    setRepoUrl("")
    setBranch("")
    setBuild("auto")
    setPort("")
    setLoading(true)
    apiFetch<AdoptProposal>(`${path}/adopt/proposal`)
      .then((p) => {
        setFields(p)
        setRepoUrl(p.repo_url.value ?? "")
        setBranch(p.branch.value || "main")
        setBuild((BUILDS as readonly string[]).includes(p.build.value ?? "") ? (p.build.value as Build) : "auto")
        setPort(p.port.value ?? "")
      })
      .catch((err) => setError(errorText(err)))
      .finally(() => setLoading(false))
  })

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await apiFetch(`${path}/adopt`, {
        method: "POST",
        body: { repo_url: repoUrl.trim(), branch: branch.trim() || undefined, build, ...(port ? { port: Number(port) } : {}) },
      })
      setOpen(false)
      await onAdopted()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  const ok = repoUrl.trim().length > 0

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogContent>
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{t("adopt.title")}</DialogTitle>
            <DialogDescription>{t("adopt.hint")}</DialogDescription>
          </DialogHeader>
          <div className="flex w-full flex-col gap-5">
            <Field>
              <FieldLabel htmlFor="adopt-repo">{t("adopt.repoUrl")}</FieldLabel>
              <Input
                id="adopt-repo"
                dir="ltr"
                required
                disabled={loading}
                placeholder="https://github.com/owner/repo"
                value={repoUrl}
                onChange={(e) => setRepoUrl(e.target.value)}
                autoComplete="off"
              />
              <AdoptConfidence field={fields?.repo_url} />
            </Field>
            <Field>
              <FieldLabel htmlFor="adopt-branch">{t("adopt.branch")}</FieldLabel>
              <Input
                id="adopt-branch"
                dir="ltr"
                disabled={loading}
                placeholder="main"
                value={branch}
                onChange={(e) => setBranch(e.target.value)}
                autoComplete="off"
              />
              <AdoptConfidence field={fields?.branch} />
            </Field>
            <Field>
              <FieldLabel>{t("adopt.build")}</FieldLabel>
              <Select value={build} onValueChange={(v) => v && setBuild(v as Build)} disabled={loading}>
                <SelectTrigger className="w-full" aria-label={t("adopt.build")}>
                  <SelectValue>{(v: string) => t(`adopt.buildKind.${v}`, undefined, v)}</SelectValue>
                </SelectTrigger>
                <SelectContent>
                  {BUILDS.map((b) => (
                    <SelectItem key={b} value={b}>
                      {t(`adopt.buildKind.${b}`, undefined, b)}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <AdoptConfidence field={fields?.build} />
            </Field>
            <Field>
              <FieldLabel htmlFor="adopt-port">{t("adopt.port")}</FieldLabel>
              <Input
                id="adopt-port"
                type="number"
                min={1}
                max={65535}
                dir="ltr"
                disabled={loading}
                className="w-32 font-mono"
                value={port}
                onChange={(e) => setPort(e.target.value)}
              />
              <AdoptConfidence field={fields?.port} />
            </Field>
          </div>
          <FormError message={error} />
          <DialogFooter>
            <Button type="button" variant="secondary" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button variant="primary" type="submit" disabled={busy || loading || !ok}>
              {busy ? t("common.working") : t("adopt.submit")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

/** Shows what the proposal guessed for a field (confidence + source), or that it could not guess it. */
function AdoptConfidence({ field }: { field?: AdoptField }) {
  const { t } = useI18n()
  if (!field) return null
  if (!field.value) return <FieldDescription>{t("adopt.fieldMissing")}</FieldDescription>
  if (!field.confidence) return null
  return <FieldDescription>{t(`adopt.confidence.${field.confidence}`, undefined, field.confidence)}</FieldDescription>
}
