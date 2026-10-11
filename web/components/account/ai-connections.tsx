"use client"

import { Badge, EmptyState, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@fadymondy/nasaq/web"
import { BotIcon } from "lucide-react"
import Link from "next/link"
import { useCallback, useEffect, useState } from "react"

import { ConfirmButton } from "@/components/confirm-button"
import { useErrorText } from "@/components/form-error"
import { ErrorBand, LoadingRows, SectionTitle, TableBand } from "@/components/states"
import { apiFetch, type McpToken } from "@/lib/api"
import { useFormatDate, useI18n } from "@/lib/i18n"

type MyToken = McpToken & { team_id: string; team_slug: string; team_name: string }

/**
 * The account's AI connections: every live MCP token the person holds, in every team, and the team
 * each one is bound to. AI connectors (claude.ai, ChatGPT, Claude Code…) connected over OAuth, and
 * keys minted by hand. A connection works with exactly one team; revoking ends it at once.
 */
export function AIConnections() {
  const { t, locale } = useI18n()
  const fmt = useFormatDate()
  const errorText = useErrorText()
  const [tokens, setTokens] = useState<MyToken[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(() => {
    apiFetch<{ tokens: MyToken[] }>("/api/me/mcp-tokens")
      .then((r) => setTokens(r.tokens ?? []))
      .catch((err) => {
        setError(errorText(err))
        setTokens([])
      })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  useEffect(load, [load])

  // ConfirmButton shows a failure itself, so this throws rather than catches.
  async function revoke(tok: MyToken) {
    await apiFetch(`/api/teams/${tok.team_slug}/mcp-tokens/${tok.id}`, { method: "DELETE" })
    load()
  }

  return (
    <section data-testid="ai-connections">
      <SectionTitle title={t("account.connections.title")} hint={t("account.connections.hint")} className="mt-6 border-t" />
      <ErrorBand message={error} />
      {tokens === null ? (
        <LoadingRows />
      ) : tokens.length === 0 ? (
        <EmptyState className="border-b" icon={BotIcon} title={t("account.connections.emptyTitle")} description={t("account.connections.emptyHint")} />
      ) : (
        <TableBand>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("account.connections.name")}</TableHead>
                <TableHead>{t("account.connections.team")}</TableHead>
                <TableHead>{t("tokens.scopes")}</TableHead>
                <TableHead>{t("tokens.lastUsed")}</TableHead>
                <TableHead>
                  <span className="sr-only">{t("tokens.revoke")}</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {tokens.map((tok) => (
                <TableRow key={`${tok.team_id}/${tok.id}`}>
                  <TableCell>
                    <span className="flex min-w-0 items-center gap-2">
                      <span dir="auto" className="truncate font-medium">
                        {tok.name}
                      </span>
                      <Badge variant="outline">{tok.connector ? t("tokens.connector") : t("account.connections.key")}</Badge>
                    </span>
                  </TableCell>
                  <TableCell>
                    <Link href={`/${locale}/t/${tok.team_slug}/tokens`} className="text-nq-brand hover:underline" dir="auto">
                      {tok.team_name}
                    </Link>
                  </TableCell>
                  <TableCell>
                    <span className="flex flex-wrap gap-1">
                      {(tok.scopes ?? []).map((s) => (
                        <Badge key={s} variant="outline" className="font-mono">
                          {s}
                        </Badge>
                      ))}
                    </span>
                  </TableCell>
                  <TableCell dir="ltr" className="text-start">
                    {tok.last_used_at ? fmt(tok.last_used_at) : t("tokens.never")}
                  </TableCell>
                  <TableCell className="text-end">
                    <ConfirmButton
                      label={t("tokens.revoke")}
                      title={t("tokens.revokeTitle")}
                      description={t("account.connections.revokeHint", { name: tok.name, team: tok.team_name })}
                      confirmLabel={t("tokens.revoke")}
                      onConfirm={() => revoke(tok)}
                    />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </TableBand>
      )}
    </section>
  )
}
