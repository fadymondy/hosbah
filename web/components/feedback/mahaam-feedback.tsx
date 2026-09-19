"use client"

import { useEffect } from "react"

import { MAHAAM_FEEDBACK, MAHAAM_FEEDBACK_KEY, MAHAAM_URL } from "@/lib/config"
import { useI18n } from "@/lib/i18n"

type MahaamFeedbackApi = { open: () => void; close: () => void; show: () => void }

declare global {
  interface Window {
    MahaamFeedback?: MahaamFeedbackApi
  }
}

/**
 * Loads Mahaam's Feedback widget ({MAHAAM_URL}/embed/v1.js) once, the same way
 * @mahaam/feedback/react does: the script tag carries the public key and the locale, and the
 * widget exposes window.MahaamFeedback. Renders nothing; a no-op when Mahaam is not configured.
 */
// How many signed-in shells are mounted, and a pending teardown. React may unmount and remount at
// once (Strict Mode in development, a layout re-keying); the teardown waits a tick so a remount
// cancels it instead of loading the widget twice.
let mounted = 0
let teardown: ReturnType<typeof setTimeout> | undefined

/** The widget's own top-level elements (it renders into shadow roots appended to <body>). */
function widgetHosts(): Element[] {
  return Array.from(document.body.children).filter((el) => {
    const root = (el as HTMLElement).shadowRoot
    return !!root && !!root.querySelector(".launcher, [id^='mf-']")
  })
}

function removeWidget() {
  document.querySelectorAll("script[data-mahaam-feedback]").forEach((s) => s.remove())
  widgetHosts().forEach((el) => el.remove())
  delete window.MahaamFeedback
}

export function MahaamFeedback() {
  const { locale } = useI18n()
  useEffect(() => {
    if (!MAHAAM_FEEDBACK) return
    mounted++
    clearTimeout(teardown)
    if (!document.querySelector("script[data-mahaam-feedback]") && !window.MahaamFeedback) {
      const s = document.createElement("script")
      s.src = `${MAHAAM_URL}/embed/v1.js`
      s.defer = true
      s.dataset.key = MAHAAM_FEEDBACK_KEY
      s.dataset.locale = locale
      // No floating launcher: the header's "Report a problem" button (and the account menu on phones)
      // opens the dialog through window.MahaamFeedback.open().
      s.dataset.launcher = "false"
      s.dataset.theme = "auto"
      s.dataset.mahaamFeedback = ""
      document.body.appendChild(s)
    }
    // Only signed-in pages mount this (it lives in the app shell). When the last one goes — signing
    // out reaches the sign-in page without a reload — the widget goes too, so it never floats over
    // the public pages.
    return () => {
      mounted--
      teardown = setTimeout(() => {
        if (mounted === 0) removeWidget()
      }, 0)
    }
  }, [locale])
  return null
}

/** Opens Mahaam's report dialog when the widget is loaded; false when it is not (fall back). */
export function openMahaamFeedback(): boolean {
  if (!MAHAAM_FEEDBACK || !window.MahaamFeedback) return false
  window.MahaamFeedback.open()
  return true
}
