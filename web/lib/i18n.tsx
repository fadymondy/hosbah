"use client"

import { createContext, useCallback, useContext, useEffect, type ReactNode } from "react"

import en from "@/lang/en.json"
import type { Locale } from "@/lib/i18n-config"

export type Dict = typeof en

type I18n = {
  locale: Locale
  dict: Dict
  /** Dot-path lookup with {name} interpolation. Falls back to the key itself. */
  t: (key: string, vars?: Record<string, string | number>, fallback?: string) => string
}

const Ctx = createContext<I18n | null>(null)

function lookup(dict: unknown, key: string): string | undefined {
  let node: unknown = dict
  for (const part of key.split(".")) {
    if (node && typeof node === "object" && part in (node as Record<string, unknown>)) {
      node = (node as Record<string, unknown>)[part]
    } else {
      return undefined
    }
  }
  return typeof node === "string" ? node : undefined
}

export function I18nProvider({ locale, dict, children }: { locale: Locale; dict: Dict; children: ReactNode }) {
  const t = useCallback(
    (key: string, vars?: Record<string, string | number>, fallback?: string) => {
      let out = lookup(dict, key) ?? fallback ?? key
      if (vars) for (const [k, v] of Object.entries(vars)) out = out.replaceAll(`{${k}}`, String(v))
      return out
    },
    [dict],
  )
  return <Ctx.Provider value={{ locale, dict, t }}>{children}</Ctx.Provider>
}

export function useI18n(): I18n {
  const ctx = useContext(Ctx)
  if (!ctx) throw new Error("useI18n outside I18nProvider")
  return ctx
}

/** Sets document.title as "<page> · Hosbah". */
export function useDocumentTitle(title: string | null | undefined) {
  const { t } = useI18n()
  const brand = t("common.brand")
  useEffect(() => {
    const entry = { wanted: title ? `${title} · ${brand}` : brand }
    titleStack.push(entry)
    document.title = entry.wanted
    // Next's metadata <title> ("Hosbah") can be committed AFTER this effect — measured on /login
    // in the dark theme — so re-assert whenever something else rewrites the title while mounted.
    // Only the latest title re-asserts: two mounted titles (a panel and its page) would otherwise
    // rewrite each other forever and freeze the tab.
    const observer = new MutationObserver(() => {
      if (titleStack[titleStack.length - 1] === entry && document.title !== entry.wanted) document.title = entry.wanted
    })
    observer.observe(document.head, { subtree: true, childList: true, characterData: true })
    return () => {
      observer.disconnect()
      const i = titleStack.indexOf(entry)
      if (i >= 0) titleStack.splice(i, 1)
      const top = titleStack[titleStack.length - 1]
      if (i === titleStack.length && top) document.title = top.wanted
    }
  }, [title, brand])
}

/** The mounted useDocumentTitle entries, latest last; the latest one owns document.title. */
const titleStack: { wanted: string }[] = []

/** Formats an ISO date for the current locale (the caller wraps it dir="ltr"). */
export function useFormatDate() {
  const { locale } = useI18n()
  return useCallback(
    (value: string | null | undefined) => {
      if (!value) return "—"
      const d = new Date(value)
      if (Number.isNaN(d.getTime())) return value
      const out = new Intl.DateTimeFormat(locale === "ar" ? "ar-EG-u-nu-latn" : "en-GB", {
        dateStyle: "medium",
        timeStyle: "short",
      }).format(d)
      // Callers put dates in dir="ltr" cells (they sit beside LTR data). An Arabic date inside one
      // was reordered by the bidi algorithm ("14م 5:19 .2026/09/"); a right-to-left isolate
      // (RLI … PDI) keeps it in its own reading order wherever it lands.
      return locale === "ar" ? `⁧${out}⁩` : out
    },
    [locale],
  )
}

/** Formats a monthly amount in its currency for the current locale (the caller wraps it dir="ltr"). */
export function useFormatMoney() {
  const { locale } = useI18n()
  return useCallback(
    (amount: number, currency: string) => {
      let out: string
      try {
        out = new Intl.NumberFormat(locale === "ar" ? "ar-EG-u-nu-latn" : "en-US", {
          style: "currency",
          currency: currency || "USD",
          currencyDisplay: "narrowSymbol",
        }).format(amount)
      } catch {
        out = `${amount.toFixed(2)} ${currency}`
      }
      // Same reasoning as useFormatDate: an Arabic-locale currency string is still LTR data, and
      // needs its own reading-order island wherever a bidi (Arabic) context places it.
      return locale === "ar" ? `⁧${out}⁩` : out
    },
    [locale],
  )
}
