"use client"

import { Alert, Button, Collapsible, CollapsiblePanel, CollapsibleTrigger, Field, FieldDescription, FieldLabel, Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@fadymondy/nasaq/web"
import Link from "next/link"
import { useEffect, useState, type FormEvent, type ReactNode } from "react"
import { ArrowLeftIcon, ArrowRightIcon, BoxesIcon, CheckCircle2Icon, CheckIcon, ChevronDownIcon, DownloadIcon, GithubIcon, InfoIcon, LayoutTemplateIcon, PackageIcon, RocketIcon, ScanSearchIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { AppRepoPicker } from "@/components/apps/app-repo-picker"
import { RepoPicker } from "@/components/apps/repo-picker"
import { CopyField } from "@/components/copy-field"
import { FormError, useErrorText } from "@/components/form-error"
import { Notice } from "@/components/states"
import { OperationLog } from "@/components/op-log"
import { ApiError, apiFetch } from "@/lib/api"
import { APP_NAME, runtimeLabel, shortSha, type App, type Deployment, type DetectResult } from "@/lib/apps"
import { useDocumentTitle, useI18n } from "@/lib/i18n"
import { useEventStream, type Operation, type Server } from "@/lib/servers"
import { useTeam } from "@/lib/team"
import { cn } from "@/lib/utils"

type Created = { app: App; deploy_hook_url: string; deployment?: Deployment }
type Recipe = { id: string; name: string; summary: string; kind: "app" | "stack" }

/** The wizard's steps. Services and a domain are added from the app once it exists, so Review says so. */
const STEPS = ["source", "app", "infra", "review"] as const
type Step = (typeof STEPS)[number]

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
  // Set when create-app is refused because an imported app already answers to this name or
  // repository (duplicate_imported): offer to build a separate one anyway.
  const [duplicateNotice, setDuplicateNotice] = useState<string | null>(null)
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

  // ?stack=<recipe> (from the catalog) prefills the repository and branch from that stack.
  const [stack, setStack] = useState<{ name: string; stack: { repo: string; branch: string; notes?: string } } | null>(null)
  const [step, setStep] = useState<Step>("source")
  const [recipes, setRecipes] = useState<Recipe[]>([])
  function pickStack(id: string, replace: boolean) {
    apiFetch<{ name: string; stack: { repo: string; branch: string; notes?: string } }>(`/api/teams/${team.slug}/catalog/${encodeURIComponent(id)}`)
      .then((r) => {
        if (!r.stack) return
        setStack(r)
        setRepo((cur) => (replace ? r.stack.repo : cur || r.stack.repo))
        setBranch((cur) => (replace || !cur || cur === "main" ? r.stack.branch : cur))
        setDetectOp(null)
        setStep("app")
      })
      .catch((err) => setError(errorText(err)))
  }
  useEffect(() => {
    const id = new URLSearchParams(window.location.search).get("stack")
    if (id) pickStack(id, false)
    apiFetch<{ recipes: Recipe[] }>(`/api/teams/${team.slug}/catalog`)
      .then((r) => setRecipes((r.recipes ?? []).filter((x) => x.kind === "stack")))
      .catch(() => {})
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
    if (step !== "review") {
      if (canLeave(step)) setStep(STEPS[STEPS.indexOf(step) + 1])
      return
    }
    await createApp(false)
  }

  /** Whether every step up to and including this one is filled in well enough to move on. */
  function canLeave(s: Step): boolean {
    if (s === "source") return true
    if (s === "app") return nameOK && repoOK
    if (s === "infra") return canLeave("app") && !!server
    return canLeave("infra")
  }

  async function createApp(allowDuplicate: boolean) {
    if (!nameOK || !repoOK || !server) return
    setBusy(true)
    setError(null)
    if (!allowDuplicate) setDuplicateNotice(null)
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
      if (allowDuplicate) body.allow_duplicate = true
      setCreated(await apiFetch<Created>(`/api/teams/${team.slug}/apps`, { method: "POST", body }))
    } catch (err) {
      if (err instanceof ApiError && err.reason === "duplicate_imported") {
        setDuplicateNotice(err.message)
      } else {
        setError(errorText(err))
      }
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
        <section className="flex flex-col gap-4 border-b border-border px-6 py-6 [&>*]:max-w-3xl" data-testid="created">
          <div className="flex items-center gap-2 font-medium">
            <CheckCircle2Icon className="size-5 text-nq-brand" aria-hidden />
            {t("apps.new.createdTitle", { name: created.app.name })}
          </div>
          <p className="text-sm text-muted-foreground">
            {created.deployment ? t("apps.new.createdDeploying") : t("apps.new.createdHint")}
          </p>
          <div className="flex flex-col gap-2">
            <h2 className="font-medium">{t("apps.hook.title")}</h2>
            <p className="text-sm text-muted-foreground">{t("apps.hook.hint")}</p>
            <CopyField value={created.deploy_hook_url} />
            <Alert tone="warning">
{t("apps.hook.once")}
</Alert>
          </div>
          <div>
            <Button variant="primary" nativeButton={false} render={<Link href={`${base}/apps/${created.app.name}`} />}>
              {t("apps.new.open")}
            </Button>
          </div>
        </section>
      </PageBody>
    )
  }

  const noServers = servers !== null && servers.length === 0
  const at = STEPS.indexOf(step)
  const serverName = servers?.find((s) => s.id === server)?.name
  const importServer = servers?.find((s) => s.id === server)?.id ?? servers?.[0]?.id

  return (
    <PageBody title={t("apps.new.title")} description={t("apps.new.description")} bleed>
      <ol className="flex flex-wrap gap-px border-b border-border bg-border" aria-label={t("apps.new.wizard.steps")}>
        {STEPS.map((s, i) => {
          const reachable = i <= at || STEPS.slice(0, i).every((p) => canLeave(p))
          return (
            <li key={s} className="min-w-0 flex-1 bg-background">
              <button
                type="button"
                disabled={!reachable}
                onClick={() => setStep(s)}
                aria-current={s === step ? "step" : undefined}
                className={cn(
                  "flex w-full items-center gap-2 px-4 py-3 text-start text-sm transition-colors enabled:hover:bg-grid-soft disabled:cursor-not-allowed",
                  s === step ? "text-grid-fg shadow-[inset_0_-2px_0_var(--color-grid-brand)]" : "text-grid-muted",
                )}
              >
                <span
                  className={cn(
                    "flex size-5 shrink-0 items-center justify-center border text-[11px] tabular-nums",
                    i < at ? "border-grid-brand bg-grid-brand text-white" : s === step ? "border-grid-brand" : "border-line",
                  )}
                >
                  {i < at ? <CheckIcon className="size-3" /> : i + 1}
                </span>
                <span className="truncate">{t(`apps.new.wizard.${s}`)}</span>
              </button>
            </li>
          )
        })}
      </ol>
      <div className="grid gap-px border-b border-border bg-border lg:grid-cols-[minmax(0,1fr)_minmax(0,22rem)]">
        <form onSubmit={submit} className="flex min-w-0 flex-col gap-6 bg-background px-6 py-6 [&>*]:max-w-2xl">
          {stack && step !== "source" ? (
            <Alert tone="info" title={<>{t("site.stacks.fromStack", { name: stack.name })}</>}>
              {stack.stack.notes ?? null}
            </Alert>
          ) : null}

          {step === "source" ? (
            <div className="flex w-full flex-col gap-5">
              <div className="flex flex-col gap-1">
                <h2 className="font-medium">{t("apps.new.wizard.sourceTitle")}</h2>
                <p className="text-sm text-muted-foreground">{t("apps.new.wizard.sourceHint")}</p>
              </div>
              <div className="grid gap-px border border-border bg-border sm:grid-cols-2">
                <SourceOption
                  icon={<GithubIcon />}
                  title={t("home2.fromGit")}
                  body={t("home2.fromGitBody")}
                  onClick={() => {
                    setStack(null)
                    setStep("app")
                  }}
                />
                <SourceOption icon={<LayoutTemplateIcon />} title={t("home2.fromTemplate")} body={t("home2.fromTemplateBody")} href={`${base}/templates`} />
                {importServer ? (
                  <SourceOption icon={<DownloadIcon />} title={t("home2.fromServer")} body={t("home2.fromServerBody")} href={`${base}/servers/${importServer}/discovered`} />
                ) : null}
                <SourceOption icon={<PackageIcon />} title={t("nav.products")} body={t("apps.new.wizard.productsBody")} href={`${base}/products`} />
              </div>
              {recipes.length ? (
                <section className="flex flex-col gap-2" aria-labelledby="stacks-h">
                  <h3 id="stacks-h" className="text-sm font-medium">
                    {t("home2.fromStack")}
                  </h3>
                  <div className="grid gap-px border border-border bg-border sm:grid-cols-2">
                    {recipes.map((r) => (
                      <SourceOption key={r.id} icon={<BoxesIcon />} title={r.name} body={r.summary} onClick={() => pickStack(r.id, true)} />
                    ))}
                  </div>
                </section>
              ) : null}
            </div>
          ) : null}

          {step === "app" ? (
            <div className="flex w-full flex-col gap-5">
              <Field>
                <FieldLabel htmlFor="app-name">{t("apps.name")}</FieldLabel>
                <Input
                  id="app-name"
                  dir="ltr"
                  required
                  autoFocus
                  maxLength={40}
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  aria-invalid={name !== "" && !nameOK}
                  autoComplete="off"
                  placeholder="my-app"
                />
                <FieldDescription>{t("apps.new.nameHint")}</FieldDescription>
              </Field>
              <AppRepoPicker
                team={team.slug}
                value={repo}
                onPick={(url, def) => {
                  setRepo(url)
                  if (def) setBranch(def)
                  setDetectOp(null)
                }}
              />
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
            </div>
          ) : null}

          {step === "infra" ? (
            <div className="flex w-full flex-col gap-5">
              {noServers ? (
                <Alert tone="info" title={t("apps.new.noServersTitle")} data-testid="no-servers">
                  <div className="flex flex-col items-start gap-3">
                    <span>{t("apps.new.noServersHint")}</span>
                    <Button size="sm" variant="secondary" nativeButton={false} render={<Link href={`${base}/servers/new`} />}>
                      {t("servers.add")}
                    </Button>
                  </div>
                </Alert>
              ) : null}
              <Field>
                <FieldLabel htmlFor="app-server">{t("apps.server")}</FieldLabel>
                <Select
                  value={server}
                  onValueChange={(v) => {
                    if (!v) return
                    setServer(v as string)
                    setDetectOp(null)
                  }}
                  disabled={!servers?.length}
                >
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
                <FieldDescription>{t("apps.new.wizard.serverHint")}</FieldDescription>
              </Field>

              <section className="flex flex-col gap-3 border border-border p-4" aria-labelledby="detect-h">
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
                    variant="secondary"
                    disabled={!repoOK || !server || detecting || detectRunning}
                    onClick={detect}
                  >
                    <ScanSearchIcon />
                    {detecting || detectRunning ? t("common.working") : t("apps.new.detect")}
                  </Button>
                </div>
                <FormError message={detectErr} />
                {detected ? <Detected d={detected} /> : null}
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
                <CollapsiblePanel>
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
                  <p className="pt-2 text-caption text-muted-foreground">{t("apps.new.advancedHint")}</p>
                </CollapsiblePanel>
              </Collapsible>
            </div>
          ) : null}

          {step === "review" ? (
            <div className="flex w-full flex-col gap-5">
              <dl className="grid grid-cols-[auto_1fr] gap-x-6 gap-y-2 border border-border p-4 text-sm" data-testid="review">
                <dt className="text-muted-foreground">{t("apps.name")}</dt>
                <dd dir="ltr" className="text-start font-mono text-xs leading-5">
                  {name}
                </dd>
                <dt className="text-muted-foreground">{t("apps.repo")}</dt>
                <dd dir="ltr" className="min-w-0 truncate text-start font-mono text-xs leading-5">
                  {repo.trim()}
                </dd>
                <dt className="text-muted-foreground">{t("apps.branch")}</dt>
                <dd dir="ltr" className="text-start font-mono text-xs leading-5">
                  {branch.trim() || "main"}
                </dd>
                <dt className="text-muted-foreground">{t("apps.server")}</dt>
                <dd dir="ltr" className="text-start text-xs leading-5">
                  {serverName ?? "—"}
                </dd>
                <dt className="text-muted-foreground">{t("apps.runtime")}</dt>
                <dd className="text-start text-xs leading-5">
                  {detected ? (
                    <span dir="ltr" className="font-mono">
                      {runtimeLabel(`${detected.runtime}${detected.provider && detected.runtime === "railpack" ? `:${detected.provider}` : ""}`)}
                    </span>
                  ) : (
                    t("apps.new.wizard.detectAtBuild")
                  )}
                </dd>
                <dt className="text-muted-foreground">{t("apps.port")}</dt>
                <dd dir="ltr" className="text-start font-mono text-xs leading-5">
                  {port || "8080"} · {healthPath.trim() || "/"} · {healthTimeout || "60"}s
                </dd>
              </dl>
              <Notice icon={<InfoIcon />} title={t("apps.new.wizard.laterTitle")}>
                {t("apps.new.wizard.laterBody")}
              </Notice>
              {duplicateNotice ? (
                <Alert tone="info" title={duplicateNotice} data-testid="duplicate-imported">
                  <div className="flex flex-col items-start gap-3">
                    <span>{t("apps.new.duplicateImportedHint")}</span>
                    <Button type="button" size="sm" variant="secondary" disabled={busy} onClick={() => createApp(true)}>
                      {busy ? t("common.working") : t("apps.new.duplicateImportedOverride")}
                    </Button>
                  </div>
                </Alert>
              ) : null}
            </div>
          ) : null}

          <FormError message={error} />
          {step !== "source" ? (
            <div className="flex w-full items-center gap-2">
              <Button type="button" variant="ghost" onClick={() => setStep(STEPS[at - 1])}>
                <ArrowLeftIcon className="rtl:-scale-x-100" />
                {t("common.back")}
              </Button>
              {step === "review" ? (
                <Button variant="primary" type="submit" disabled={busy || !canLeave("review")}>
                  <RocketIcon />
                  {busy ? t("common.working") : t("apps.new.create")}
                </Button>
              ) : (
                <Button variant="primary" type="submit" disabled={!canLeave(step)}>
                  {t("common.next")}
                  <ArrowRightIcon className="rtl:-scale-x-100" />
                </Button>
              )}
            </div>
          ) : null}
        </form>
        <HowItWorks />
      </div>
    </PageBody>
  )
}

function Detected({ d }: { d: DetectResult }) {
  const { t } = useI18n()
  return (
    <dl className="grid grid-cols-[auto_1fr] gap-x-6 gap-y-2 text-sm" data-testid="detected">
      <dt className="text-muted-foreground">{t("apps.runtime")}</dt>
      <dd dir="ltr" className="text-start font-mono text-xs leading-5">
        {runtimeLabel(`${d.runtime}${d.provider && d.runtime === "railpack" ? `:${d.provider}` : ""}`)}
      </dd>
      {d.start_command ? (
        <>
          <dt className="text-muted-foreground">{t("apps.new.startCommand")}</dt>
          <dd dir="ltr" className="text-start font-mono text-xs leading-5 break-all">
            {d.start_command}
          </dd>
        </>
      ) : null}
      <dt className="text-muted-foreground">{t("apps.commit")}</dt>
      <dd dir="ltr" className="min-w-0 truncate text-start font-mono text-xs leading-5">
        {shortSha(d.commit)} {d.message}
      </dd>
      <dt className="text-muted-foreground">hosbah.toml</dt>
      <dd className="text-xs leading-5">{(d.hosbah_toml ?? d.cloudy_toml) ? t("apps.new.tomlFound") : t("apps.new.tomlMissing")}</dd>
    </dl>
  )
}

/** One way to start: a step inside the wizard (onClick) or a page that already does it (href). */
function SourceOption({ icon, title, body, href, onClick }: { icon: ReactNode; title: string; body: string; href?: string; onClick?: () => void }) {
  const cls = "group flex w-full items-start gap-3 bg-background p-4 text-start transition-colors hover:bg-grid-soft focus-visible:outline-2 focus-visible:outline-grid-action"
  const inner = (
    <>
      <span className="flex size-9 shrink-0 items-center justify-center border border-line text-grid-fg [&_svg]:size-4">{icon}</span>
      <span className="min-w-0 flex-1">
        <span className="block text-sm font-medium text-grid-fg">{title}</span>
        <span className="mt-0.5 block text-xs text-grid-muted">{body}</span>
      </span>
      <ArrowRightIcon className="mt-1 size-4 shrink-0 text-grid-muted transition-transform group-hover:translate-x-0.5 rtl:-scale-x-100 rtl:group-hover:-translate-x-0.5" />
    </>
  )
  return href ? (
    <Link href={href} className={cls}>
      {inner}
    </Link>
  ) : (
    <button type="button" onClick={onClick} className={cls}>
      {inner}
    </button>
  )
}

function HowItWorks() {
  const { t } = useI18n()
  return (
    <aside className="flex flex-col bg-background" aria-labelledby="how-h">
      <h2 id="how-h" className="border-b border-border px-6 pt-5 pb-3 text-base font-medium text-foreground">
        {t("apps.new.howTitle")}
      </h2>
      <ol className="flex flex-col divide-y divide-border border-b border-border text-sm lg:border-b-0">
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
