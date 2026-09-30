"use client"

import { Button, EmptyState } from "@fadymondy/nasaq/web"
import Link from "next/link"
import type { ReactNode } from "react"
import { ShieldOffIcon } from "lucide-react"



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
    <EmptyState className="flex-1 border-y" icon={ShieldOffIcon} title={<><span dir="ltr" className="font-mono">
            403
          </span>{" "}{t("errors.forbiddenTitle")}</>} description={t("admin.forbidden")} actions={<><Button variant="primary" nativeButton={false} render={<Link href={`/${locale}`} />}>
        {t("common.home")}
      </Button></>} />
  )
}
