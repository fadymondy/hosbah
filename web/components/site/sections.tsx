import {
  ActivityIcon,
  SquareTerminalIcon,
  ArchiveIcon,
  BellRingIcon,
  BlocksIcon,
  CalendarClockIcon,
  ChartNoAxesColumnIcon,
  CloudIcon,
  CogIcon,
  DatabaseIcon,
  FileCode2Icon,
  GaugeIcon,
  GitBranchIcon,
  GlobeIcon,
  HardDriveIcon,
  KeyRoundIcon,
  LayoutDashboardIcon,
  LayersIcon,
  RadarIcon,
  EarthIcon,
  ListOrderedIcon,
  NetworkIcon,
  PackageIcon,
  RadioIcon,
  RocketIcon,
  ScrollTextIcon,
  ShieldAlertIcon,
  ShieldIcon,
  WaypointsIcon,
  ZapIcon,
  type LucideIcon,
} from "lucide-react"

export type SiteSection = {
  /** The route segment under /apps/{name}; "" is the overview. Also the i18n key (site.nav.*). */
  key: string
  icon: LucideIcon
  group: "site" | "code" | "services" | "observe" | "network" | "manage"
  /** Shown for a git-deployed app, an imported workload, or both. */
  for: "git" | "imported" | "both"
  /** False while the section is a description of what is coming, not a working page. */
  ready: boolean
}

export const SITE_SECTIONS: SiteSection[] = [
  { key: "", icon: LayoutDashboardIcon, group: "site", for: "both", ready: true },
  { key: "repository", icon: GitBranchIcon, group: "code", for: "git", ready: true },
  { key: "deployments", icon: RocketIcon, group: "code", for: "git", ready: true },
  { key: "releases", icon: PackageIcon, group: "code", for: "git", ready: true },
  { key: "deploy-script", icon: FileCode2Icon, group: "code", for: "git", ready: true },
  { key: "environment", icon: KeyRoundIcon, group: "code", for: "git", ready: true },
  { key: "databases", icon: DatabaseIcon, group: "services", for: "git", ready: true },
  { key: "storage", icon: HardDriveIcon, group: "services", for: "git", ready: true },
  { key: "workers", icon: CogIcon, group: "services", for: "git", ready: true },
  { key: "queues", icon: ListOrderedIcon, group: "services", for: "git", ready: true },
  { key: "schedules", icon: CalendarClockIcon, group: "services", for: "git", ready: true },
  { key: "realtime", icon: RadioIcon, group: "services", for: "git", ready: true },
  { key: "browsers", icon: GlobeIcon, group: "services", for: "git", ready: true },
  { key: "stacks", icon: BlocksIcon, group: "services", for: "git", ready: true },
  { key: "logs", icon: ScrollTextIcon, group: "observe", for: "both", ready: true },
  { key: "analytics", icon: ChartNoAxesColumnIcon, group: "observe", for: "both", ready: true },
  { key: "web-vitals", icon: GaugeIcon, group: "observe", for: "both", ready: true },
  { key: "apm", icon: WaypointsIcon, group: "observe", for: "both", ready: true },
  { key: "monitors", icon: BellRingIcon, group: "observe", for: "both", ready: true },
  { key: "security", icon: ShieldAlertIcon, group: "observe", for: "both", ready: true },
  { key: "proxy", icon: NetworkIcon, group: "network", for: "both", ready: true },
  { key: "cache", icon: ZapIcon, group: "network", for: "both", ready: true },
  { key: "rules", icon: ShieldIcon, group: "network", for: "both", ready: true },
  { key: "dns", icon: CloudIcon, group: "network", for: "both", ready: true },
  { key: "terminal", icon: SquareTerminalIcon, group: "manage", for: "git", ready: true },
  { key: "backups", icon: ArchiveIcon, group: "manage", for: "both", ready: true },
  { key: "settings", icon: ActivityIcon, group: "manage", for: "both", ready: true },
]

export const SITE_GROUPS: SiteSection["group"][] = ["site", "code", "services", "observe", "network", "manage"]

export function sectionsFor(imported: boolean) {
  return SITE_SECTIONS.filter((s) => s.for === "both" || (imported ? s.for === "imported" : s.for === "git"))
}

export type SiteTab = {
  /** The route segment under /apps/{name}; "" is the overview. Also the i18n key (site.nav.*). */
  key: string
  icon: LucideIcon
  for: SiteSection["for"]
  /** Sections this tab holds, switched in-page (?view=…); the first is the default. */
  views?: readonly string[]
  /** Sections this tab holds as nodes of the flow, opened in a side sheet (?open=…). */
  nodes?: readonly string[]
}

/**
 * The app's tabs. Related sections share one tab: the overview carries the traffic numbers, the
 * architecture is the flow of everything the app uses, networking is every way traffic reaches it.
 */
export const SITE_TABS: SiteTab[] = [
  { key: "", icon: LayoutDashboardIcon, for: "both", views: ["analytics", "web-vitals", "apm"] },
  {
    key: "architecture",
    icon: LayersIcon,
    for: "git",
    nodes: ["databases", "storage", "workers", "queues", "schedules", "realtime", "browsers", "stacks"],
  },
  { key: "deployments", icon: RocketIcon, for: "git", views: ["deployments", "releases", "repository", "deploy-script"] },
  { key: "environment", icon: KeyRoundIcon, for: "git" },
  { key: "networking", icon: EarthIcon, for: "both", views: ["proxy", "dns", "cache", "rules"] },
  { key: "logs", icon: ScrollTextIcon, for: "both" },
  { key: "monitoring", icon: RadarIcon, for: "both", views: ["monitors", "security"] },
  { key: "terminal", icon: SquareTerminalIcon, for: "git" },
  { key: "backups", icon: ArchiveIcon, for: "both" },
  { key: "settings", icon: ActivityIcon, for: "both" },
]

export function tabsFor(imported: boolean) {
  return SITE_TABS.filter((s) => s.for === "both" || (imported ? s.for === "imported" : s.for === "git"))
}

/** Where an old section route lives now: its tab and how it is opened there. */
export function sectionHome(section: string): { tab: string; query: string } | null {
  for (const tab of SITE_TABS) {
    const v = tab.views?.indexOf(section) ?? -1
    if (v >= 0 && tab.key !== section) return { tab: tab.key, query: `?view=${section}` }
    if (tab.nodes?.includes(section)) return { tab: tab.key, query: `?open=${section}` }
  }
  return null
}

/** The tab a section belongs to (itself when it is a tab). */
export function tabOf(section: string) {
  return SITE_TABS.find((t) => t.key === section || t.views?.includes(section) || t.nodes?.includes(section))?.key ?? ""
}
