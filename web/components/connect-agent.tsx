"use client"

import { Badge, Button, Checkbox, Field, FieldLabel, Input, McpConnectApps, McpConnectSheet, McpLogo } from "@fadymondy/nasaq/web"
import Link from "next/link"
import { useEffect, useState, type FormEvent } from "react"
import { TriangleAlertIcon } from "lucide-react"

import { FormError, useErrorText } from "@/components/form-error"
import { apiFetch, MCP_SCOPES } from "@/lib/api"
import { useI18n } from "@/lib/i18n"
import { useOptionalTeam } from "@/lib/team"

/**
 * "Connect your agent" in the header: Nasaq's MCP connect side-over for the team's MCP server
 * (/api/mcp on this origin). The server takes bearer tokens only (no OAuth discovery), so the key
 * step mints an hbu_ token right here; members who cannot mint are sent to an owner or admin.
 */
export function ConnectAgent() {
  const { t, locale } = useI18n()
  const teamCtx = useOptionalTeam()
  const [origin, setOrigin] = useState("")
  useEffect(() => setOrigin(window.location.origin), [])
  if (!teamCtx || !origin) return null
  const { team, manage } = teamCtx

  return (
    <McpConnectSheet
      types={[
        {
          value: "team",
          label: t("connectAgent.team"),
          description: t("connectAgent.teamHint", { team: team.name }),
          icon: <McpLogo />,
          content: (
            <McpConnectApps
              product="Hosbah"
              server={{ name: "hosbah", url: `${origin}/api/mcp` }}
              oauth={false}
              tryHint={t("connectAgent.tryHint")}
              renderKeyForm={(done) =>
                manage ? (
                  <MintKeyForm slug={team.slug} onMinted={done} />
                ) : (
                  <p className="rounded-xl bg-secondary/60 px-3 py-3 text-body-sm text-muted-foreground">
                    {t("connectAgent.askAdmin")}{" "}
                    <Link href={`/${locale}/t/${team.slug}/tokens`} className="text-nq-brand hover:underline">
                      {t("connectAgent.tokensPage")}
                    </Link>
                  </p>
                )
              }
            />
          ),
        },
      ]}
    />
  )
}

/** Mints a team MCP token for the agent being connected; the plaintext goes straight to the steps. */
function MintKeyForm({ slug, onMinted }: { slug: string; onMinted: (token: string) => void }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [name, setName] = useState(() => t("connectAgent.defaultName"))
  const [scopes, setScopes] = useState<string[]>(["read"])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const res = await apiFetch<{ token: string }>(`/api/teams/${slug}/mcp-tokens`, {
        method: "POST",
        body: { name: name.trim(), scopes, expires_in_days: 90 },
      })
      onMinted(res.token)
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={submit} className="flex flex-col gap-4">
      <FormError message={error} />
      <Field>
        <FieldLabel htmlFor="connect-token-name">{t("tokens.name")}</FieldLabel>
        <Input
          id="connect-token-name"
          required
          maxLength={80}
          value={name}
          onChange={(e) => setName(e.target.value)}
          className="rounded-xl"
        />
      </Field>
      <fieldset className="flex flex-col gap-3">
        <legend className="mb-2 text-label">{t("tokens.scopes")}</legend>
        {MCP_SCOPES.map((scope) => (
          <Field className="flex-row items-center" key={scope}>
            <Checkbox
              id={`connect-scope-${scope}`}
              checked={scopes.includes(scope)}
              onCheckedChange={(v) => setScopes((prev) => (v === true ? [...new Set([...prev, scope])] : prev.filter((s) => s !== scope)))}
            />
            <FieldLabel htmlFor={`connect-scope-${scope}`} className="flex flex-wrap items-center gap-2 font-normal">
              <code dir="ltr" className="font-mono text-xs">
                {scope}
              </code>
              <span className="text-muted-foreground">{t(`tokens.scopeHelp.${scope.replace(":", "_")}`)}</span>
              {scope === "destructive" ? (
                <Badge variant="danger">
                  <TriangleAlertIcon />
                  {t("tokens.dangerous")}
                </Badge>
              ) : null}
            </FieldLabel>
          </Field>
        ))}
      </fieldset>
      <p className="text-caption text-muted-foreground">{t("connectAgent.expiry")}</p>
      <Button variant="primary" type="submit" className="self-start" disabled={busy || !name.trim() || scopes.length === 0}>
        {t("connectAgent.mint")}
      </Button>
    </form>
  )
}
