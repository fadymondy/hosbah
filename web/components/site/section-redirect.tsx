"use client"

import { usePathname, useRouter, useSearchParams } from "next/navigation"
import { useEffect } from "react"

import { sectionHome } from "@/components/site/sections"
import { LoadingRows } from "@/components/states"

/** An old section route: the section now lives inside a merged tab, so this forwards there. */
export function SectionRedirect({ section }: { section: string }) {
  const router = useRouter()
  const pathname = usePathname() ?? ""
  const params = useSearchParams()
  useEffect(() => {
    const home = sectionHome(section)
    const root = pathname.replace(/\/[^/]+\/?$/, "")
    const next = new URLSearchParams(params.toString())
    if (home) new URLSearchParams(home.query).forEach((v, k) => next.set(k, v))
    const q = next.toString()
    router.replace(`${root}${home?.tab ? `/${home.tab}` : ""}${q ? `?${q}` : ""}`)
  }, [section, pathname, params, router])
  return <LoadingRows />
}
