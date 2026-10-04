"use client"

import Link from "next/link"
import { usePathname } from "next/navigation"
import type { ReactNode } from "react"

import { cn } from "@/lib/utils"

/** The Settings → Security pages (team paths); the sidebar's Security item is active on any of them. */
export const SECURITY_PAGES = ["tokens", "ssh-keys", "credentials", "certificates"]

export type SubNavItem = {
  href: string
  label: ReactNode
  icon?: ReactNode
  /** Only this exact path, not its children. */
  exact?: boolean
  /** Other paths (and their children) that count as this item. */
  also?: string[]
  /** An in-page anchor (#…): it never shows as the current item. */
  anchor?: boolean
}

/**
 * A row of links across the top of a group of pages (Settings, Team). It scrolls sideways on a
 * small screen instead of wrapping, and the current page carries the action underline.
 */
export function SubNav({ items, label, className }: { items: SubNavItem[]; label: string; className?: string }) {
  const pathname = usePathname() ?? ""
  const under = (p: string) => pathname === p || pathname.startsWith(p + "/")
  return (
    <nav aria-label={label} className={cn("border-b border-line", className)}>
      <ul className="flex gap-1 overflow-x-auto px-4 [scrollbar-width:none] sm:px-6">
        {items.map((it) => {
          const active = !it.anchor && ((it.exact ? pathname === it.href : under(it.href)) || (it.also ?? []).some(under))
          return (
            <li key={it.href} className="shrink-0">
              <Link
                href={it.href}
                aria-current={active ? "page" : undefined}
                className={cn(
                  "-mb-px flex items-center gap-2 border-b-2 px-2.5 py-2.5 text-sm whitespace-nowrap transition-colors [&_svg]:size-4 [&_svg]:shrink-0",
                  active
                    ? "border-grid-action font-medium text-grid-fg"
                    : "border-transparent text-grid-muted hover:text-grid-fg",
                )}
              >
                {it.icon}
                {it.label}
              </Link>
            </li>
          )
        })}
      </ul>
    </nav>
  )
}
