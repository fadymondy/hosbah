"use client"

import Link from "next/link"
import { usePathname, useRouter } from "next/navigation"
import { useCallback, useEffect, useState, type ReactNode } from "react"
import {
  BellIcon,
  CheckIcon,
  ChevronsUpDownIcon,
  CircleDollarSignIcon,
  KeyRoundIcon,
  LayoutGridIcon,
  LayoutTemplateIcon,
  LockKeyholeIcon,
  MegaphoneIcon,
  TriangleAlertIcon,
  LogOutIcon,
  MailPlusIcon,
  MenuIcon,
  PanelLeftCloseIcon,
  PanelLeftOpenIcon,
  MessageSquareWarningIcon,
  PackageIcon,
  PlusIcon,
  ScrollTextIcon,
  ServerIcon,
  SettingsIcon,
  ShieldCheckIcon,
  ShieldIcon,
  SlidersHorizontalIcon,
  TerminalSquareIcon,
  UsersRoundIcon,
  WorkflowIcon,
  UserIcon,
  UsersIcon,
} from "lucide-react"

import { ThemedCubeMark } from "@/components/cube-mark"
import { MahaamFeedback } from "@/components/feedback/mahaam-feedback"
import { ReportProblem, requestReport } from "@/components/feedback/report-problem"
import { LanguageMenu, ThemeToggle } from "@/components/preferences"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { GroupLabel, NavLink } from "@/components/shell/nav-link"
import { ShellScopeProvider } from "@/components/shell/scope"
import { ScopeCrumbs, ServerNav, SiteNav, useScope } from "@/components/shell/scope-nav"
import { Sheet, SheetContent, SheetTitle } from "@/components/ui/sheet"
import { apiFetch } from "@/lib/api"
import type { Notification } from "@/lib/apps"
import { useFormatDate, useI18n } from "@/lib/i18n"
import { useSession } from "@/lib/session"
import { usePublicSettings } from "@/lib/platform"
import { useOptionalTeam } from "@/lib/team"
import { cn } from "@/lib/utils"

/**
 * The app shell, in the same structure as Mahaam: the grid's sticky sidebar (brand, then the
 * navigation) and a slim top bar (team switcher at the start; report, notifications, language,
 * theme and account at the end). Below md the navigation moves into a slide-out sheet.
 * `title` is kept for callers; the page header (PageBody) carries the visible title.
 */
export function AppShell({ children }: { children: ReactNode; title?: ReactNode }) {
  const { t, locale } = useI18n()
  const [navOpen, setNavOpen] = useState(false)
  // The desktop sidebar can be minimised to its icons; the choice is kept on this device (Ctrl/⌘+B toggles).
  const [collapsed, setCollapsed] = useState(false)
  useEffect(() => {
    setCollapsed(localStorage.getItem("hosbah.sidebar") === "min")
    const onKey = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "b") {
        e.preventDefault()
        toggleNav()
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  function toggleNav() {
    setCollapsed((c) => {
      localStorage.setItem("hosbah.sidebar", c ? "full" : "min")
      return !c
    })
  }

  return (
    <ShellScopeProvider>
      <div className="grid-shell" data-collapsed={collapsed}>
        <aside
          data-collapsed={collapsed}
          className="group/nav grid-shell-nav sticky top-0 hidden h-dvh flex-col overflow-x-hidden overflow-y-auto [scrollbar-width:none] md:flex [&::-webkit-scrollbar]:hidden"
        >
          <div className="flex h-14 shrink-0 items-center border-b border-line px-4 group-data-[collapsed=true]/nav:justify-center group-data-[collapsed=true]/nav:px-0">
            <Brand />
          </div>
          <ShellNav />
        </aside>

        <div className="flex min-w-0 flex-col">
          <header className="sticky top-0 z-30 flex h-14 items-center justify-between gap-2 border-b border-line bg-background px-3 sm:px-4">
            <div className="flex min-w-0 items-center gap-1">
              <Button
                variant="ghost"
                size="icon-sm"
                className="md:hidden"
                aria-label={t("shell.navigation")}
                onClick={() => setNavOpen(true)}
              >
                <MenuIcon />
              </Button>
              <Button
                variant="ghost"
                size="icon-sm"
                className="hidden md:inline-flex rtl:[&_svg]:-scale-x-100"
                aria-label={collapsed ? t("shell.expandNav") : t("shell.collapseNav")}
                aria-pressed={collapsed}
                title={`${collapsed ? t("shell.expandNav") : t("shell.collapseNav")} (Ctrl+B)`}
                onClick={toggleNav}
              >
                {collapsed ? <PanelLeftOpenIcon /> : <PanelLeftCloseIcon />}
              </Button>
              <TeamSwitcher />
              <ScopeCrumbs />
            </div>
            <div className="flex items-center gap-1">
              <ReportProblem />
              <NotificationsBell />
              <LanguageMenu />
              <ThemeToggle />
              <UserMenu />
            </div>
          </header>
          <Announcement />
          <main className="flex min-w-0 flex-1 flex-col">{children}</main>
        </div>
        {/* Mahaam Feedback SDK, for signed-in people only: the shell only renders behind the session. */}
        <MahaamFeedback />

        <Sheet open={navOpen} onOpenChange={setNavOpen}>
          <SheetContent side={locale === "ar" ? "right" : "left"} className="w-72 gap-0 p-0">
            <SheetTitle className="flex h-14 items-center border-b border-line px-4">
              <Brand />
            </SheetTitle>
            <ShellNav onNavigate={() => setNavOpen(false)} />
          </SheetContent>
        </Sheet>
      </div>
    </ShellScopeProvider>
  )
}

/** The platform announcement (admin → platform settings), under the top bar on every signed-in page. */
function Announcement() {
  const settings = usePublicSettings()
  const text = settings?.announcement?.trim()
  if (!text) return null
  const warn = settings?.announcement_level === "warning"
  return (
    <div
      role="status"
      className={cn(
        "flex items-start gap-3 border-b border-line px-6 py-3 text-sm",
        warn ? "bg-[color-mix(in_oklab,var(--grid-warn)_14%,transparent)] text-grid-fg" : "bg-grid-card text-grid-fg",
      )}
    >
      {warn ? (
        <TriangleAlertIcon className="mt-0.5 size-4 shrink-0 text-grid-warn" aria-hidden />
      ) : (
        <MegaphoneIcon className="mt-0.5 size-4 shrink-0 text-grid-brand" aria-hidden />
      )}
      <p dir="auto" className="whitespace-pre-line">
        {text}
      </p>
    </div>
  )
}

function Brand() {
  const { locale, t } = useI18n()
  return (
    <Link href={`/${locale}`} className="flex items-center gap-2.5" aria-label={t("common.brand")}>
      <ThemedCubeMark size={24} />
      <span className="text-[15px] font-medium group-data-[collapsed=true]/nav:hidden">{t("common.brand")}</span>
    </Link>
  )
}

/** The team's sections, then its settings, then the platform (admins only). Inside /admin the
 * sidebar is the platform's own: its sections, then its settings. Inside a server or a site the
 * sidebar is that level's own sections, with the way back up at its top. */
function ShellNav({ onNavigate }: { onNavigate?: () => void }) {
  const { t, locale } = useI18n()
  const teamCtx = useOptionalTeam()
  const { me } = useSession()
  const pathname = usePathname() ?? ""
  const scope = useScope()
  const L = (props: { href: string; icon: ReactNode; children: ReactNode; exact?: boolean }) => (
    <NavLink {...props} onNavigate={onNavigate} />
  )
  const base = teamCtx ? `/${locale}/t/${teamCtx.team.slug}` : null
  const admin = `/${locale}/admin`

  if (me.user.platform_admin && (pathname === admin || pathname.startsWith(admin + "/"))) {
    return (
      <nav className="flex flex-col py-3" aria-label={t("shell.navigation")}>
        <L href={admin} exact icon={<LayoutGridIcon />}>
          {t("admin.nav.overview")}
        </L>
        <L href={`${admin}/users`} icon={<UsersIcon />}>
          {t("admin.nav.users")}
        </L>
        <L href={`${admin}/teams`} icon={<UsersRoundIcon />}>
          {t("admin.nav.teams")}
        </L>
        <L href={`${admin}/servers`} icon={<ServerIcon />}>
          {t("admin.nav.servers")}
        </L>
        <L href={`${admin}/operations`} icon={<WorkflowIcon />}>
          {t("admin.nav.operations")}
        </L>
        <L href={`${admin}/audit`} icon={<ScrollTextIcon />}>
          {t("admin.nav.audit")}
        </L>
        <GroupLabel>{t("nav.settings")}</GroupLabel>
        <L href={`${admin}/settings`} icon={<SlidersHorizontalIcon />}>
          {t("admin.nav.settings")}
        </L>
        <L href={`${admin}/tokens`} icon={<KeyRoundIcon />}>
          {t("admin.nav.tokens")}
        </L>
      </nav>
    )
  }

  if (scope.kind === "server") return <ServerNav serverId={scope.serverId} onNavigate={onNavigate} />
  if (scope.kind === "site") return <SiteNav site={scope.site} onNavigate={onNavigate} />

  return (
    <nav className="flex flex-col py-3" aria-label={t("shell.navigation")}>
      {base ? (
        <>
          <L href={base} exact icon={<LayoutGridIcon />}>
            {t("nav.overview")}
          </L>
          <L href={`${base}/shared-services`} icon={<WorkflowIcon />}>
            {t("nav.sharedServices")}
          </L>
          <L href={`${base}/scripts`} icon={<TerminalSquareIcon />}>
            {t("nav.scripts")}
          </L>
          <L href={`${base}/templates`} icon={<LayoutTemplateIcon />}>
            {t("nav.templates")}
          </L>
          <L href={`${base}/products`} icon={<PackageIcon />}>
            {t("nav.products")}
          </L>
          <L href={`${base}/members`} icon={<UsersIcon />}>
            {t("nav.members")}
          </L>
          <L href={`${base}/invitations`} icon={<MailPlusIcon />}>
            {t("nav.invitations")}
          </L>
          <L href={`${base}/approvals`} icon={<ShieldCheckIcon />}>
            {t("nav.approvals")}
          </L>
          <L href={`${base}/costs`} icon={<CircleDollarSignIcon />}>
            {t("nav.costs")}
          </L>
          <L href={`${base}/audit`} icon={<ScrollTextIcon />}>
            {t("nav.audit")}
          </L>
          <GroupLabel>{t("nav.settings")}</GroupLabel>
          <L href={`${base}/tokens`} icon={<KeyRoundIcon />}>
            {t("nav.tokens")}
          </L>
          {/* Credentials are owner/admin only (the API answers 403 to anyone else). */}
          {teamCtx?.manage ? (
            <L href={`${base}/credentials`} icon={<LockKeyholeIcon />}>
              {t("nav.credentials")}
            </L>
          ) : null}
          <L href={`${base}/settings`} icon={<SettingsIcon />}>
            {t("nav.teamSettings")}
          </L>
        </>
      ) : null}
      {me.user.platform_admin ? (
        <>
          <GroupLabel>{t("nav.platform")}</GroupLabel>
          <L href={`/${locale}/admin`} icon={<ShieldIcon />}>
            {t("nav.admin")}
          </L>
        </>
      ) : null}
    </nav>
  )
}

/** The current team in the top bar, as Mahaam's workspace switcher: initial tile, name, chevrons. */
function TeamSwitcher() {
  const { t, locale } = useI18n()
  const { me } = useSession()
  const teamCtx = useOptionalTeam()
  const router = useRouter()
  const pathname = usePathname() ?? ""
  const active = teamCtx?.team

  /** Same section in the other team (members → members), falling back to its overview. */
  function hrefFor(slug: string) {
    if (!active) return `/${locale}/t/${slug}`
    const prefix = `/${locale}/t/${active.slug}`
    return pathname.startsWith(prefix) ? `/${locale}/t/${slug}${pathname.slice(prefix.length)}` : `/${locale}/t/${slug}`
  }

  const name = active?.name ?? t("shell.chooseTeam")
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <Button
            variant="ghost"
            className="h-9 max-w-[9rem] justify-between gap-2 px-2.5 sm:max-w-[16rem]"
            aria-label={t("shell.switchTeam")}
          />
        }
      >
        <span className="flex size-6 shrink-0 items-center justify-center border border-line bg-grid-card text-xs font-medium text-grid-brand">
          {active ? name.slice(0, 1).toUpperCase() : <UsersRoundIcon className="size-3.5" />}
        </span>
        <span className="hidden truncate text-sm font-medium sm:inline">{name}</span>
        <ChevronsUpDownIcon className="text-grid-muted" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="min-w-64">
        <DropdownMenuGroup>
          <DropdownMenuLabel>{t("shell.teams")}</DropdownMenuLabel>
          {me.teams.map((team) => (
            <DropdownMenuItem key={team.id} onClick={() => router.push(hrefFor(team.slug))}>
              <span className="min-w-0 flex-1 truncate">{team.name}</span>
              <span className="text-xs text-grid-muted">{t(`roles.${team.role}`)}</span>
              {team.slug === active?.slug ? <CheckIcon aria-label={t("shell.current")} /> : null}
            </DropdownMenuItem>
          ))}
        </DropdownMenuGroup>
        <DropdownMenuSeparator />
        <DropdownMenuItem onClick={() => router.push(`/${locale}/onboarding`)}>
          <PlusIcon />
          {t("shell.createTeam")}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function UserMenu() {
  const { t, locale } = useI18n()
  const { me } = useSession()
  const router = useRouter()
  const [signingOut, setSigningOut] = useState(false)

  async function signOut() {
    setSigningOut(true)
    try {
      await apiFetch("/api/auth/logout", { method: "POST" })
    } catch {
      // Signed out or not, the next page asks the API who we are.
    }
    router.replace(`/${locale}/login`)
  }

  return (
    <DropdownMenu>
      <DropdownMenuTrigger render={<Button variant="ghost" size="icon-sm" aria-label={t("shell.account")} />}>
        <UserIcon />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-56">
        <DropdownMenuGroup>
          <DropdownMenuLabel>
            <span className="grid-micro block">{t("shell.signedInAs")}</span>
            <span dir="ltr" className="mt-1 block truncate text-sm text-grid-fg">
              {me.user.email}
            </span>
          </DropdownMenuLabel>
        </DropdownMenuGroup>
        <DropdownMenuSeparator />
        <DropdownMenuItem onClick={() => router.push(`/${locale}/account`)}>
          <UserIcon />
          {t("shell.accountAnd2fa")}
        </DropdownMenuItem>
        <DropdownMenuItem onClick={requestReport}>
          <MessageSquareWarningIcon />
          {t("feedback.launcher")}
        </DropdownMenuItem>
        <DropdownMenuItem variant="destructive" disabled={signingOut} onClick={signOut}>
          <LogOutIcon />
          {t("shell.signOut")}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

/** Team notifications (failed deploys…): a bell with the unread count; opening it marks them read. */
function NotificationsBell() {
  const { t, locale } = useI18n()
  const teamCtx = useOptionalTeam()
  const router = useRouter()
  const fmt = useFormatDate()
  const slug = teamCtx?.team.slug
  const [items, setItems] = useState<Notification[]>([])
  const [unread, setUnread] = useState(0)

  const load = useCallback(async () => {
    if (!slug) return
    try {
      const r = await apiFetch<{ notifications: Notification[]; unread: number }>(`/api/teams/${slug}/notifications`)
      setItems(r.notifications ?? [])
      setUnread(r.unread ?? 0)
    } catch {
      // The bell stays quiet if notifications cannot be read.
    }
  }, [slug])

  useEffect(() => {
    void load()
    const id = setInterval(load, 30_000)
    return () => clearInterval(id)
  }, [load])

  if (!slug) return null

  async function markRead() {
    if (unread === 0) return
    try {
      await apiFetch(`/api/teams/${slug}/notifications/read`, { method: "POST" })
      setUnread(0)
    } catch {
      // Stays unread; the next poll shows the truth.
    }
  }

  function open(n: Notification) {
    if (n.subject_type === "app" && n.subject_id) router.push(`/${locale}/t/${slug}/apps/${n.subject_id}`)
  }

  return (
    <DropdownMenu onOpenChange={(o) => (o ? void markRead() : void load())}>
      <DropdownMenuTrigger
        render={
          <button
            type="button"
            className="relative inline-flex size-8 items-center justify-center hover:bg-grid-soft aria-expanded:bg-grid-soft"
            aria-label={unread ? t("notifications.titleUnread", { count: unread }) : t("notifications.title")}
            data-testid="notifications"
            data-unread={unread}
          />
        }
      >
        <BellIcon className="size-4" />
        {unread > 0 ? (
          <span className="absolute -end-0.5 -top-0.5 min-w-4 bg-primary px-1 text-center font-mono text-[11px] leading-4 text-primary-foreground tabular-nums">
            {unread > 99 ? "99+" : unread}
          </span>
        ) : null}
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" sideOffset={4} className="w-80">
        <DropdownMenuGroup>
          <DropdownMenuLabel className="text-xs text-muted-foreground">{t("notifications.title")}</DropdownMenuLabel>
          {items.length === 0 ? (
            <p className="px-2 py-3 text-sm text-muted-foreground">{t("notifications.empty")}</p>
          ) : (
            items.slice(0, 20).map((n) => (
              <DropdownMenuItem key={n.id} onClick={() => open(n)} className="flex-col items-start gap-0.5">
                <span className="flex w-full items-center gap-2">
                  {!n.read_at ? <span aria-hidden className="size-1.5 shrink-0 rounded-full bg-primary" /> : null}
                  <span className="truncate font-medium" dir="auto">
                    {n.title}
                  </span>
                </span>
                {n.body ? (
                  <span className="line-clamp-2 text-xs text-muted-foreground" dir="auto">
                    {n.body}
                  </span>
                ) : null}
                <span className="text-xs text-muted-foreground" dir="ltr">
                  {fmt(n.created_at)}
                </span>
              </DropdownMenuItem>
            ))
          )}
        </DropdownMenuGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

/** The page header, as Mahaam's SectionHeader: a micro label (the team by default), the title,
 * a description and the page's action at the far end. Content follows below a hairline. */
export function PageBody({
  title,
  description,
  eyebrow,
  actions,
  bleed = false,
  children,
}: {
  title: ReactNode
  description?: ReactNode
  eyebrow?: ReactNode
  actions?: ReactNode
  /** Full-bleed content: sections draw their own hairlines edge to edge (no padded column). */
  bleed?: boolean
  children: ReactNode
}) {
  const teamCtx = useOptionalTeam()
  const micro = eyebrow ?? teamCtx?.team.name
  return (
    <div className="flex flex-col">
      <div className="flex flex-wrap items-end justify-between gap-4 border-b border-line px-6 py-6">
        <div className="min-w-0 space-y-1.5">
          {micro ? <p className="grid-micro">{micro}</p> : null}
          <h1 className="grid-title text-2xl">{title}</h1>
          {description ? <p className="grid-body max-w-2xl text-sm">{description}</p> : null}
        </div>
        {actions ? <div className="flex flex-wrap items-center gap-2">{actions}</div> : null}
      </div>
      {bleed ? children : <div className="flex flex-col gap-6 px-6 py-6">{children}</div>}
    </div>
  )
}
