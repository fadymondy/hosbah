"use client"

import Link from "next/link"
import type { ReactNode } from "react"
import { ShieldOffIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { useDocumentTitle, useI18n } from "@/lib/i18n"
import { useSession } from "@/lib/session"

/** Every /admin page: platform admins only (the API enforces it too; this is the friendly face). */
export default function AdminLayout({ children }: { children: ReactNode }) {
  const { me } = useSession()
  if (!me.user.platform_admin) return <Forbidden />
  return <>{children}</>
}

function Forbidden() {
  const { t, locale } = useI18n()
  useDocumentTitle(t("errors.forbiddenTitle"))
  return (
    <Empty className="flex-1 border-y">
      <EmptyHeader>
        <EmptyMedia variant="icon">
          <ShieldOffIcon />
        </EmptyMedia>
        <EmptyTitle>
          <span dir="ltr" className="font-mono">
            403
          </span>{" "}
          {t("errors.forbiddenTitle")}
        </EmptyTitle>
        <EmptyDescription>{t("admin.forbidden")}</EmptyDescription>
      </EmptyHeader>
      <Button nativeButton={false} render={<Link href={`/${locale}`} />}>
        {t("common.home")}
      </Button>
    </Empty>
  )
}
