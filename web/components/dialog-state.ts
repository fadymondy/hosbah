"use client"

import { useEffect, useRef, useState } from "react"

/**
 * open/onOpenChange: a dialog opened by its caller (the site overview canvas) rather than by its own
 * trigger button, which it then does not render.
 */
export type Controlled = { open?: boolean; onOpenChange?: (open: boolean) => void }

/**
 * A dialog's open state — its own, or its caller's when `open` is given. onOpen runs each time it
 * opens either way (to reset a form).
 */
export function useDialogOpen({ open, onOpenChange }: Controlled, onOpen?: () => void) {
  const [own, setOwn] = useState(false)
  const controlled = open !== undefined
  const value = controlled ? open : own
  const wasOpen = useRef(false)
  const onOpenRef = useRef(onOpen)
  onOpenRef.current = onOpen

  useEffect(() => {
    if (controlled && open && !wasOpen.current) onOpenRef.current?.()
    wasOpen.current = !!open
  }, [controlled, open])

  const set = (v: boolean) => {
    if (!controlled) {
      setOwn(v)
      if (v) onOpenRef.current?.()
    }
    onOpenChange?.(v)
  }
  return { open: value, setOpen: set, controlled }
}
