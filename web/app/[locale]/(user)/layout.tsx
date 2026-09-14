"use client"

import type { ReactNode } from "react"

import { AppShell } from "@/components/app-shell"
import { PageLoading } from "@/components/page-loading"
import { SessionGate } from "@/lib/session"

/** /account and /admin: signed-in pages outside any team. */
export default function UserLayout({ children }: { children: ReactNode }) {
  return (
    <SessionGate fallback={<PageLoading />}>
      <AppShell>{children}</AppShell>
    </SessionGate>
  )
}
