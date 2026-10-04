"use client"

import {
  AppHeader,
  AppMain,
  AppShell as NasaqAppShell,
  Button,
  DropdownMenuItem,
  ProductMark,
  SectionHeader,
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarHeader,
  SidebarTrigger,
  UserMenu,
  WorkspaceSwitcher,
} from "@fadymondy/nasaq/web"
import Link from "next/link"
import { usePathname, useRouter } from "next/navigation"
import { useCallback, useEffect, useRef, useState, type ReactNode } from "react"
import {
  AppWindowIcon,
  ArchiveIcon,
  DatabaseIcon,
  HardDriveIcon,
  HouseIcon,
  NetworkIcon,
  RocketIcon,
  BellIcon,
  CircleDollarSignIcon,
  GlobeIcon,
  KeyRoundIcon,
  LayoutGridIcon,
  LayoutTemplateIcon,
  LockKeyholeIcon,
  MailIcon,
  MegaphoneIcon,
  TriangleAlertIcon,
  MailPlusIcon,
  MessageSquareWarningIcon,
  PackageIcon,
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

import { MahaamFeedback } from "@/components/feedback/mahaam-feedback"
import { ReportProblem, requestReport } from "@/components/feedback/report-problem"
import { LanguageMenu, ThemeToggle } from "@/components/preferences"
import { GlobalSearch } from "@/components/shell/global-search"
import { NavLink } from "@/components/shell/nav-link"
import { SECURITY_PAGES } from "@/components/shell/sub-nav"
import { ShellScopeProvider } from "@/components/shell/scope"
import { ScopeCrumbs, ServerNav, SiteNav, useScope } from "@/components/shell/scope-nav"

import { apiFetch } from "@/lib/api"
import type { Notification } from "@/lib/apps"
import { useFormatDate, useI18n } from "@/lib/i18n"
import { useEventStream } from "@/lib/servers"
import { useSession } from "@/lib/session"
import { usePublicSettings } from "@/lib/platform"
import { useOptionalTeam } from "@/lib/team"
import { cn } from "@/lib/utils"

/**
 * The signed-in shell on Nasaq's AppShell: the sidebar (team switcher, then the navigation, then
 * the account menu) and a slim header (sidebar trigger, brand and scope crumbs at the start;
 * report, notifications, language and theme at the end). The rail, its width and Ctrl/Cmd+B are
 * Nasaq's; below md the sidebar is its sheet. `title` is kept for callers; the page header
 * (PageBody) carries the visible title.
 */
export function AppShell({ children }: { children: ReactNode; title?: ReactNode }) {
  const { t } = useI18n()
  return (
    <ShellScopeProvider>
      <NasaqAppShell sidebar={<AppSidebar />} resizeLabel={t("shell.resizeNav")}>
        <AppHeader>
          <SidebarTrigger label={t("shell.navigation")} />
          <Brand />
          <ScopeCrumbs />
          <div className="ms-auto flex items-center gap-1">
            <GlobalSearch />
            <ReportProblem />
            <NotificationsBell />
            <LanguageMenu />
            <ThemeToggle />
          </div>
        </AppHeader>
        <Announcement />
        <AppMain className="p-0">{children}</AppMain>
        {/* Mahaam Feedback SDK, for signed-in people only: the shell only renders behind the session. */}
        <MahaamFeedback />
      </NasaqAppShell>
    </ShellScopeProvider>
  )
}

function AppSidebar() {
  const { t } = useI18n()
  return (
    <Sidebar aria-label={t("shell.navigation")}>
      <SidebarHeader>
        <TeamSwitcher />
      </SidebarHeader>
      <SidebarContent>
        <ShellNav />
      </SidebarContent>
      <SidebarFooter>
        <AccountMenu />
      </SidebarFooter>
    </Sidebar>
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
        "flex items-start gap-3 border-b border-border px-6 py-3 text-body-sm",
        warn ? "bg-[color-mix(in_oklab,var(--nq-warning)_14%,transparent)] text-foreground" : "bg-card text-foreground",
      )}
    >
      {warn ? (
        <TriangleAlertIcon className="mt-0.5 size-4 shrink-0 text-nq-warning-text" aria-hidden />
      ) : (
        <MegaphoneIcon className="mt-0.5 size-4 shrink-0 text-nq-brand" aria-hidden />
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
    <Link href={`/${locale}`} className="flex items-center gap-2" aria-label={t("common.brand")}>
      <ProductMark brand="hosbah" size={22} title="" />
      <span className="hidden text-label sm:inline">{t("common.brand")}</span>
    </Link>
  )
}

/** The team's sections, then its settings, then the platform (admins only). Inside /admin the
 * sidebar is the platform's own: its sections, then its settings. Inside a server or a site the
 * sidebar is that level's own sections, with the way back up at its top. */
function ShellNav() {
  const { t, locale } = useI18n()
  const teamCtx = useOptionalTeam()
  const { me } = useSession()
  const pathname = usePathname() ?? ""
  const scope = useScope()
  const L = (props: { href: string; icon: ReactNode; children: string; exact?: boolean; also?: string[] }) => <NavLink {...props} />
  const base = teamCtx ? `/${locale}/t/${teamCtx.team.slug}` : null
  const admin = `/${locale}/admin`

  if (me.user.platform_admin && (pathname === admin || pathname.startsWith(admin + "/"))) {
    return (
      <>
        <SidebarGroup>
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
        </SidebarGroup>
        <SidebarGroup label={t("nav.settings")}>
          <L href={`${admin}/settings`} icon={<SlidersHorizontalIcon />}>
            {t("admin.nav.settings")}
          </L>
          <L href={`${admin}/tokens`} icon={<KeyRoundIcon />}>
            {t("admin.nav.tokens")}
          </L>
        </SidebarGroup>
      </>
    )
  }

  if (scope.kind === "server") return <ServerNav serverId={scope.serverId} />
  if (scope.kind === "site") return <SiteNav site={scope.site} />

  return (
    <>
      {base ? (
        <>
          <SidebarGroup>
            <L href={base} exact icon={<HouseIcon />}>
              {t("nav.home")}
            </L>
            <L href={`${base}/apps`} icon={<AppWindowIcon />}>
              {t("nav.apps")}
            </L>
            <L href={`${base}/deployments`} icon={<RocketIcon />}>
              {t("nav.deployments")}
            </L>
            <L href={`${base}/servers`} icon={<ServerIcon />}>
              {t("nav.servers")}
            </L>
            <L href={`${base}/databases`} icon={<DatabaseIcon />}>
              {t("nav.databases")}
            </L>
            <L href={`${base}/domains`} icon={<GlobeIcon />} also={[`${base}/proxy-hosts`]}>
              {t("nav.domains")}
            </L>
          </SidebarGroup>
          <SidebarGroup label={t("nav.groups.infrastructure")}>
            <L href={`${base}/dns`} icon={<NetworkIcon />}>
              {t("nav.networking")}
            </L>
            <L href={`${base}/storage`} icon={<HardDriveIcon />}>
              {t("nav.storage")}
            </L>
            <L href={`${base}/backups`} icon={<ArchiveIcon />}>
              {t("nav.backups")}
            </L>
            <L href={`${base}/shared-services`} icon={<WorkflowIcon />}>
              {t("nav.sharedServices")}
            </L>
            <L href={`${base}/mail`} icon={<MailIcon />}>
              {t("nav.mail")}
            </L>
          </SidebarGroup>
          <SidebarGroup label={t("nav.groups.automation")}>
            <L href={`${base}/templates`} icon={<LayoutTemplateIcon />}>
              {t("nav.templates")}
            </L>
            <L href={`${base}/scripts`} icon={<TerminalSquareIcon />}>
              {t("nav.scripts")}
            </L>
            <L href={`${base}/products`} icon={<PackageIcon />}>
              {t("nav.products")}
            </L>
          </SidebarGroup>
          <SidebarGroup label={t("nav.team")}>
            <L href={`${base}/members`} icon={<UsersIcon />} also={[`${base}/invitations`]}>
              {t("nav.members")}
            </L>
            <L href={`${base}/approvals`} icon={<ShieldCheckIcon />}>
              {t("nav.approvals")}
            </L>
          </SidebarGroup>
          <SidebarGroup label={t("nav.settings")}>
            <L href={`${base}/settings`} icon={<SettingsIcon />} also={[`${base}/audit`]}>
              {t("nav.general")}
            </L>
            <L href={`${base}/tokens`} icon={<LockKeyholeIcon />} also={SECURITY_PAGES.map((p) => `${base}/${p}`)}>
              {t("nav.security")}
            </L>
            <L href={`${base}/costs`} icon={<CircleDollarSignIcon />}>
              {t("nav.billing")}
            </L>
          </SidebarGroup>
        </>
      ) : null}
      {me.user.platform_admin ? (
        <SidebarGroup label={t("nav.platform")}>
          <L href={`/${locale}/admin`} icon={<ShieldIcon />}>
            {t("nav.admin")}
          </L>
        </SidebarGroup>
      ) : null}
    </>
  )
}

/** The current team, as Nasaq's workspace switcher (first in the sidebar header). */
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

  return (
    <WorkspaceSwitcher
      workspaces={me.teams.map((team) => ({ id: team.slug, name: team.name, description: t(`roles.${team.role}`) }))}
      value={active?.slug ?? ""}
      onValueChange={(slug) => router.push(hrefFor(slug))}
      onCreate={() => router.push(`/${locale}/onboarding`)}
      labels={{ heading: t("shell.teams"), create: t("shell.createTeam") }}
    />
  )
}

/** The account menu at the foot of the sidebar: identity, account, report a problem, sign out. */
function AccountMenu() {
  const { t, locale } = useI18n()
  const { me } = useSession()
  const router = useRouter()

  async function signOut() {
    try {
      await apiFetch("/api/auth/logout", { method: "POST" })
    } catch {
      // Signed out or not, the next page asks the API who we are.
    }
    router.replace(`/${locale}/login`)
  }

  return (
    <UserMenu
      user={{ name: me.user.email.split("@")[0] || me.user.email, email: me.user.email }}
      preferences={false}
      onSignOut={signOut}
      labels={{ signOut: t("shell.signOut") }}
    >
      <DropdownMenuItem onClick={() => router.push(`/${locale}/account`)}>
        <UserIcon />
        {t("shell.accountAnd2fa")}
      </DropdownMenuItem>
      <DropdownMenuItem onClick={requestReport}>
        <MessageSquareWarningIcon />
        {t("feedback.launcher")}
      </DropdownMenuItem>
    </UserMenu>
  )
}

type NotificationsPayload = { notifications: Notification[]; unread: number }

/**
 * Team notifications (failed deploys…): a bell with the unread count, opening a side-over sheet
 * (inline-end; mirrored in RTL) that lists them and marks them read. Kept live by the team's own
 * SSE stream (/api/teams/{team}/notifications/stream — internal/ops's broker wakes it the moment a
 * deploy or monitor writes a notification; it also polls every 15s as a fallback, same as every
 * other stream in this app). A new, unread notification pops a desktop Notification when the tab
 * is hidden and permission is granted.
 */
function NotificationsBell() {
  const { t, locale } = useI18n()
  const teamCtx = useOptionalTeam()
  const router = useRouter()
  const fmt = useFormatDate()
  const slug = teamCtx?.team.slug
  const [items, setItems] = useState<Notification[]>([])
  const [unread, setUnread] = useState(0)
  const [sheetOpen, setSheetOpen] = useState(false)
  const [permission, setPermission] = useState<NotificationPermission | "unsupported">("default")
  const seenRef = useRef<Set<string> | null>(null)

  useEffect(() => {
    if (typeof window === "undefined" || !("Notification" in window)) {
      setPermission("unsupported")
      return
    }
    setPermission(Notification.permission)
  }, [])

  const apply = useCallback((data: NotificationsPayload) => {
    const list = data.notifications ?? []
    setItems(list)
    setUnread(data.unread ?? 0)
    const seen = seenRef.current
    if (seen && typeof document !== "undefined" && document.hidden && typeof window !== "undefined" && "Notification" in window && Notification.permission === "granted") {
      for (const n of list) {
        if (n.read_at || seen.has(n.id)) continue
        try {
          const desktop = new Notification(n.title, { body: n.body ?? undefined, tag: n.id })
          desktop.onclick = () => {
            window.focus()
            if (n.subject_type === "app" && n.subject_id && slug)
              router.push(`/${locale}/t/${slug}/apps/${n.subject_id}${n.kind === "adoptable" ? "?adopt=1" : ""}`)
          }
        } catch {
          // Desktop notifications can fail quietly (permission revoked mid-session, etc.).
        }
      }
    }
    seenRef.current = new Set(list.map((n) => n.id))
  }, [locale, router, slug])

  useEffect(() => {
    if (!slug) return
    apiFetch<NotificationsPayload>(`/api/teams/${slug}/notifications`).then(apply).catch(() => {
      // The bell stays quiet if notifications cannot be read; the stream retries on its own.
    })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [slug])

  useEventStream(slug ? `/api/teams/${slug}/notifications/stream` : null, {
    notifications: (d) => apply(d as NotificationsPayload),
  })

  if (!slug) return null

  async function markRead() {
    if (unread === 0) return
    try {
      await apiFetch(`/api/teams/${slug}/notifications/read`, { method: "POST" })
      setUnread(0)
    } catch {
      // Stays unread; the next stream frame shows the truth.
    }
  }

  function requestDesktop() {
    if (typeof window === "undefined" || !("Notification" in window)) return
    void Notification.requestPermission().then(setPermission)
  }

  function open(n: Notification) {
    if (n.subject_type === "app" && n.subject_id)
      router.push(`/${locale}/t/${slug}/apps/${n.subject_id}${n.kind === "adoptable" ? "?adopt=1" : ""}`)
    setSheetOpen(false)
  }

  return (
    <Sheet open={sheetOpen} onOpenChange={(o) => { setSheetOpen(o); if (o) void markRead() }}>
      <Button
        variant="ghost"
        size="icon-sm"
        className="relative"
        aria-label={unread ? t("notifications.titleUnread", { count: unread }) : t("notifications.title")}
        aria-expanded={sheetOpen}
        data-testid="notifications"
        data-unread={unread}
        onClick={() => setSheetOpen(true)}
      >
        <BellIcon />
        {unread > 0 ? (
          <span className="absolute -end-0.5 -top-0.5 min-w-4 rounded-full bg-nq-danger px-1 text-center text-[11px] leading-4 text-white tabular-nums">
            {unread > 99 ? "99+" : unread}
          </span>
        ) : null}
      </Button>
      <SheetContent side="end" className="w-80 gap-0 sm:max-w-sm">
        <SheetHeader className="border-b border-border">
          <SheetTitle>{t("notifications.title")}</SheetTitle>
        </SheetHeader>
        {permission !== "unsupported" && permission !== "granted" ? (
          <div className="border-b border-border px-4 py-2">
            <Button variant="secondary" size="sm" onClick={requestDesktop} disabled={permission === "denied"}>
              {permission === "denied" ? t("notifications.desktopBlocked") : t("notifications.enableDesktop")}
            </Button>
          </div>
        ) : null}
        <div className="min-h-0 flex-1 overflow-y-auto">
          {items.length === 0 ? (
            <p className="px-4 py-3 text-body-sm text-muted-foreground">{t("notifications.empty")}</p>
          ) : (
            items.slice(0, 50).map((n) => (
              <button
                key={n.id}
                type="button"
                onClick={() => open(n)}
                className="flex w-full flex-col items-start gap-0.5 border-b border-border px-4 py-3 text-start hover:bg-secondary"
              >
                <span className="flex w-full items-center gap-2">
                  {!n.read_at ? <span aria-hidden className="size-1.5 shrink-0 rounded-full bg-nq-brand" /> : null}
                  <span className="truncate font-medium" dir="auto">
                    {n.title}
                  </span>
                </span>
                {n.body ? (
                  <span className="line-clamp-2 text-caption text-muted-foreground" dir="auto">
                    {n.body}
                  </span>
                ) : null}
                <span className="text-caption text-muted-foreground" dir="ltr">
                  {fmt(n.created_at)}
                </span>
              </button>
            ))
          )}
        </div>
      </SheetContent>
    </Sheet>
  )
}

/** The page header: an eyebrow (the team by default), then Nasaq's SectionHeader (the h1, its
 * description and the page's actions at the inline end). Content follows below a hairline. */
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
      <div className="flex flex-col gap-1.5 border-b border-border px-6 py-6">
        {micro ? <p className="eyebrow">{micro}</p> : null}
        <SectionHeader
          as="h1"
          title={title}
          description={description}
          action={actions ? <div className="flex flex-wrap items-center gap-2">{actions}</div> : undefined}
        />
      </div>
      {bleed ? children : <div className="flex flex-col gap-6 px-6 py-6">{children}</div>}
    </div>
  )
}
