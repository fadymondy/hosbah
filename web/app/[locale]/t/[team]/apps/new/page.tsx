"use client"

import Link from "next/link"
import { useEffect, useState, type FormEvent } from "react"
import { AlertTriangleIcon, CheckCircle2Icon, ChevronDownIcon, InfoIcon, ScanSearchIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { RepoPicker } from "@/components/apps/repo-picker"
import { CopyField } from "@/components/copy-field"
import { FormError, useErrorText } from "@/components/form-error"
import { Notice } from "@/components/states"
import { OperationLog } from "@/components/op-log"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { apiFetch } from "@/lib/api"
import { APP_NAME, runtimeLabel, shortSha, type App, type Deployment, type DetectResult } from "@/lib/apps"
import { useDocumentTitle, useI18n } from "@/lib/i18n"
import { useEventStream, type Operation, type Server } from "@/lib/servers"
import { useTeam } from "@/lib/team"

type Created = { app: App; deploy_hook_url: string; deployment?: Deployment }

export default function NewAppPage() {
  const { t, locale } = useI18n()
  const { team, manage } = useTeam()
  useDocumentTitle(`${t("apps.new.title")} — ${team.name}`)
  const errorText = useErrorText()
  const base = `/${locale}/t/${team.slug}`

  const [servers, setServers] = useState<Server[] | null>(null)
  const [name, setName] = useState("")
  const [repo, setRepo] = useState("")
  const [branch, setBranch] = useState("main")
  const [server, setServer] = useState("")
  const [port, setPort] = useState("")
  const [healthPath, setHealthPath] = useState("")
  const [healthTimeout, setHealthTimeout] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [detectOp, setDetectOp] = useState<Operation | null>(null)
  const [detectErr, setDetectErr] = useState<string | null>(null)
  const [detecting, setDetecting] = useState(false)
  const [created, setCreated] = useState<Created | null>(null)

  useEffect(() => {
    apiFetch<{ servers: Server[] }>(`/api/teams/${team.slug}/servers`)
      .then((r) => {
        const ok = (r.servers ?? []).filter((s) => s.status === "enrolled")
        setServers(ok)
        // ?server=<id> (from a server's "New site") preselects that server.
        const want = new URLSearchParams(window.location.search).get("server")
        const pick = ok.find((s) => s.id === want) ?? ok[0]
        if (pick) setServer((cur) => cur || pick.id)
      })
      .catch((err) => {
        setServers([])
        setError(errorText(err))
      })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [team.slug])

  // The detect operation's final status carries the result; OperationLog shows the log itself.
  useEventStream(
    detectOp ? `/api/teams/${team.slug}/servers/${detectOp.server_id}/operations/${detectOp.id}/stream` : null,
    { status: (d) => setDetectOp(d as Operation), done: () => {}, gone: () => {} },
  )

  const nameOK = APP_NAME.test(name) && name.length <= 40
  const repoOK = /^(https?|git):\/\/\S+$/.test(repo.trim())
  const detected = detectOp?.status === "ok" ? (detectOp.result as DetectResult | undefined) : undefined
  const detectRunning = detectOp !== null && !["ok", "failed", "timeout"].includes(detectOp.status)

  async function detect() {
    setDetecting(true)
    setDetectErr(null)
    try {
      const op = await apiFetch<Operation>(`/api/teams/${team.slug}/servers/${server}/detect`, {
        method: "POST",
        body: { repo_url: repo.trim(), branch: branch.trim() || undefined },
      })
      setDetectOp(op)
    } catch (err) {
      setDetectErr(errorText(err))
    } finally {
      setDetecting(false)
    }
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    if (!nameOK || !repoOK || !server) return
    setBusy(true)
    setError(null)
    try {
      const body: Record<string, unknown> = {
        name,
        server,
        repo_url: repo.trim(),
        deploy: true,
      }
      if (branch.trim()) body.branch = branch.trim()
      if (port) body.port = Number(port)
      if (healthPath.trim()) body.healthcheck_path = healthPath.trim()
      if (healthTimeout) body.healthcheck_timeout = Number(healthTimeout)
      setCreated(await apiFetch<Created>(`/api/teams/${team.slug}/apps`, { method: "POST", body }))
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  if (!manage) {
    return (
      <PageBody title={t("apps.new.title")} description={t("apps.new.description")} bleed>
        <Notice icon={<InfoIcon />}>{t("common.readOnly")}</Notice>
      </PageBody>
    )
  }

  if (created) {
    return (
      <PageBody title={t("apps.new.title")} description={t("apps.new.description")} bleed>
        <section className="flex flex-col gap-4 border-b border-line px-6 py-6 [&>*]:max-w-3xl" data-testid="created">
          <div className="flex items-center gap-2 font-medium">
            <CheckCircle2Icon className="size-5 text-grid-brand" aria-hidden />
            {t("apps.new.createdTitle", { name: created.app.name })}
          </div>
          <p className="text-sm text-muted-foreground">
            {created.deployment ? t("apps.new.createdDeploying") : t("apps.new.createdHint")}
          </p>
          <div className="flex flex-col gap-2">
            <h2 className="font-medium">{t("apps.hook.title")}</h2>
            <p className="text-sm text-muted-foreground">{t("apps.hook.hint")}</p>
            <CopyField value={created.deploy_hook_url} />
            <Alert>
              <AlertTriangleIcon />
              <AlertDescription>{t("apps.hook.once")}</AlertDescription>
            </Alert>
          </div>
          <div>
            <Button nativeButton={false} render={<Link href={`${base}/apps/${created.app.name}`} />}>
              {t("apps.new.open")}
            </Button>
          </div>
        </section>
      </PageBody>
    )
  }

  const noServers = servers !== null && servers.length === 0

  return (
    <PageBody title={t("apps.new.title")} description={t("apps.new.description")} bleed>
      <div className="grid gap-px border-b border-line bg-line lg:grid-cols-[minmax(0,1fr)_minmax(0,22rem)]">
        <form onSubmit={submit} className="flex min-w-0 flex-col gap-6 bg-grid-bg px-6 py-6 [&>*]:max-w-2xl">
          {noServers ? (
            <Alert data-testid="no-servers">
              <InfoIcon />
              <AlertTitle>{t("apps.new.noServersTitle")}</AlertTitle>
              <AlertDescription className="flex flex-col items-start gap-3">
                <span>{t("apps.new.noServersHint")}</span>
                <Button size="sm" variant="outline" nativeButton={false} render={<Link href={`${base}/servers/new`} />}>
                  {t("servers.add")}
                </Button>
              </AlertDescription>
            </Alert>
          ) : null}
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="app-name">{t("apps.name")}</FieldLabel>
              <Input
                id="app-name"
                dir="ltr"
                required
                maxLength={40}
                value={name}
                onChange={(e) => setName(e.target.value)}
                aria-invalid={name !== "" && !nameOK}
                autoComplete="off"
                placeholder="my-app"
              />
              <FieldDescription>{t("apps.new.nameHint")}</FieldDescription>
            </Field>
            {server ? (
              <Field>
                <FieldLabel>{t("github.pickRepo")}</FieldLabel>
                <RepoPicker
                  team={team.slug}
                  serverId={server}
                  connected={!!(servers?.find((s) => s.id === server) as { github?: { connected?: boolean } } | undefined)?.github?.connected}
                  base={`/${locale}/t/${team.slug}`}
                  value={repo}
                  onPick={(url, def) => {
                    setRepo(url)
                    if (def) setBranch(def)
                    setDetectOp(null)
                  }}
                  branch={branch}
                  onBranch={(b) => {
                    setBranch(b)
                    setDetectOp(null)
                  }}
                />
                <FieldDescription>{t("github.pickRepoHint")}</FieldDescription>
              </Field>
            ) : null}
            <Field>
              <FieldLabel htmlFor="app-repo">{server ? t("github.typeUrl") : t("apps.repo")}</FieldLabel>
              <Input
                id="app-repo"
                dir="ltr"
                required
                maxLength={500}
                value={repo}
                onChange={(e) => {
                  setRepo(e.target.value)
                  setDetectOp(null)
                }}
                aria-invalid={repo !== "" && !repoOK}
                autoComplete="off"
                placeholder="https://github.com/owner/repo"
              />
              <FieldDescription>{t("apps.new.repoHint")}</FieldDescription>
            </Field>
            <div className="grid gap-4 sm:grid-cols-2">
              <Field>
                <FieldLabel htmlFor="app-branch">{t("apps.branch")}</FieldLabel>
                <Input
                  id="app-branch"
                  dir="ltr"
                  maxLength={200}
                  value={branch}
                  onChange={(e) => {
                    setBranch(e.target.value)
                    setDetectOp(null)
                  }}
                  autoComplete="off"
                />
              </Field>
              <Field>
                <FieldLabel htmlFor="app-server">{t("apps.server")}</FieldLabel>
                <Select value={server} onValueChange={(v) => v && setServer(v as string)} disabled={!servers?.length}>
                  <SelectTrigger id="app-server" className="w-full">
                    <SelectValue>
                      {(v: string) => servers?.find((s) => s.id === v)?.name ?? t("apps.new.chooseServer")}
                    </SelectValue>
                  </SelectTrigger>
                  <SelectContent>
                    {(servers ?? []).map((s) => (
                      <SelectItem key={s.id} value={s.id}>
                        <span dir="ltr">{s.name}</span>
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
            </div>

            <section className="flex flex-col gap-3 border border-line p-4" aria-labelledby="detect-h">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div className="flex flex-col gap-1">
                  <h2 id="detect-h" className="font-medium">
                    {t("apps.new.detectTitle")}
                  </h2>
                  <p className="text-sm text-muted-foreground">{t("apps.new.detectHint")}</p>
                </div>
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  disabled={!repoOK || !server || detecting || detectRunning}
                  onClick={detect}
                >
                  <ScanSearchIcon />
                  {detecting || detectRunning ? t("common.working") : t("apps.new.detect")}
                </Button>
              </div>
              <FormError message={detectErr} />
              {detected ? (
                <dl className="grid grid-cols-[auto_1fr] gap-x-6 gap-y-2 text-sm" data-testid="detected">
                  <dt className="text-muted-foreground">{t("apps.runtime")}</dt>
                  <dd dir="ltr" className="text-start font-mono text-xs leading-5">
                    {runtimeLabel(`${detected.runtime}${detected.provider && detected.runtime === "railpack" ? `:${detected.provider}` : ""}`)}
                  </dd>
                  {detected.start_command ? (
                    <>
                      <dt className="text-muted-foreground">{t("apps.new.startCommand")}</dt>
                      <dd dir="ltr" className="text-start font-mono text-xs leading-5 break-all">
                        {detected.start_command}
                      </dd>
                    </>
                  ) : null}
                  <dt className="text-muted-foreground">{t("apps.commit")}</dt>
                  <dd dir="ltr" className="min-w-0 truncate text-start font-mono text-xs leading-5">
                    {shortSha(detected.commit)} {detected.message}
                  </dd>
                  <dt className="text-muted-foreground">cloudy.toml</dt>
                  <dd className="text-xs leading-5">
                    {detected.cloudy_toml ? t("apps.new.tomlFound") : t("apps.new.tomlMissing")}
                  </dd>
                </dl>
              ) : null}
              {detectOp?.status === "failed" || detectOp?.status === "timeout" ? (
                <p role="alert" dir="auto" className="text-sm text-destructive">
                  {detectOp.error ?? t("apps.new.detectFailed")}
                </p>
              ) : null}
              {detectOp ? (
                <div className="border">
                  <OperationLog team={team.slug} serverId={detectOp.server_id} op={detectOp} />
                </div>
              ) : null}
            </section>

            <Collapsible>
              <CollapsibleTrigger
                render={<Button type="button" variant="ghost" size="sm" className="group/adv -ms-2 w-fit" />}
              >
                <ChevronDownIcon className="transition-transform group-aria-expanded/adv:rotate-180" />
                {t("apps.new.advanced")}
              </CollapsibleTrigger>
              <CollapsibleContent>
                <div className="grid gap-4 pt-3 sm:grid-cols-3">
                  <Field>
                    <FieldLabel htmlFor="app-port">{t("apps.port")}</FieldLabel>
                    <Input
                      id="app-port"
                      dir="ltr"
                      type="number"
                      min={1}
                      max={65535}
                      placeholder="8080"
                      value={port}
                      onChange={(e) => setPort(e.target.value)}
                    />
                  </Field>
                  <Field>
                    <FieldLabel htmlFor="app-hpath">{t("apps.healthPath")}</FieldLabel>
                    <Input
                      id="app-hpath"
                      dir="ltr"
                      placeholder="/"
                      maxLength={200}
                      value={healthPath}
                      onChange={(e) => setHealthPath(e.target.value)}
                    />
                  </Field>
                  <Field>
                    <FieldLabel htmlFor="app-htimeout">{t("apps.healthTimeout")}</FieldLabel>
                    <Input
                      id="app-htimeout"
                      dir="ltr"
                      type="number"
                      min={5}
                      max={600}
                      placeholder="60"
                      value={healthTimeout}
                      onChange={(e) => setHealthTimeout(e.target.value)}
                    />
                  </Field>
                </div>
                <FieldDescription className="pt-2">{t("apps.new.advancedHint")}</FieldDescription>
              </CollapsibleContent>
            </Collapsible>

            <FormError message={error} />
            <div>
              <Button type="submit" disabled={busy || !nameOK || !repoOK || !server}>
                {busy ? t("common.working") : t("apps.new.create")}
              </Button>
            </div>
          </FieldGroup>
        </form>
        <HowItWorks />
      </div>
    </PageBody>
  )
}

function HowItWorks() {
  const { t } = useI18n()
  return (
    <aside className="flex flex-col bg-grid-bg" aria-labelledby="how-h">
      <h2 id="how-h" className="border-b border-line px-6 pt-5 pb-3 text-base font-medium text-grid-fg">
        {t("apps.new.howTitle")}
      </h2>
      <ol className="flex flex-col divide-y divide-line border-b border-line text-sm lg:border-b-0">
        {(["how1", "how2", "how3", "how4"] as const).map((k, i) => (
          <li key={k} className="flex gap-3 px-6 py-3">
            <span className="font-mono text-muted-foreground tabular-nums">{String(i + 1).padStart(2, "0")}</span>
            <span className="text-muted-foreground">{t(`apps.new.${k}`)}</span>
          </li>
        ))}
      </ol>
    </aside>
  )
}
