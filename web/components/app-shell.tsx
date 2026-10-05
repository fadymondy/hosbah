"use client"

import {
  AppHeader,
  AppMain,
  AppShell as NasaqAppShell,
  Button,
  DropdownMenuItem,
  NotificationCenter,
  type NotificationCenterItem,
  ProductLogo,
  ProductMark,
  SectionHeader,
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarHeader,
  SidebarNest,
  SidebarSubItem,
  SidebarTrigger,
  toast,
  useSidebarCollapsed,
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
  RocketIcon,
  BellIcon,
  CheckIcon,
  CircleCheckIcon,
  CircleXIcon,
  InfoIcon,
  SirenIcon,
  ExternalLinkIcon,
  GlobeIcon,
  KeyRoundIcon,
  LayoutGridIcon,
  LayoutTemplateIcon,
  MailIcon,
  MegaphoneIcon,
  TriangleAlertIcon,
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

import { ConnectAgent } from "@/components/connect-agent"
import { MahaamFeedback } from "@/components/feedback/mahaam-feedback"
import { ReportProblem, requestReport } from "@/components/feedback/report-problem"
import { GlobalSearch } from "@/components/shell/global-search"
import { isPlainClick, NavLink } from "@/components/shell/nav-link"
import { ShellScopeProvider } from "@/components/shell/scope"
import { ScopeCrumbs, SiteNav, useScope } from "@/components/shell/scope-nav"

import { apiFetch } from "@/lib/api"
import type { Notification } from "@/lib/apps"
import { useI18n } from "@/lib/i18n"
import { useEventStream } from "@/lib/servers"
import { useSession } from "@/lib/session"
import { usePublicSettings } from "@/lib/platform"
import { useOptionalTeam } from "@/lib/team"
import { cn } from "@/lib/utils"

/**
 * The signed-in shell on Nasaq's AppShell, laid out like its app-shell dashboard: the sidebar
 * (logo, team switcher and search on top, then the navigation, then the account menu) and a slim
 * header (sidebar trigger and scope crumbs at the start;
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
          <ScopeCrumbs />
          <div className="ms-auto flex items-center gap-2">
            <ConnectAgent />
            <NotificationsBell />
          </div>
        </AppHeader>
        <Announcement />
        <AppMain className="p-0">{children}</AppMain>
        {/* Mahaam Feedback SDK, for signed-in people only: the shell only renders behind the session. */}
        <MahaamFeedback />
        {/* The report dialog; its launcher is the account menu's item. */}
        <ReportProblem launcher={false} />
      </NasaqAppShell>
    </ShellScopeProvider>
  )
}

function AppSidebar() {
  const { t } = useI18n()
  return (
    <Sidebar aria-label={t("shell.navigation")}>
      <SidebarHeader>
        <Brand />
        <TeamSwitcher />
        <GlobalSearch />
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

/** The Hosbah logo at the top of the sidebar, like every Nasaq product: mark and wordmark (the
 * Arabic name in ar), the mark alone on the collapsed rail. */
function Brand() {
  const { locale, t } = useI18n()
  const collapsed = useSidebarCollapsed()
  return (
    <Link
      href={`/${locale}`}
      aria-label={t("common.brand")}
      className={cn("flex h-control items-center rounded-control outline-none focus-visible:outline-2 focus-visible:outline-nq-focus", collapsed ? "justify-center" : "px-1.5")}
    >
      {collapsed ? <ProductMark brand="hosbah" size={20} title="" /> : <ProductLogo brand="hosbah" size={20} arabic={locale === "ar"} />}
    </Link>
  )
}

const SETTINGS_PAGES = ["settings", "notifications", "status-pages", "log-drains", "tokens", "ssh-keys", "credentials", "certificates", "audit", "costs"]

/** A Settings sub-page in the sidebar's Settings dropdown (Nasaq SidebarSubItem), routed client-side. */
function SubLink({ href, children }: { href: string; children: string }) {
  const pathname = usePathname() ?? ""
  const router = useRouter()
  return (
    <SidebarSubItem
      href={href}
      active={pathname === href || pathname.startsWith(href + "/")}
      onClick={(e) => {
        if (!isPlainClick(e)) return
        e.preventDefault()
        router.push(href)
      }}
    >
      {children}
    </SidebarSubItem>
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
  const under = (p: string) => pathname === p || pathname.startsWith(p + "/")
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

  // A server keeps the team sidebar; its sections are the tabs under its cover (components/server-tabs/header.tsx).
  if (scope.kind === "site") return <SiteNav site={scope.site} />

  return (
    <>
      {base ? (
        <>
          <SidebarGroup>
            <L href={base} exact icon={<HouseIcon />}>
              {t("nav.home")}
            </L>
            <L href={`${base}/servers`} icon={<ServerIcon />}>
              {t("nav.servers")}
            </L>
            <L href={`${base}/apps`} icon={<AppWindowIcon />}>
              {t("nav.apps")}
            </L>
            <L href={`${base}/deployments`} icon={<RocketIcon />}>
              {t("nav.deployments")}
            </L>
          </SidebarGroup>
          <SidebarGroup label={t("nav.groups.infrastructure")}>
            <L href={`${base}/databases`} icon={<DatabaseIcon />}>
              {t("nav.databases")}
            </L>
            <L href={`${base}/domains`} icon={<GlobeIcon />} also={[`${base}/proxy-hosts`, `${base}/dns`]}>
              {t("nav.domains")}
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
            <SidebarNest
              label={t("nav.settings")}
              icon={<SettingsIcon />}
              active={SETTINGS_PAGES.some((p) => under(`${base}/${p}`))}
              defaultOpen={SETTINGS_PAGES.some((p) => under(`${base}/${p}`))}
            >
              <SubLink href={`${base}/settings`}>{t("nav.general")}</SubLink>
              <SubLink href={`${base}/notifications`}>{t("nav.notifications")}</SubLink>
              <SubLink href={`${base}/status-pages`}>{t("nav.statusPages")}</SubLink>
              <SubLink href={`${base}/log-drains`}>{t("nav.logDrains")}</SubLink>
              <SubLink href={`${base}/tokens`}>{t("nav.tokens")}</SubLink>
              <SubLink href={`${base}/ssh-keys`}>{t("nav.sshKeys")}</SubLink>
              {teamCtx?.manage ? <SubLink href={`${base}/credentials`}>{t("nav.credentials")}</SubLink> : null}
              <SubLink href={`${base}/certificates`}>{t("nav.certificates")}</SubLink>
              <SubLink href={`${base}/audit`}>{t("nav.audit")}</SubLink>
              <SubLink href={`${base}/costs`}>{t("nav.billing")}</SubLink>
            </SidebarNest>
          </SidebarGroup>
        </>
      ) : null}
    </>
  )
}

/** The current team, as Nasaq's workspace switcher (under the logo in the sidebar header). */
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
export function AccountMenu() {
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
      onSignOut={signOut}
      labels={{ signOut: t("shell.signOut") }}
    >
      <DropdownMenuItem onClick={() => router.push(`/${locale}/account`)}>
        <UserIcon />
        {t("shell.accountAnd2fa")}
      </DropdownMenuItem>
      {me.user.platform_admin ? (
        <DropdownMenuItem onClick={() => router.push(`/${locale}/admin`)}>
          <ShieldIcon />
          {t("nav.admin")}
        </DropdownMenuItem>
      ) : null}
      <DropdownMenuItem onClick={requestReport}>
        <MessageSquareWarningIcon />
        {t("feedback.launcher")}
      </DropdownMenuItem>
    </UserMenu>
  )
}

/** Each kind gets its own colour and icon so failures, alerts and recoveries read apart at a glance. */
const NOTIFICATION_TONES: Record<string, { icon: typeof BellIcon; className: string }> = {
  danger: { icon: CircleXIcon, className: "bg-nq-danger/15 text-nq-danger-text" },
  alert: { icon: SirenIcon, className: "bg-nq-warning/15 text-nq-warning-text" },
  success: { icon: CircleCheckIcon, className: "bg-nq-success/15 text-nq-success-text" },
  info: { icon: InfoIcon, className: "bg-nq-info/15 text-nq-info" },
  neutral: { icon: BellIcon, className: "bg-secondary text-muted-foreground" },
}

function notificationTone(kind: string) {
  if (kind === "deploy.failed" || kind.endsWith(".failed")) return NOTIFICATION_TONES.danger
  if (kind === "monitor.resolved" || kind === "deploy.succeeded") return NOTIFICATION_TONES.success
  if (kind.startsWith("monitor.")) return NOTIFICATION_TONES.alert
  if (kind === "adoptable") return NOTIFICATION_TONES.info
  return NOTIFICATION_TONES.neutral
}

function NotificationIcon({ kind }: { kind: string }) {
  const tone = notificationTone(kind)
  const Icon = tone.icon
  // Fills Nasaq's round icon slot (border included) with the tone.
  return (
    <span data-kind={kind} className={cn("-m-px flex size-8 items-center justify-center rounded-full", tone.className)}>
      <Icon />
    </span>
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

  function patch(id: string, readAt: string | null) {
    setItems((list) => list.map((n) => (n.id === id ? { ...n, read_at: readAt } : n)))
  }

  /** Optimistic: flip the row and the count now, put them back if the API refuses. */
  async function setRead(n: Notification, read: boolean) {
    if (!!n.read_at === read) return
    const before = n.read_at
    patch(n.id, read ? new Date().toISOString() : null)
    setUnread((c) => Math.max(0, c + (read ? -1 : 1)))
    try {
      await apiFetch(`/api/teams/${slug}/notifications/${n.id}/${read ? "read" : "unread"}`, { method: "POST" })
    } catch {
      patch(n.id, before)
      setUnread((c) => Math.max(0, c + (read ? 1 : -1)))
      toast.error(t("notifications.updateFailed"))
    }
  }

  async function markAllRead() {
    if (unread === 0) return
    const before = items
    setItems((list) => list.map((n) => (n.read_at ? n : { ...n, read_at: new Date().toISOString() })))
    setUnread(0)
    try {
      await apiFetch(`/api/teams/${slug}/notifications/read`, { method: "POST" })
    } catch {
      setItems(before)
      toast.error(t("notifications.updateFailed"))
    }
  }

  function requestDesktop() {
    if (typeof window === "undefined" || !("Notification" in window)) return
    void Notification.requestPermission().then(setPermission)
  }

  function open(n: Notification) {
    void setRead(n, true)
    if (n.subject_type === "app" && n.subject_id)
      router.push(`/${locale}/t/${slug}/apps/${n.subject_id}${n.kind === "adoptable" ? "?adopt=1" : ""}`)
    setSheetOpen(false)
  }

  const byId = new Map(items.map((n) => [n.id, n]))
  const rows: NotificationCenterItem[] = items.slice(0, 50).map((n) => ({
    id: n.id,
    title: n.title,
    description: n.body ?? undefined,
    icon: <NotificationIcon kind={n.kind} />,
    time: n.created_at,
    unread: !n.read_at,
  }))

  return (
    <NotificationCenter
      variant="sheet"
      side="end"
      open={sheetOpen}
      onOpenChange={setSheetOpen}
      items={rows}
      unreadCount={unread}
      onMarkAllRead={() => void markAllRead()}
      onItemClick={(row) => {
        const n = byId.get(row.id)
        if (n) open(n)
      }}
      itemActions={(row) => {
        const n = byId.get(row.id)
        if (!n) return []
        const isUnread = !n.read_at
        return [
          {
            id: "toggle-read",
            label: isUnread ? t("notifications.markRead") : t("notifications.markUnread"),
            icon: isUnread ? CheckIcon : MailIcon,
            onSelect: () => void setRead(n, isUnread),
          },
          ...(n.subject_type === "app" && n.subject_id
            ? [{ id: "open", label: t("notifications.open"), icon: ExternalLinkIcon, onSelect: () => open(n) }]
            : []),
        ]
      }}
      labels={{
        title: t("notifications.title"),
        all: t("notifications.all"),
        unread: t("notifications.unread"),
        markAllRead: t("notifications.markAllRead"),
        emptyAll: t("notifications.empty"),
        emptyAllDescription: t("notifications.emptyHint"),
        emptyUnread: t("notifications.caughtUp"),
        emptyUnreadDescription: t("notifications.caughtUpHint"),
        trigger: (c) => (c ? t("notifications.titleUnread", { count: c }) : t("notifications.title")),
      }}
      header={
        permission !== "unsupported" && permission !== "granted" ? (
          <Button variant="secondary" size="sm" onClick={requestDesktop} disabled={permission === "denied"}>
            {permission === "denied" ? t("notifications.desktopBlocked") : t("notifications.enableDesktop")}
          </Button>
        ) : undefined
      }
      data-testid="notifications"
      data-unread={unread}
    />
  )
}

/** The page header: an eyebrow (the team by default), then Nasaq's SectionHeader (the h1, its
 * description and the page's actions at the inline end). Content follows as cards, no hairline. */
export function PageBody({
  title,
  description,
  eyebrow,
  actions,
  bleed = false,
  divider = false,
  children,
}: {
  title: ReactNode
  description?: ReactNode
  eyebrow?: ReactNode
  actions?: ReactNode
  /** Full-bleed content: sections draw their own hairlines edge to edge (no padded column). */
  bleed?: boolean
  /** A hairline under the header (off: the content below is cards, as in Nasaq). */
  divider?: boolean
  children: ReactNode
}) {
  const micro = eyebrow
  return (
    <div className="flex flex-col">
      <div className={cn("flex flex-col gap-1.5 px-6 py-6", divider && "border-b border-border")}>
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
