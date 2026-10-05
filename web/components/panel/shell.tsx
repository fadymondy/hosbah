"use client"

import {
  AppHeader,
  AppMain,
  AppShell as NasaqAppShell,
  Button,
  DropdownMenuItem,
  EmptyState,
  ImpersonationBanner,
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarHeader,
  SidebarTrigger,
  UserMenu,
  useSidebarCollapsed,
} from "@fadymondy/nasaq/web"
import Link from "next/link"
import { usePathname, useRouter } from "next/navigation"
import { createContext, useCallback, useContext, useEffect, useRef, useState, type CSSProperties, type ReactNode } from "react"
import { AppWindowIcon, SquareTerminalIcon, UserIcon } from "lucide-react"

import { brandStyle, panelApi, usePanelBrand, type PanelAccount, type PanelBrand } from "@/components/panel/auth"
import { PageBody } from "@/components/app-shell"
import { PageLoading } from "@/components/page-loading"
import { ServerMark } from "@/components/server-mark"
import { ServerProvider, useServerPage } from "@/components/server-tabs/context"
import { type ServerTab } from "@/components/server-tabs/header"
import { NavLink } from "@/components/shell/nav-link"
import { ShellScopeProvider } from "@/components/shell/scope"
import { SERVER_ITEMS } from "@/components/shell/scope-nav"
import { CardPieces, ErrorBand } from "@/components/states"
import { apiFetch, ApiError, type Me } from "@/lib/api"
import { useDocumentTitle, useI18n } from "@/lib/i18n"
import { SessionProvider } from "@/lib/session"
import { TeamBase, TeamGate } from "@/lib/team"
import { cn } from "@/lib/utils"

/** The member panel's pages under /{locale}/panel/{id}: the workloads first, then the tools. */
const PANEL_MAIN: readonly ServerTab[] = ["", "apps", "databases"]
const PANEL_TOOLS: readonly ServerTab[] = ["files", "sftp", "ssh", "terminal"]
/** Pages that are a bare list or tool: the panel gives them their title. */
const TITLED = new Set<string>(["apps", "databases", "files", "sftp", "ssh", "terminal"])

/** Who is looking at the panel, for its own pages (dashboard, account). */
export type PanelViewer =
  | { kind: "member"; panelId: string; email: string }
  | {
      kind: "account"
      panelId: string
      account: PanelAccount
      brand: PanelBrand
      signOut: () => Promise<void>
    }
  | { kind: "app"; panelId: string; email: string; appId: string }
const Viewer = createContext<PanelViewer | null>(null)
export function usePanelViewer(): PanelViewer | null {
  return useContext(Viewer)
}

/** A customer signed in through an integration's one-time panel link (MH-1153): one app, nothing else. */
type PanelAppSession = { email: string; team_id: string; team_slug: string; app_id: string; app_name: string; server_id: string }

type Who =
  | { kind: "member"; me: Me; team: string }
  | { kind: "account"; account: PanelAccount }
  | { kind: "app"; session: PanelAppSession }
  | { kind: "none" }

/**
 * A user's panel (MH-1141): a separate UI for one server user (an LXC, VM or plain server), branded with
 * the server's own name, mark and colour, nothing of Hosbah. Team members who can see the server come
 * in with their console session; customers sign in with a panel account at /panel/{id}/login. Nobody is
 * ever sent to the Hosbah sign-in from here.
 */
export function PanelShell({ panelId, children }: { panelId: string; children: ReactNode }) {
  const pathname = usePathname() ?? ""
  if (/\/panel\/[^/]+\/login\/?$/.test(pathname)) return <>{children}</>
  return <PanelGate panelId={panelId}>{children}</PanelGate>
}

/** Who is here: a console member of the server's team, else a panel account, else off to the panel's sign-in. */
function PanelGate({ panelId, children }: { panelId: string; children: ReactNode }) {
  const { locale } = useI18n()
  const router = useRouter()
  const pathname = usePathname() ?? ""
  const brand = usePanelBrand(panelId)
  const [who, setWho] = useState<Who | null>(null)

  const load = useCallback(async () => {
    try {
      const me = await apiFetch<Me>("/api/me")
      for (const team of me.teams) {
        try {
          await apiFetch(`/api/teams/${encodeURIComponent(team.slug)}/servers/${encodeURIComponent(panelId)}`)
          return setWho({ kind: "member", me, team: team.slug })
        } catch {
          // not in this team
        }
      }
    } catch {
      // no console session (or a panel account's): try an app link's, then the panel's own
    }
    try {
      const session = await apiFetch<PanelAppSession>("/api/me/panel-app")
      return setWho({ kind: "app", session })
    } catch {
      // not signed in through an app link
    }
    try {
      const { account } = await panelApi.me(panelId)
      return setWho({ kind: "account", account })
    } catch (e) {
      if (!(e instanceof ApiError) || e.status !== 401) return setWho({ kind: "none" })
    }
    setWho({ kind: "none" })
  }, [panelId])

  useEffect(() => {
    void load()
  }, [load])

  useEffect(() => {
    if (who?.kind === "none" && brand) router.replace(`/${locale}/panel/${panelId}/login?next=${encodeURIComponent(pathname)}`)
  }, [who, brand, locale, panelId, pathname, router])

  // A member needs no brand call: the server page itself carries the brand.
  if (!who) return <PageLoading />
  if (who.kind === "app") return <AppPanel panelId={panelId} brand={brand} session={who.session}>{children}</AppPanel>
  if (who.kind !== "member" && brand === null) return <NotFound />
  if (who.kind === "none" || (who.kind === "account" && !brand)) return <PageLoading />
  if (who.kind === "account")
    return brand ? (
      <AccountPanel panelId={panelId} brand={brand} account={who.account}>
        {children}
      </AccountPanel>
    ) : (
      <PageLoading />
    )

  const refresh = async () => load()
  return (
    <SessionProvider me={who.me} refresh={refresh}>
      <TeamGate slug={who.team} loading={<PageLoading />} notFound={<NotFound />} failed={() => <NotFound />}>
        <TeamBase base={`/${locale}/panel/${panelId}`}>
          <ServerProvider serverId={panelId}>
            <Frame panelId={panelId} team={who.team} email={who.me.user.email}>
              {children}
            </Frame>
          </ServerProvider>
        </TeamBase>
      </TeamGate>
    </SessionProvider>
  )
}

function NotFound() {
  const { t } = useI18n()
  return <EmptyState className="min-h-dvh" title={t("panel.notFound")} description={t("panel.notFoundHint")} />
}

type ChromeBrand = {
  name: string
  color?: string | null
  icon?: string | null
  logo_url?: string | null
}

/**
 * The panel's chrome, the same Nasaq AppShell as the console but with the server's brand: its mark and
 * name atop the sidebar, the customer's pages (dashboard, apps, databases, then the tools and the
 * account), the account menu at the foot. `banner` sits above everything (the impersonation bar when a
 * Hosbah member is looking at a customer's panel).
 */
function PanelChrome({
  panelId,
  brand,
  main,
  tools = [],
  nav,
  home,
  banner,
  account,
  children,
}: {
  panelId: string
  brand: ChromeBrand
  main: readonly ServerTab[]
  tools?: readonly ServerTab[]
  /** Extra links under the main ones (an app link's single app). */
  nav?: ReactNode
  /** Where the brand leads; the panel's dashboard by default. */
  home?: string
  banner?: ReactNode
  account: ReactNode
  children: ReactNode
}) {
  const { t, locale } = useI18n()
  const root = `/${locale}/panel/${panelId}`
  // The impersonation bar stays on top; the sidebar sticks right under it (Nasaq's shell offset).
  const bar = useRef<HTMLDivElement>(null)
  const [offset, setOffset] = useState(0)
  useEffect(() => {
    const el = bar.current
    if (!el) return setOffset(0)
    const ro = new ResizeObserver(() => setOffset(Math.round(el.getBoundingClientRect().height)))
    ro.observe(el)
    return () => ro.disconnect()
  }, [banner])
  const link = (tab: ServerTab) => {
    const Icon = SERVER_ITEMS[tab].icon
    return (
      <NavLink key={tab || "overview"} href={tab ? `${root}/${tab}` : root} exact={!tab} icon={<Icon />}>
        {t(`panel.tabs.${tab || "overview"}`)}
      </NavLink>
    )
  }
  return (
    <ShellScopeProvider>
      <div style={{ ...brandStyle(brand.color), "--nasaq-shell-offset": `${offset}px` } as CSSProperties} data-testid="panel">
        {banner ? (
          <div ref={bar} className="sticky top-0 z-40">
            {banner}
          </div>
        ) : null}
        <NasaqAppShell
          resizeLabel={t("shell.resizeNav")}
          sidebar={
            <Sidebar aria-label={brand.name}>
              <SidebarHeader>
                <SidebarBrand href={home ?? root} brand={brand} />
              </SidebarHeader>
              <SidebarContent>
                <SidebarGroup>
                  {main.map(link)}
                  {nav}
                </SidebarGroup>
                {tools.length ? <SidebarGroup label={t("panel.nav.tools")}>{tools.map(link)}</SidebarGroup> : null}
              </SidebarContent>
              <SidebarFooter>{account}</SidebarFooter>
            </Sidebar>
          }
        >
          <AppHeader>
            <SidebarTrigger label={t("shell.navigation")} />
            <span dir="auto" className="truncate text-sm font-medium text-foreground">
              {brand.name}
            </span>
          </AppHeader>
          <AppMain className="p-0">{children}</AppMain>
        </NasaqAppShell>
      </div>
    </ShellScopeProvider>
  )
}

function SidebarBrand({ href, brand }: { href: string; brand: ChromeBrand }) {
  const collapsed = useSidebarCollapsed()
  return (
    <Link
      href={href}
      className={cn(
        "flex h-control items-center gap-2 rounded-control outline-none focus-visible:outline-2 focus-visible:outline-nq-focus",
        collapsed ? "justify-center" : "px-1.5",
      )}
    >
      <ServerMark server={brand} size={6} />
      {collapsed ? null : (
        <span dir="auto" className="truncate text-sm font-semibold text-foreground">
          {brand.name}
        </span>
      )}
    </Link>
  )
}

/** The panel page under /panel/{id}/…: its first segment, and whether it is that section's own page. */
function usePanelPage(panelId: string): { tab: string; top: boolean } {
  const pathname = usePathname() ?? ""
  const rest = pathname.split(`/panel/${panelId}`)[1] ?? ""
  const parts = rest.split("/").filter(Boolean)
  return { tab: parts[0] ?? "", top: parts.length <= 1 }
}

/**
 * A Hosbah team member in a customer's panel: the panel as the customer sees it, under an impersonation
 * bar that says so and leads back to the console. No server cover or tabs: it is the customer's console.
 */
function Frame({ panelId, team, email, children }: { panelId: string; team: string; email: string; children: ReactNode }) {
  const { t, locale } = useI18n()
  const router = useRouter()
  const { server, missing, error } = useServerPage()
  const { tab, top } = usePanelPage(panelId)
  useDocumentTitle(server?.name ?? t("panel.title"))
  if (missing) return <NotFound />
  if (!server) return <PageLoading />

  async function signOutConsole() {
    await apiFetch("/api/auth/logout", { method: "POST" }).catch(() => undefined)
    router.replace(`/${locale}/login`)
  }
  const consoleHref = server.parent_server_id ? `/${locale}/t/${team}/servers/${server.parent_server_id}/users` : `/${locale}/t/${team}/servers/${panelId}`

  return (
    <Viewer.Provider value={{ kind: "member", panelId, email }}>
      <PanelChrome
        panelId={panelId}
        brand={server}
        main={PANEL_MAIN}
        tools={PANEL_TOOLS}
        banner={
          <ImpersonationBanner
            as={{ name: server.name }}
            hint={t("panel.impersonation.hint", { email })}
            onExit={() => router.push(consoleHref)}
            labels={{ exit: t("panel.impersonation.exit") }}
          />
        }
        account={<PanelUserMenu panelId={panelId} email={email} onSignOut={signOutConsole} />}
      >
        <ErrorBand message={error} />
        {/* The tab's own section title heads the page; a page title on top of it would say it twice. */}
        {top && TITLED.has(tab) ? (
          <CardPieces>{children}</CardPieces>
        ) : (
          children
        )}
      </PanelChrome>
    </Viewer.Provider>
  )
}

/**
 * A customer signed in with a panel account: the dashboard and their account (password, sign-out).
 * Their tools (apps, databases, files, terminal) are not yet served to panel sessions (MH-1146).
 */
function AccountPanel({ panelId, brand, account, children }: { panelId: string; brand: PanelBrand; account: PanelAccount; children: ReactNode }) {
  const { t, locale } = useI18n()
  const router = useRouter()
  const { tab } = usePanelPage(panelId)
  useDocumentTitle(brand.name)

  async function signOut() {
    await panelApi.logout(panelId).catch(() => undefined)
    router.replace(`/${locale}/panel/${panelId}/login`)
  }

  return (
    <Viewer.Provider value={{ kind: "account", panelId, account, brand, signOut }}>
      <PanelChrome panelId={panelId} brand={brand} main={[""]} account={<PanelUserMenu panelId={panelId} email={account.email} onSignOut={signOut} />}>
        {tab === "account" ? (
          children
        ) : (
          <PageBody title={t("panel.dashboard.title")} description={t("panel.dashboard.description", { name: brand.name })}>
            <EmptyState
              className="mx-auto max-w-xl py-16"
              icon={SquareTerminalIcon}
              title={t("panel.login.limitedTitle")}
              description={t("panel.login.limited")}
              actions={
                <Button variant="secondary" onClick={() => void signOut()}>
                  {t("panel.login.signOut")}
                </Button>
              }
            />
          </PageBody>
        )}
      </PanelChrome>
    </Viewer.Provider>
  )
}

/** The panel's own user menu: its Account page and sign-out, nothing of the Hosbah console. */
function PanelUserMenu({
  panelId,
  email,
  onSignOut,
  accountPage = true,
}: {
  panelId: string
  email: string
  onSignOut: () => void | Promise<void>
  accountPage?: boolean
}) {
  const { t, locale } = useI18n()
  const router = useRouter()
  return (
    <UserMenu user={{ name: email.split("@")[0] || email, email }} onSignOut={() => void onSignOut()} labels={{ signOut: t("panel.login.signOut") }}>
      {accountPage ? (
        <DropdownMenuItem onClick={() => router.push(`/${locale}/panel/${panelId}/account`)}>
          <UserIcon />
          {t("panel.tabs.account")}
        </DropdownMenuItem>
      ) : null}
    </UserMenu>
  )
}

/**
 * A customer who came in through an integration's one-time panel link (MH-1153): the same panel
 * chrome, holding their one app and nothing else. Every other panel page leads back to the app; the
 * API refuses the rest anyway. Signing out ends the session for good: the link worked once.
 */
function AppPanel({
  panelId,
  brand,
  session,
  children,
}: {
  panelId: string
  brand: PanelBrand | null | undefined
  session: PanelAppSession
  children: ReactNode
}) {
  const { locale } = useI18n()
  const router = useRouter()
  const pathname = usePathname() ?? ""
  const root = `/${locale}/panel/${session.server_id}`
  const home = `${root}/apps/${encodeURIComponent(session.app_id)}`
  const ours =
    session.server_id === panelId &&
    [session.app_id, session.app_name].some((ref) => {
      const p = `${root}/apps/${encodeURIComponent(ref)}`
      return pathname === p || pathname.startsWith(`${p}/`)
    })

  useEffect(() => {
    if (!ours) router.replace(home)
  }, [ours, home, router])

  async function signOut() {
    await apiFetch("/api/auth/logout", { method: "POST" }).catch(() => undefined)
    router.replace(`/${locale}/link-expired?out=1`)
  }

  if (!ours || brand === undefined) return <PageLoading />
  return (
    <TeamGate slug={session.team_slug} loading={<PageLoading />} notFound={<NotFound />} failed={() => <NotFound />}>
      <TeamBase base={root}>
        <Viewer.Provider value={{ kind: "app", panelId, email: session.email, appId: session.app_id }}>
          <PanelChrome
            panelId={panelId}
            brand={brand ?? { name: session.app_name }}
            main={[]}
            home={home}
            nav={
              <NavLink href={home} also={[`${root}/apps/${encodeURIComponent(session.app_name)}`]} icon={<AppWindowIcon />}>
                {session.app_name}
              </NavLink>
            }
            account={<PanelUserMenu panelId={panelId} email={session.email} onSignOut={signOut} accountPage={false} />}
          >
            {children}
          </PanelChrome>
        </Viewer.Provider>
      </TeamBase>
    </TeamGate>
  )
}
