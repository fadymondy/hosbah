"use client"

import { Badge, Button, Input, toast } from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useState, type FormEvent } from "react"
import { CheckIcon, CopyIcon, ExternalLinkIcon, GithubIcon, LogOutIcon, RefreshCwIcon } from "lucide-react"

import { ConfirmButton } from "@/components/confirm-button"
import { useErrorText } from "@/components/form-error"
import { ErrorBand, SectionTitle, SettingsRow } from "@/components/states"



import { apiFetch } from "@/lib/api"
import { useI18n } from "@/lib/i18n"
import { useEventStream, type LogLine, type Operation } from "@/lib/servers"

/*
The server's GitHub connection: the GitHub CLI (gh) on this server, signed in to a GitHub account.
With it, Hosbah lists that account's repositories for new apps and clones private ones on deploy.
Signing in is GitHub's device flow run on the server: gh prints a one-time code, shown here, which
the person enters at github.com/login/device. A token can be pasted instead.
*/

export type GitHubStatus = {
  installed?: boolean
  version?: string
  logged_in?: boolean
  connected?: boolean
  user?: string | null
  scopes?: string[] | string | null
  checked_at?: string | null
}

/** Reads the status whatever the envelope ({github:{…}} or the fields themselves). */
function asStatus(r: unknown): GitHubStatus {
  const o = (r ?? {}) as Record<string, unknown>
  return ((o.github as GitHubStatus) ?? (o.status as GitHubStatus) ?? o) as GitHubStatus
}

export const isConnected = (s: GitHubStatus | null | undefined) => !!(s && (s.logged_in ?? s.connected))

const CODE_RE = /\b([A-Z0-9]{4}-[A-Z0-9]{4})\b/
const URL_RE = /https:\/\/github\.com\/login\/device\S*/

/** The device code and link, from a structured event field or from gh's own printed line. */
function deviceFrom(l: LogLine & Record<string, unknown>): { code?: string; url?: string } {
  // The agent attaches {type:"github.device_code", user_code, verification_uri} as the line's data.
  const data = (l.data ?? {}) as Record<string, unknown>
  const code = (data.user_code ?? l.user_code ?? l.code) as string | undefined
  const url = (data.verification_uri ?? l.verification_uri ?? l.url) as string | undefined
  const text = [l.line, l.message, JSON.stringify(l.data ?? "")].filter(Boolean).join(" ")
  return {
    code: code ?? CODE_RE.exec(text)?.[1],
    url: url ?? URL_RE.exec(text)?.[0],
  }
}

export function ServerGitHub({ team, serverId, manage }: { team: string; serverId: string; manage: boolean }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const path = `/api/teams/${encodeURIComponent(team)}/servers/${serverId}/github`
  const [status, setStatus] = useState<GitHubStatus | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [op, setOp] = useState<Operation | null>(null)
  const [device, setDevice] = useState<{ code?: string; url?: string }>({})
  const [finished, setFinished] = useState<string | null>(null)
  const [tokenMode, setTokenMode] = useState(false)
  const [token, setToken] = useState("")
  const [busy, setBusy] = useState(false)

  const load = useCallback(
    async (refresh = false) => {
      setLoading(true)
      try {
        setStatus(asStatus(await apiFetch(`${path}${refresh ? "?refresh=true" : ""}`)))
        setError(null)
      } catch (err) {
        setError(errorText(err))
      } finally {
        setLoading(false)
      }
    },
    [path, errorText],
  )

  useEffect(() => {
    void load()
  }, [load])

  // The sign-in runs as an operation on the server; its events carry the one-time code.
  useEventStream(op ? `/api/teams/${encodeURIComponent(team)}/servers/${serverId}/operations/${op.id}/stream` : null, {
    line: (d) => {
      const found = deviceFrom(d as LogLine & Record<string, unknown>)
      if (found.code || found.url) setDevice((prev) => ({ code: prev.code ?? found.code, url: prev.url ?? found.url }))
    },
    status: (d) => {
      const s = (d as Operation).status
      if (s === "ok" || s === "failed" || s === "timeout") {
        setFinished(s)
        void load(true)
        if (s === "ok") toast.success(t("github.connected"))
      }
    },
    done: () => {},
    gone: () => {},
  })

  async function connect(mode: "device" | "token", e?: FormEvent) {
    e?.preventDefault()
    setBusy(true)
    setError(null)
    setDevice({})
    setFinished(null)
    try {
      const r = await apiFetch<{ operation: Operation } | Operation>(`${path}/connect`, {
        method: "POST",
        body: mode === "token" ? { mode, token: token.trim() } : { mode },
      })
      setOp("operation" in (r as { operation: Operation }) ? (r as { operation: Operation }).operation : (r as Operation))
      setToken("")
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  async function disconnect() {
    try {
      await apiFetch(path, { method: "DELETE" })
    } catch (err) {
      throw new Error(errorText(err))
    }
    setOp(null)
    setDevice({})
    await load(true)
  }

  const connected = isConnected(status)
  const waiting = op && !finished
  const scopes = Array.isArray(status?.scopes) ? status?.scopes.join(", ") : status?.scopes

  return (
    <section aria-labelledby="github-h" data-testid="server-github">
      <SectionTitle
        id="github-h"
        title={t("github.title")}
        hint={t("github.hint")}
        action={
          <span className="flex items-center gap-2">
            {status ? (
              <Badge variant={connected ? "brand" : "outline"}>
                {connected ? t("github.statusConnected") : t("github.statusNotConnected")}
              </Badge>
            ) : null}
            <Button variant="ghost" size="icon-sm" aria-label={t("github.refresh")} disabled={loading} onClick={() => void load(true)}>
              <RefreshCwIcon className={loading ? "animate-spin" : ""} />
            </Button>
          </span>
        }
      />
      <ErrorBand message={error} />

      {connected ? (
        <SettingsRow label={t("github.account")} hint={scopes ? `${t("github.scopes")}: ${scopes}` : undefined}>
          <span className="flex flex-wrap items-center gap-3">
            <span className="inline-flex items-center gap-2 font-medium" dir="ltr">
              <GithubIcon className="size-4" />
              {status?.user ? `@${status.user}` : "—"}
            </span>
            {status?.version ? (
              <span dir="ltr" className="font-mono text-xs text-muted-foreground">
                gh {status.version}
              </span>
            ) : null}
            {manage ? (
              <ConfirmButton
                icon={<LogOutIcon />}
                label={t("github.disconnect")}
                title={t("github.disconnectTitle")}
                description={t("github.disconnectBody")}
                confirmLabel={t("github.disconnect")}
                onConfirm={disconnect}
              />
            ) : null}
          </span>
        </SettingsRow>
      ) : manage ? (
        <>
          {waiting && (device.code || device.url) ? (
            <DeviceCode code={device.code} url={device.url} />
          ) : waiting ? (
            <p className="border-b border-border px-6 py-4 text-sm text-muted-foreground">{t("github.starting")}</p>
          ) : finished && finished !== "ok" ? (
            <p className="border-b border-border px-6 py-4 text-sm text-nq-danger-text">{t("github.failed")}</p>
          ) : null}

          {!waiting ? (
            tokenMode ? (
              <form onSubmit={(e) => void connect("token", e)}>
                <SettingsRow label={t("github.token")} htmlFor="gh-token" hint={t("github.tokenHint")}>
                  <Input
                    id="gh-token"
                    type="password"
                    dir="ltr"
                    autoComplete="off"
                    placeholder="github_pat_…"
                    value={token}
                    onChange={(e) => setToken(e.target.value)}
                  />
                </SettingsRow>
                <div className="flex flex-wrap justify-end gap-2 border-b border-border px-6 py-4">
                  <Button type="button" variant="ghost" onClick={() => setTokenMode(false)}>
                    {t("github.useBrowser")}
                  </Button>
                  <Button variant="primary" type="submit" disabled={busy || token.trim().length < 20}>
                    {busy ? t("common.working") : t("github.connectToken")}
                  </Button>
                </div>
              </form>
            ) : (
              <div className="flex flex-wrap items-center gap-2 border-b border-border px-6 py-4">
                <Button variant="primary" disabled={busy} onClick={() => void connect("device")}>
                  <GithubIcon />
                  {busy ? t("common.working") : t("github.connect")}
                </Button>
                <Button variant="ghost" onClick={() => setTokenMode(true)}>
                  {t("github.useToken")}
                </Button>
              </div>
            )
          ) : null}
        </>
      ) : (
        <p className="border-b border-border px-6 py-4 text-sm text-muted-foreground">{t("github.askAdmin")}</p>
      )}
    </section>
  )
}

/** The one-time code, big and copyable, and the link where GitHub asks for it. */
function DeviceCode({ code, url }: { code?: string; url?: string }) {
  const { t } = useI18n()
  const [copied, setCopied] = useState(false)
  const link = url ?? "https://github.com/login/device"
  return (
    <div className="flex flex-col gap-4 border-b border-border bg-card px-6 py-5" role="status" aria-live="polite">
      <p className="text-sm text-foreground">{t("github.deviceIntro")}</p>
      <div className="flex flex-wrap items-center gap-3">
        <code dir="ltr" className="border border-border bg-background px-4 py-2 font-mono text-2xl tracking-[0.3em] text-foreground">
          {code ?? "····-····"}
        </code>
        {code ? (
          <Button
            variant="secondary"
            onClick={() => {
              void navigator.clipboard.writeText(code)
              setCopied(true)
              setTimeout(() => setCopied(false), 1500)
            }}
          >
            {copied ? <CheckIcon /> : <CopyIcon />}
            {copied ? t("github.copied") : t("github.copy")}
          </Button>
        ) : null}
        <Button variant="primary" nativeButton={false} render={<a href={link} target="_blank" rel="noreferrer" />}>
          <ExternalLinkIcon />
          {t("github.openGitHub")}
        </Button>
      </div>
      <p className="text-xs text-muted-foreground">{t("github.deviceWaiting")}</p>
    </div>
  )
}
