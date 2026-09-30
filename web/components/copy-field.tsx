"use client"

import { Button } from "@fadymondy/nasaq/web"
import { useState } from "react"
import { CheckIcon, CopyIcon } from "lucide-react"


import { useI18n } from "@/lib/i18n"
import { cn } from "@/lib/utils"

/** A value shown in mono, LTR, with a copy button. `block` for multi-line snippets. */
export function CopyField({ value, block = false, className }: { value: string; block?: boolean; className?: string }) {
  const { t } = useI18n()
  const [copied, setCopied] = useState(false)

  async function copy() {
    try {
      await navigator.clipboard.writeText(value)
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch {
      // Clipboard can be refused (insecure origin); the value is selectable anyway.
    }
  }

  return (
    <div className={cn("flex items-start gap-2 border bg-muted/40 p-2", className)}>
      {block ? (
        <pre dir="ltr" className="min-w-0 flex-1 overflow-x-auto text-start font-mono text-xs leading-relaxed select-all">
          {value}
        </pre>
      ) : (
        <code dir="ltr" className="min-w-0 flex-1 break-all text-start font-mono text-xs leading-7 select-all">
          {value}
        </code>
      )}
      <Button type="button" variant="secondary" size="icon-sm" onClick={copy} aria-label={t("common.copy")} title={t("common.copy")}>
        {copied ? <CheckIcon /> : <CopyIcon />}
      </Button>
      <span className="sr-only" aria-live="polite">
        {copied ? t("common.copied") : ""}
      </span>
    </div>
  )
}
