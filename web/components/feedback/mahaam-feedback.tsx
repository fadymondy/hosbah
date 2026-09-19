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
export function MahaamFeedback() {
  const { locale } = useI18n()
  useEffect(() => {
    if (!MAHAAM_FEEDBACK || document.querySelector("script[data-mahaam-feedback]")) return
    const s = document.createElement("script")
    s.src = `${MAHAAM_URL}/embed/v1.js`
    s.defer = true
    s.dataset.key = MAHAAM_FEEDBACK_KEY
    s.dataset.locale = locale
    // Mahaam's floating launcher is shown on every page; the header button and the account menu
    // open the same dialog (window.MahaamFeedback.open()).
    s.dataset.theme = "auto"
    s.dataset.mahaamFeedback = ""
    document.body.appendChild(s)
  }, [locale])
  return null
}

/** Opens Mahaam's report dialog when the widget is loaded; false when it is not (fall back). */
export function openMahaamFeedback(): boolean {
  if (!MAHAAM_FEEDBACK || !window.MahaamFeedback) return false
  window.MahaamFeedback.open()
  return true
}
