"use client"

import Link from "next/link"
import { usePathname, useRouter } from "next/navigation"
import { useTheme } from "next-themes"
import { useCallback, useEffect, useState, type ReactNode } from "react"
import {
  BellIcon,
  BoxesIcon,
  ChevronsUpDownIcon,
  DatabaseIcon,
  ConstructionIcon,
  KeyRoundIcon,
  LanguagesIcon,
  LockKeyholeIcon,
  NetworkIcon,
  ServerIcon,
  LayoutDashboardIcon,
  LogOutIcon,
  MailPlusIcon,
  MoonIcon,
  ScrollTextIcon,
  SettingsIcon,
  ShieldCheckIcon,
  ShieldIcon,
  PlusIcon,
  SunIcon,
  UserIcon,
  UsersIcon,
} from "lucide-react"

import { CubeMark } from "@/components/cube-mark"
import { ReportProblem } from "@/components/feedback/report-problem"
import { useOtherLocaleHref } from "@/components/preferences"
import { Avatar, AvatarFallback } from "@/components/ui/avatar"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Separator } from "@/components/ui/separator"
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarInset,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarProvider,
  SidebarTrigger,
  useSidebar,
} from "@/components/ui/sidebar"
import { apiFetch } from "@/lib/api"
import type { Notification } from "@/lib/apps"
import { useFormatDate, useI18n } from "@/lib/i18n"
import { useSession } from "@/lib/session"
import { useOptionalTeam } from "@/lib/team"

/**
 * sidebar-07 adapted to the grid: the sidebar and the header draw CONTAINED rules (grid-rule-*),
 * never full-bleed ones, so no line runs through the navigation. The in-development notice is a
 * banner row that is always visible above every page.
 */
export function AppShell({ children, title }: { children: ReactNode; title?: ReactNode }) {
  const { locale, t } = useI18n()
  return (
    <SidebarProvider>
      <AppSidebar side={locale === "ar" ? "right" : "left"} />
      <SidebarInset className="min-w-0">
        <header className="grid-rule-bottom flex h-14 shrink-0 items-center gap-2 px-4">
          <SidebarTrigger className="-ms-1" label={t("shell.toggleSidebar")} />
          <Separator orientation="vertical" className="data-vertical:h-4 data-vertical:self-center" />
          <div className="min-w-0 flex-1 truncate text-sm font-medium">{title}</div>
          <NotificationsBell />
        </header>
        <DevBanner />
        <div className="grid-hatch h-4 grid-rule-bottom" aria-hidden />
        <main className="flex min-w-0 flex-1 flex-col">{children}</main>
      </SidebarInset>
    </SidebarProvider>
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
            className="relative inline-flex size-8 items-center justify-center hover:bg-muted aria-expanded:bg-muted"
            aria-label={unread ? t("notifications.titleUnread", { count: unread }) : t("notifications.title")}
            data-testid="notifications"
            data-unread={unread}
          />
        }
      >
        <BellIcon className="size-4" />
        {unread > 0 ? (
          <span className="absolute -end-0.5 -top-0.5 min-w-4 bg-primary px-1 text-center font-mono text-[10px] leading-4 text-primary-foreground tabular-nums">
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

function DevBanner() {
  const { t } = useI18n()
  return (
    <div
      role="status"
      className="grid-rule-bottom flex items-center gap-2 bg-muted px-4 py-2 text-sm text-foreground"
      data-testid="dev-banner"
    >
      <ConstructionIcon className="size-4 shrink-0 text-[var(--grid-warn-text)]" aria-hidden />
      <span>{t("shell.devBanner")}</span>
    </div>
  )
}

function AppSidebar({ side }: { side: "left" | "right" }) {
  const { t, locale } = useI18n()
  const pathname = usePathname() ?? ""
  const teamCtx = useOptionalTeam()
  const { me } = useSession()
  const base = teamCtx ? `/${locale}/t/${teamCtx.team.slug}` : null

  const nav = base
    ? [
        { href: base, label: t("nav.overview"), icon: <LayoutDashboardIcon />, exact: true },
        { href: `${base}/servers`, label: t("nav.servers"), icon: <ServerIcon /> },
        { href: `${base}/proxy-hosts`, label: t("nav.proxyHosts"), icon: <NetworkIcon /> },
        { href: `${base}/apps`, label: t("nav.apps"), icon: <BoxesIcon /> },
        { href: `${base}/databases`, label: t("nav.databases"), icon: <DatabaseIcon /> },
        { href: `${base}/members`, label: t("nav.members"), icon: <UsersIcon /> },
        { href: `${base}/invitations`, label: t("nav.invitations"), icon: <MailPlusIcon /> },
        { href: `${base}/tokens`, label: t("nav.tokens"), icon: <KeyRoundIcon /> },
        { href: `${base}/approvals`, label: t("nav.approvals"), icon: <ShieldCheckIcon /> },
        // Credentials are owner/admin only (the API answers 403 to anyone else).
        ...(teamCtx?.manage ? [{ href: `${base}/credentials`, label: t("nav.credentials"), icon: <LockKeyholeIcon /> }] : []),
        { href: `${base}/audit`, label: t("nav.audit"), icon: <ScrollTextIcon /> },
        { href: `${base}/settings`, label: t("nav.settings"), icon: <SettingsIcon /> },
      ]
    : []

  return (
    <Sidebar collapsible="icon" side={side}>
      <SidebarHeader className="grid-rule-bottom">
        <TeamSwitcher />
      </SidebarHeader>
      <SidebarContent>
        {nav.length ? (
          <SidebarGroup>
            <SidebarGroupLabel>{t("nav.team")}</SidebarGroupLabel>
            <SidebarMenu>
              {nav.map((item) => {
                const active = item.exact ? pathname === item.href : pathname.startsWith(item.href)
                return (
                  <SidebarMenuItem key={item.href}>
                    <SidebarMenuButton
                      isActive={active}
                      tooltip={item.label}
                      render={<Link href={item.href} />}
                    >
                      {item.icon}
                      <span>{item.label}</span>
                    </SidebarMenuButton>
                  </SidebarMenuItem>
                )
              })}
            </SidebarMenu>
          </SidebarGroup>
        ) : null}
        {me.user.platform_admin ? (
          <SidebarGroup>
            <SidebarGroupLabel>{t("nav.platform")}</SidebarGroupLabel>
            <SidebarMenu>
              <SidebarMenuItem>
                <SidebarMenuButton
                  isActive={pathname.startsWith(`/${locale}/admin`)}
                  tooltip={t("nav.admin")}
                  render={<Link href={`/${locale}/admin`} />}
                >
                  <ShieldIcon />
                  <span>{t("nav.admin")}</span>
                </SidebarMenuButton>
              </SidebarMenuItem>
            </SidebarMenu>
          </SidebarGroup>
        ) : null}
      </SidebarContent>
      <SidebarFooter className="grid-rule-top">
        <SidebarMenu>
          <SidebarMenuItem>
            <ReportProblem />
          </SidebarMenuItem>
        </SidebarMenu>
        <AccountMenu />
      </SidebarFooter>
    </Sidebar>
  )
}

function TeamSwitcher() {
  const { t, locale } = useI18n()
  const { isMobile } = useSidebar()
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
    <SidebarMenu>
      <SidebarMenuItem>
        <DropdownMenu>
          <DropdownMenuTrigger
            render={
              <SidebarMenuButton
                size="lg"
                className="aria-expanded:bg-sidebar-accent aria-expanded:text-sidebar-accent-foreground"
                aria-label={t("shell.switchTeam")}
              />
            }
          >
            <div className="flex aspect-square size-8 shrink-0 items-center justify-center border bg-background">
              <CubeMark size={18} className="dark:hidden" />
              <CubeMark size={18} onDark className="hidden dark:block" />
            </div>
            <div className="grid flex-1 text-start text-sm leading-tight">
              <span className="truncate font-medium">{active?.name ?? "Cloudy"}</span>
              <span className="truncate text-xs text-muted-foreground">
                {active ? t(`roles.${active.role}`) : t("shell.noTeam")}
              </span>
            </div>
            <ChevronsUpDownIcon className="ms-auto" />
          </DropdownMenuTrigger>
          <DropdownMenuContent
            className="min-w-56"
            align="start"
            side={isMobile ? "bottom" : "inline-end"}
            sideOffset={4}
          >
            <DropdownMenuGroup>
              <DropdownMenuLabel className="text-xs text-muted-foreground">{t("shell.teams")}</DropdownMenuLabel>
              {me.teams.map((team) => (
                <DropdownMenuItem key={team.id} onClick={() => router.push(hrefFor(team.slug))}>
                  <span className="flex-1 truncate">{team.name}</span>
                  {team.slug === active?.slug ? <ShieldCheckIcon aria-label={t("shell.current")} /> : null}
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
      </SidebarMenuItem>
    </SidebarMenu>
  )
}

function AccountMenu() {
  const { t, locale } = useI18n()
  const { isMobile } = useSidebar()
  const { me } = useSession()
  const router = useRouter()
  const { resolvedTheme, setTheme } = useTheme()
  const { href: otherLocale, target } = useOtherLocaleHref()
  const [signingOut, setSigningOut] = useState(false)
  const initials = me.user.email.slice(0, 2).toUpperCase()

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
    <SidebarMenu>
      <SidebarMenuItem>
        <DropdownMenu>
          <DropdownMenuTrigger
            render={<SidebarMenuButton size="lg" className="aria-expanded:bg-muted" aria-label={t("shell.account")} />}
          >
            <Avatar className="size-8 rounded-none">
              <AvatarFallback className="rounded-none font-mono text-xs">{initials}</AvatarFallback>
            </Avatar>
            <div className="grid flex-1 text-start text-sm leading-tight">
              <span className="truncate font-medium">{t("shell.account")}</span>
              <span className="truncate text-xs text-muted-foreground" dir="ltr">
                {me.user.email}
              </span>
            </div>
            <ChevronsUpDownIcon className="ms-auto" />
          </DropdownMenuTrigger>
          <DropdownMenuContent className="min-w-56" side={isMobile ? "bottom" : "inline-end"} align="end" sideOffset={4}>
            <DropdownMenuGroup>
              <DropdownMenuLabel className="truncate font-normal text-muted-foreground" dir="ltr">
                {me.user.email}
              </DropdownMenuLabel>
            </DropdownMenuGroup>
            <DropdownMenuSeparator />
            <DropdownMenuGroup>
              <DropdownMenuItem onClick={() => router.push(`/${locale}/account`)}>
                <UserIcon />
                {t("shell.accountAnd2fa")}
              </DropdownMenuItem>
              <DropdownMenuItem onClick={() => router.push(otherLocale)}>
                <LanguagesIcon />
                <span lang={target}>{target === "ar" ? "العربية" : "English"}</span>
              </DropdownMenuItem>
              <DropdownMenuItem
                closeOnClick={false}
                onClick={() => setTheme(resolvedTheme === "dark" ? "light" : "dark")}
              >
                <SunIcon className="hidden dark:block" />
                <MoonIcon className="dark:hidden" />
                {resolvedTheme === "dark" ? t("prefs.light") : t("prefs.dark")}
              </DropdownMenuItem>
            </DropdownMenuGroup>
            <DropdownMenuSeparator />
            <DropdownMenuItem variant="destructive" disabled={signingOut} onClick={signOut}>
              <LogOutIcon />
              {t("shell.signOut")}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </SidebarMenuItem>
    </SidebarMenu>
  )
}

/** Page body: a contained title row, then content. */
export function PageBody({
  title,
  description,
  actions,
  children,
}: {
  title: ReactNode
  description?: ReactNode
  actions?: ReactNode
  children: ReactNode
}) {
  return (
    <div className="flex flex-col">
      <div className="grid-rule-bottom flex flex-wrap items-end justify-between gap-4 px-6 py-8">
        <div className="flex min-w-0 flex-col gap-1">
          <h1 className="grid-title">{title}</h1>
          {description ? <p className="text-sm text-muted-foreground">{description}</p> : null}
        </div>
        {actions ? <div className="flex flex-wrap items-center gap-2">{actions}</div> : null}
      </div>
      <div className="flex flex-col gap-6 px-6 py-6">{children}</div>
    </div>
  )
}
