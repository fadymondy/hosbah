import { Input } from "@fadymondy/nasaq/web"
"use client"



/** A 6-digit code field: numeric keyboard, one-time-code autofill, always left-to-right. */
export function CodeInput({
  id = "code",
  value,
  onChange,
  autoFocus = false,
}: {
  id?: string
  value: string
  onChange: (v: string) => void
  autoFocus?: boolean
}) {
  return (
    <Input
      id={id}
      inputMode="numeric"
      autoComplete="one-time-code"
      pattern="[0-9]{6}"
      maxLength={6}
      dir="ltr"
      required
      autoFocus={autoFocus}
      className="text-center font-mono text-lg tracking-[0.4em]"
      value={value}
      onChange={(e) => onChange(e.target.value.replace(/\D/g, "").slice(0, 6))}
    />
  )
}

/** A recovery code field ("xxxx-xxxx"): free text, left-to-right, monospace. */
export function RecoveryInput({ id = "recovery", value, onChange }: { id?: string; value: string; onChange: (v: string) => void }) {
  return (
    <Input
      id={id}
      dir="ltr"
      autoComplete="off"
      autoCapitalize="off"
      spellCheck={false}
      required
      className="text-center font-mono tracking-widest"
      placeholder="xxxx-xxxx"
      value={value}
      onChange={(e) => onChange(e.target.value.trim().toLowerCase())}
    />
  )
}

/** The seconds left before a code can be sent again (60 s, as the server enforces). */
export const RESEND_SECONDS = 60
