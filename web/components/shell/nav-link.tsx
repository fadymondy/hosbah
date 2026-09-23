"use client"

import Link from "next/link"
import { usePathname } from "next/navigation"
import type { ReactNode } from "react"

import { cn } from "@/lib/utils"

/** One sidebar item: icon and label, only the icon when the sidebar is minimised. */
export function NavLink({
  href,
  icon,
  children,
  exact = false,
  onNavigate,
}: {
  href: string
  icon: ReactNode
  children: ReactNode
  exact?: boolean
  onNavigate?: () => void
}) {
  const pathname = usePathname() ?? ""
  const active = exact ? pathname === href : pathname === href || pathname.startsWith(href + "/")
  return (
    <Link
      href={href}
      onClick={onNavigate}
      aria-current={active ? "page" : undefined}
      title={typeof children === "string" ? children : undefined}
      className={cn(
        "flex items-center gap-2.5 border-s-2 px-3 py-2 text-sm transition-colors [&_svg]:size-4 [&_svg]:shrink-0",
        "group-data-[collapsed=true]/nav:justify-center group-data-[collapsed=true]/nav:px-0 group-data-[collapsed=true]/nav:py-2.5",
        active
          ? "border-grid-action bg-grid-soft font-medium text-grid-fg"
          : "border-transparent text-grid-body hover:bg-grid-soft hover:text-grid-fg",
      )}
    >
      {icon}
      <span className="truncate group-data-[collapsed=true]/nav:hidden">{children}</span>
    </Link>
  )
}

export function GroupLabel({ children }: { children: ReactNode }) {
  return (
    <p className="grid-micro mt-4 border-t border-line px-4 pt-4 pb-1 group-data-[collapsed=true]/nav:mt-2 group-data-[collapsed=true]/nav:p-0">
      <span className="group-data-[collapsed=true]/nav:hidden">{children}</span>
    </p>
  )
}
