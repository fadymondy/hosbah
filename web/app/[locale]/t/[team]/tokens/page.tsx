"use client"

import { PageBody } from "@/components/app-shell"
import { McpTokens } from "@/components/mcp-tokens"
import { TEAM_MCP_ENDPOINT } from "@/lib/config"
import { useDocumentTitle, useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

export default function TokensPage() {
  const { t } = useI18n()
  const { team, manage } = useTeam()
  useDocumentTitle(`${t("nav.tokens")} — ${team.name}`)
  return (
    <PageBody title={t("nav.tokens")} description={t("tokens.description")} bleed>
      <McpTokens
        basePath={`/api/teams/${team.slug}/mcp-tokens`}
        endpoint={TEAM_MCP_ENDPOINT}
        withScopes
        canCreate={manage}
        serverName={`hosbah-${team.slug}`}
        bleed
      />
    </PageBody>
  )
}
