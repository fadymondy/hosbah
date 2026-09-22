import {
  ActivityIcon,
  ArchiveIcon,
  BellRingIcon,
  BlocksIcon,
  CalendarClockIcon,
  CloudIcon,
  CogIcon,
  DatabaseIcon,
  GitBranchIcon,
  GlobeIcon,
  HardDriveIcon,
  KeyRoundIcon,
  LayoutDashboardIcon,
  ListOrderedIcon,
  NetworkIcon,
  RadioIcon,
  RocketIcon,
  ScrollTextIcon,
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
  { key: "monitors", icon: BellRingIcon, group: "observe", for: "both", ready: true },
  { key: "proxy", icon: NetworkIcon, group: "network", for: "both", ready: true },
  { key: "dns", icon: CloudIcon, group: "network", for: "both", ready: true },
  { key: "backups", icon: ArchiveIcon, group: "manage", for: "both", ready: true },
  { key: "settings", icon: ActivityIcon, group: "manage", for: "both", ready: true },
]

export const SITE_GROUPS: SiteSection["group"][] = ["site", "code", "services", "observe", "network", "manage"]

export function sectionsFor(imported: boolean) {
  return SITE_SECTIONS.filter((s) => s.for === "both" || (imported ? s.for === "imported" : s.for === "git"))
}
