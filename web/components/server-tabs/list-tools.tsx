"use client"

import { Button, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger, Input } from "@fadymondy/nasaq/web"
import { useState, type FormEvent, type ReactNode } from "react"
import { PencilIcon, SearchIcon } from "lucide-react"

import { useDialogOpen, type Controlled } from "@/components/dialog-state"
import { FormError, useErrorText } from "@/components/form-error"



import { useI18n } from "@/lib/i18n"

/** The search band above a server tab's list, with the shown / total count. */
export function ListSearch({
  value,
  onChange,
  placeholder,
  shown,
  total,
  children,
}: {
  value: string
  onChange: (v: string) => void
  placeholder: string
  shown: number
  total: number
  children?: ReactNode
}) {
  const { t } = useI18n()
  return (
    <div className="flex flex-wrap items-center gap-2 rounded-lg border border-border bg-card px-4 py-3">
      <div className="relative w-full max-w-xs">
        <SearchIcon className="pointer-events-none absolute start-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
        <Input type="search" value={value} onChange={(e) => onChange(e.target.value)} placeholder={placeholder} aria-label={placeholder} className="ps-8" />
      </div>
      {children}
      <span className="ms-auto text-xs text-muted-foreground tabular-nums" aria-live="polite">
        {value.trim() ? t("lists.count", { shown, total }) : t("lists.total", { total })}
      </span>
    </div>
  )
}

/** True when any of the values contains the (trimmed, lower-cased) needle. */
export function matches(q: string, ...values: (string | number | null | undefined)[]) {
  const needle = q.trim().toLowerCase()
  return !needle || values.some((v) => v != null && String(v).toLowerCase().includes(needle))
}

/** A row's Edit button and its dialog: the form's fields, Save and Cancel, and the error. */
export function EditDialog({
  title,
  description,
  onSave,
  children,
  onOpen,
  ...controlled
}: {
  title: string
  description?: string
  onSave: () => Promise<void>
  children: ReactNode
  onOpen?: () => void
} & Controlled) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const { open, setOpen, controlled: fromCaller } = useDialogOpen(controlled, () => {
    setError(null)
    onOpen?.()
  })
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await onSave()
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
      onOpenChange={setOpen}
    >
      {fromCaller ? null : (
        <DialogTrigger render={<Button variant="secondary" size="sm" />}>
          <PencilIcon />
          {t("lists.edit")}
        </DialogTrigger>
      )}
      <DialogContent>
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{title}</DialogTitle>
            {description ? <DialogDescription>{description}</DialogDescription> : null}
          </DialogHeader>
          {children}
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
