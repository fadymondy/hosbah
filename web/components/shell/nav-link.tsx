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
  onNavigate,
}: {
  href: string
  icon: ReactNode
  children: string
  exact?: boolean
  onNavigate?: () => void
}) {
  const pathname = usePathname() ?? ""
  const router = useRouter()
  const active = exact ? pathname === href : pathname === href || pathname.startsWith(href + "/")
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
