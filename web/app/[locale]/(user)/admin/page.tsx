"use client"

import Link from "next/link"
import { ShieldOffIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { McpTokens } from "@/components/mcp-tokens"
import { Button } from "@/components/ui/button"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { ADMIN_MCP_ENDPOINT } from "@/lib/config"
import { useDocumentTitle, useI18n } from "@/lib/i18n"
import { useSession } from "@/lib/session"

export default function AdminPage() {
  const { t, locale } = useI18n()
  const { me } = useSession()
  useDocumentTitle(me.user.platform_admin ? t("admin.title") : t("errors.forbiddenTitle"))

  if (!me.user.platform_admin) {
    return (
      <Empty className="flex-1">
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

  return (
    <PageBody title={t("admin.title")} description={t("admin.description")}>
      <McpTokens
        basePath="/api/admin/mcp-tokens"
        endpoint={ADMIN_MCP_ENDPOINT}
        withScopes={false}
        canCreate
        serverName="cloudy-admin"
      />
    </PageBody>
  )
}
