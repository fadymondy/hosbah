"use client"

import { PageBody } from "@/components/app-shell"
import { McpTokens } from "@/components/mcp-tokens"
import { ADMIN_MCP_ENDPOINT } from "@/lib/config"
import { useDocumentTitle, useI18n } from "@/lib/i18n"

export default function AdminTokensPage() {
  const { t } = useI18n()
  useDocumentTitle(t("admin.nav.tokens"))
  return (
    <PageBody eyebrow={t("nav.platform")} title={t("admin.nav.tokens")} description={t("admin.description")}>
      <McpTokens
        basePath="/api/admin/mcp-tokens"
        endpoint={ADMIN_MCP_ENDPOINT}
        withScopes={false}
        canCreate
        serverName="hosbah-admin"
      />
    </PageBody>
  )
}
