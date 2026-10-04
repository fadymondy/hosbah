"use client"

import { SidebarItem } from "@fadymondy/nasaq/web"
import { usePathname, useRouter } from "next/navigation"
import type { MouseEvent, ReactNode } from "react"

/** True for a plain left-click: modified clicks keep the browser's own new-tab behaviour. */
export function isPlainClick(e: MouseEvent) {
  return !(e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey)
}

/** One sidebar item (Nasaq SidebarItem): client-side routing through href plus onClick. */
export function NavLink({
  href,
  icon,
  children,
  exact = false,
  also,
  onNavigate,
}: {
  href: string
  icon: ReactNode
  children: string
  exact?: boolean
  /** Other paths (and their children) that count as this item, e.g. the pages of a settings group. */
  also?: string[]
  onNavigate?: () => void
}) {
  const pathname = usePathname() ?? ""
  const router = useRouter()
  const under = (p: string) => pathname === p || pathname.startsWith(p + "/")
  const active = (exact ? pathname === href : under(href)) || (also ?? []).some(under)
  return (
    <SidebarItem
      href={href}
      icon={icon}
      active={active}
      tooltip={children}
      onClick={(e) => {
        onNavigate?.()
        if (!isPlainClick(e)) return
        e.preventDefault()
        router.push(href)
      }}
    >
      {children}
    </SidebarItem>
  )
}
