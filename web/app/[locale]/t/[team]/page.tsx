"use client"

import Link from "next/link"
import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react"
import {
  AppWindowIcon,
  ArrowRightIcon,
  BoxesIcon,
  DatabaseIcon,
  DownloadIcon,
  GithubIcon,
  GlobeIcon,
  LayoutTemplateIcon,
  PackageIcon,
  PlusIcon,
  RocketIcon,
  ServerIcon,
  TriangleAlertIcon,
} from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { DeploymentsFeed, type FeedItem } from "@/components/home/deployments-feed"
import { LoadingRows, SectionTitle } from "@/components/states"
import { Button } from "@/components/ui/button"
import { apiFetch } from "@/lib/api"
import type { App } from "@/lib/apps"
import type { DataService } from "@/lib/databases"
import { useDocumentTitle, useI18n } from "@/lib/i18n"
import type { Product } from "@/lib/products"
import type { Server } from "@/lib/servers"
import { useSession } from "@/lib/session"
import { useTeam } from "@/lib/team"
import { cn } from "@/lib/utils"

type ProxyHost = { id: string; hostname: string; verified: boolean; enabled: boolean }
type Recipe = { id: string; name: string; summary: string; kind: "app" | "stack" }

/** Calls an endpoint that newer APIs have; null (not an error) when this API does not have it yet. */
async function optional<T>(path: string): Promise<T | null> {
  try {
    return await apiFetch<T>(path)
  } catch {
    return null
  }
}

/** One number the team cares about, linking to its list. */
function SummaryCard({
  href,
  icon,
  label,
  value,
  detail,
  tone,
}: {
  href: string
  icon: ReactNode
  label: string
  value: ReactNode
  detail: ReactNode
  tone?: "ok" | "warn"
}) {
  return (
    <Link
      href={href}
      className="group flex flex-col gap-3 bg-grid-card p-5 transition-colors hover:bg-grid-soft focus-visible:outline-2 focus-visible:outline-grid-action"
    >
      <span className="flex items-center justify-between text-grid-muted [&_svg]:size-4">
        <span className="grid-micro">{label}</span>
        {icon}
      </span>
      <span className="grid-title text-3xl tabular-nums">{value}</span>
      <span
        className={cn(
          "flex items-center gap-1.5 text-xs",
          tone === "warn" ? "text-grid-danger" : tone === "ok" ? "text-grid-ok" : "text-grid-muted",
        )}
      >
        {detail}
      </span>
    </Link>
  )
}

/** A way to deploy something: a real source the team can use right now. */
function SourceCard({ href, icon, title, body, badge }: { href: string; icon: ReactNode; title: string; body: string; badge?: string }) {
  return (
    <Link
      href={href}
      className="group flex items-start gap-3 bg-grid-card p-4 transition-colors hover:bg-grid-soft focus-visible:outline-2 focus-visible:outline-grid-action"
    >
      <span className="flex size-9 shrink-0 items-center justify-center border border-line text-grid-fg [&_svg]:size-4">{icon}</span>
      <span className="min-w-0 flex-1">
        <span className="flex items-center gap-2 text-sm font-medium text-grid-fg">
          {title}
          {badge ? <span className="border border-line px-1.5 text-[10px] font-normal text-grid-muted uppercase">{badge}</span> : null}
        </span>
        <span className="mt-0.5 block text-xs text-grid-muted">{body}</span>
      </span>
      <ArrowRightIcon className="mt-1 size-4 shrink-0 text-grid-muted transition-transform group-hover:translate-x-0.5 rtl:-scale-x-100 rtl:group-hover:-translate-x-0.5" />
    </Link>
  )
}

/**
 * The team's home: what is running and whether it is healthy (four counts), what needs attention
 * now, the ways to deploy something, and what shipped lately. Refreshes every 30 s while open.
 */
export default function HomePage() {
  const { t, locale } = useI18n()
  const { team, manage } = useTeam()
  const { me } = useSession()
  useDocumentTitle(`${t("nav.home")} — ${team.name}`)
  const base = `/${locale}/t/${team.slug}`
  const api = `/api/teams/${team.slug}`

  const [apps, setApps] = useState<App[] | null>(null)
  const [servers, setServers] = useState<Server[] | null>(null)
  const [services, setServices] = useState<DataService[] | null>(null)
  const [proxies, setProxies] = useState<ProxyHost[]>([])
  const [feed, setFeed] = useState<FeedItem[] | null>(null)
  const [products, setProducts] = useState<Product[]>([])
  const [recipes, setRecipes] = useState<Recipe[]>([])

  const load = useCallback(async () => {
    const [a, s, d, p, f] = await Promise.all([
      optional<{ apps: App[] }>(`${api}/apps`),
      optional<{ servers: Server[] }>(`${api}/servers`),
      optional<{ services: DataService[] }>(`${api}/data-services`),
      optional<{ proxy_hosts: ProxyHost[] }>(`${api}/proxy-hosts`),
      optional<{ deployments: FeedItem[] }>(`${api}/deployments?limit=8`),
    ])
    setApps(a?.apps ?? [])
    setServers(s?.servers ?? [])
    setServices(d?.services ?? [])
    setProxies(p?.proxy_hosts ?? [])
    setFeed(f?.deployments ?? [])
  }, [api])

  useEffect(() => {
    void load()
    const id = setInterval(load, 30_000)
    return () => clearInterval(id)
  }, [load])

  // Ready-to-deploy sources only change when someone adds one; load them once.
  useEffect(() => {
    optional<{ products: Product[] }>(`${api}/products`).then((r) => setProducts(r?.products ?? []))
    optional<{ recipes: Recipe[] }>(`${api}/catalog`).then((r) => setRecipes(r?.recipes ?? []))
  }, [api])

  const stats = useMemo(() => {
    const as = apps ?? []
    // A guest a node reports is not a server of its own here, but an enrolled one is.
    const ss = servers ?? []
    const ds = services ?? []
    const appDomains = as.flatMap((a) => a.domains ?? [])
    return {
      apps: as.length,
      appsRunning: as.filter((a) => a.status === "running" || a.imported_state === "running").length,
      appsFailed: as.filter((a) => a.status === "failed"),
      servers: ss.length,
      serversOnline: ss.filter((s) => s.online).length,
      serversOffline: ss.filter((s) => s.status === "enrolled" && !s.online),
      dbs: ds.length,
      dbsRunning: ds.filter((s) => s.status === "running").length,
      dbsFailed: ds.filter((s) => s.status === "failed"),
      domains: appDomains.length + proxies.length,
      domainsPending: appDomains.filter((d) => !d.verified).length + proxies.filter((p) => !p.verified).length,
    }
  }, [apps, servers, services, proxies])

  const failedDeploys = (feed ?? []).filter((d) => d.status === "failed").slice(0, 3)
  const attention = stats.appsFailed.length + stats.serversOffline.length + stats.dbsFailed.length + failedDeploys.length

  // CircleXO as a ready-to-deploy product or catalogue stack, when the team has one.
  const circlexo =
    products.find((p) => /circle\s*xo/i.test(`${p.name} ${p.title ?? ""}`)) ?? null
  const circlexoRecipe = recipes.find((r) => /circle\s*xo/i.test(`${r.id} ${r.name}`)) ?? null
  const stacks = recipes.filter((r) => r.kind === "stack")

  const who = me.user.email.split("@")[0]
  const hour = new Date().getHours()
  const greeting = t(hour < 12 ? "home2.morning" : hour < 18 ? "home2.afternoon" : "home2.evening", { name: who })
  const loading = apps === null || servers === null

  return (
    <PageBody
      eyebrow={team.name}
      title={<span dir="auto">{greeting}</span>}
      description={loading ? undefined : t("home2.lede", { apps: stats.apps, servers: stats.servers })}
      actions={
        <>
          {manage ? (
            <Button variant="outline" nativeButton={false} render={<Link href={`${base}/servers/new`} />}>
              <ServerIcon />
              {t("servers.add")}
            </Button>
          ) : null}
          <Button nativeButton={false} render={<Link href={`${base}/apps/new`} />}>
            <RocketIcon />
            {t("home2.deploy")}
          </Button>
        </>
      }
      bleed
    >
      {/* What is running, and is it healthy? */}
      <div className="grid grid-cols-2 gap-px border-b border-line bg-line lg:grid-cols-4">
        <SummaryCard
          href={`${base}/apps`}
          icon={<AppWindowIcon />}
          label={t("nav.apps")}
          value={loading ? "–" : stats.apps}
          tone={stats.appsFailed.length ? "warn" : stats.apps ? "ok" : undefined}
          detail={
            stats.appsFailed.length
              ? t("home2.failing", { n: stats.appsFailed.length })
              : t("home2.running", { n: stats.appsRunning })
          }
        />
        <SummaryCard
          href={`${base}/servers`}
          icon={<ServerIcon />}
          label={t("nav.servers")}
          value={loading ? "–" : stats.servers}
          tone={stats.serversOffline.length ? "warn" : stats.servers ? "ok" : undefined}
          detail={
            stats.serversOffline.length
              ? t("home2.offline", { n: stats.serversOffline.length })
              : t("home2.online", { n: stats.serversOnline })
          }
        />
        <SummaryCard
          href={`${base}/databases`}
          icon={<DatabaseIcon />}
          label={t("nav.databases")}
          value={services === null ? "–" : stats.dbs}
          tone={stats.dbsFailed.length ? "warn" : stats.dbs ? "ok" : undefined}
          detail={
            stats.dbsFailed.length ? t("home2.failing", { n: stats.dbsFailed.length }) : t("home2.running", { n: stats.dbsRunning })
          }
        />
        <SummaryCard
          href={`${base}/domains`}
          icon={<GlobeIcon />}
          label={t("nav.domains")}
          value={loading ? "–" : stats.domains}
          detail={stats.domainsPending ? t("home2.awaitingDns", { n: stats.domainsPending }) : t("home2.allVerified")}
        />
      </div>

      {/* What needs attention right now (only when something does). */}
      {attention > 0 ? (
        <section aria-labelledby="attention-h">
          <SectionTitle id="attention-h" title={t("home2.attention")} />
          <ul className="divide-y divide-line border-b border-line">
            {stats.appsFailed.map((a) => (
              <AttentionRow
                key={`a${a.id}`}
                href={`${base}/apps/${encodeURIComponent(a.name)}/deployments`}
                title={a.title || a.name}
                what={t("home2.appFailed", { server: a.server_name ?? "" })}
                action={t("home2.seeDeploy")}
              />
            ))}
            {stats.serversOffline.map((s) => (
              <AttentionRow
                key={`s${s.id}`}
                href={`${base}/servers/${s.id}`}
                title={s.name}
                ltr
                what={t("home2.serverOffline")}
                action={t("home2.openServer")}
              />
            ))}
            {stats.dbsFailed.map((d) => (
              <AttentionRow
                key={`d${d.id}`}
                href={`${base}/databases/${d.id}`}
                title={d.name}
                ltr
                what={d.error ?? t("home2.dbFailed")}
                action={t("home2.openDatabase")}
              />
            ))}
            {failedDeploys
              .filter((d) => !stats.appsFailed.some((a) => a.id === d.app.id))
              .map((d) => (
                <AttentionRow
                  key={`f${d.id}`}
                  href={`${base}/apps/${encodeURIComponent(d.app.name)}/deployments?d=${d.id}`}
                  title={`${d.app.name} #${d.number}`}
                  what={t("home2.deployFailed")}
                  action={t("home2.seeDeploy")}
                />
              ))}
          </ul>
        </section>
      ) : null}

      {/* What do you want to deploy? Only sources that work today. */}
      <section aria-labelledby="deploy-h">
        <SectionTitle id="deploy-h" title={t("home2.deployTitle")} hint={t("home2.deployHint")} />
        <div className="grid gap-px border-b border-line bg-line sm:grid-cols-2 xl:grid-cols-3">
          {circlexo ? (
            <SourceCard
              href={`${base}/products`}
              icon={<PackageIcon />}
              title={circlexo.title || circlexo.name}
              badge={t("home2.ready")}
              body={t("home2.productBody")}
            />
          ) : circlexoRecipe ? (
            <SourceCard
              href={`${base}/apps/new?stack=${encodeURIComponent(circlexoRecipe.id)}`}
              icon={<PackageIcon />}
              title={circlexoRecipe.name}
              badge={t("home2.ready")}
              body={circlexoRecipe.summary}
            />
          ) : null}
          <SourceCard href={`${base}/apps/new`} icon={<GithubIcon />} title={t("home2.fromGit")} body={t("home2.fromGitBody")} />
          {stacks.length ? (
            <SourceCard
              href={`${base}/apps/new?stack=${encodeURIComponent(stacks[0].id)}`}
              icon={<BoxesIcon />}
              title={t("home2.fromStack")}
              body={t("home2.fromStackBody", { n: stacks.length })}
            />
          ) : null}
          <SourceCard href={`${base}/templates`} icon={<LayoutTemplateIcon />} title={t("home2.fromTemplate")} body={t("home2.fromTemplateBody")} />
          {(servers ?? []).length ? (
            <SourceCard
              href={`${base}/servers/${(servers ?? []).find((s) => s.online)?.id ?? (servers ?? [])[0].id}/discovered`}
              icon={<DownloadIcon />}
              title={t("home2.fromServer")}
              body={t("home2.fromServerBody")}
            />
          ) : null}
          {manage ? (
            <SourceCard href={`${base}/servers/new`} icon={<ServerIcon />} title={t("home2.byo")} body={t("home2.byoBody")} />
          ) : null}
        </div>
      </section>

      {/* What shipped lately. */}
      <section aria-labelledby="recent-h">
        <SectionTitle
          id="recent-h"
          title={t("home.latestDeployments")}
          action={
            <Button variant="ghost" size="sm" nativeButton={false} render={<Link href={`${base}/deployments`} />}>
              {t("home2.viewAll")}
              <ArrowRightIcon className="rtl:-scale-x-100" />
            </Button>
          }
        />
        <div className="px-6 pt-4 pb-8">
          {feed === null ? (
            <LoadingRows rows={3} />
          ) : feed.length === 0 ? (
            <div className="flex flex-wrap items-center justify-between gap-3 border border-dashed border-line px-4 py-4 text-sm text-grid-muted">
              {t("home.noDeploysYet")}
              <Button size="sm" nativeButton={false} render={<Link href={`${base}/apps/new`} />}>
                <PlusIcon />
                {t("home2.deploy")}
              </Button>
            </div>
          ) : (
            <DeploymentsFeed items={feed} base={base} />
          )}
        </div>
      </section>
    </PageBody>
  )
}

function AttentionRow({ href, title, what, action, ltr }: { href: string; title: string; what: string; action: string; ltr?: boolean }) {
  return (
    <li>
      <Link href={href} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-6 py-3 text-sm hover:bg-grid-soft">
        <TriangleAlertIcon className="size-4 shrink-0 text-grid-danger" />
        <span dir={ltr ? "ltr" : "auto"} className="font-medium text-grid-fg">
          {title}
        </span>
        <span dir="auto" className="min-w-0 flex-1 truncate text-grid-muted">
          {what}
        </span>
        <span className="flex items-center gap-1 text-xs text-grid-action">
          {action}
          <ArrowRightIcon className="size-3.5 rtl:-scale-x-100" />
        </span>
      </Link>
    </li>
  )
}
