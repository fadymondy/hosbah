"use client"

import {
  BoxIcon,
  BriefcaseIcon,
  Building2Icon,
  CameraIcon,
  CloudIcon,
  CodeIcon,
  CpuIcon,
  DatabaseIcon,
  FlameIcon,
  Gamepad2Icon,
  GlobeIcon,
  GraduationCapIcon,
  HeartIcon,
  HomeIcon,
  LayersIcon,
  LeafIcon,
  MusicIcon,
  RocketIcon,
  ServerIcon,
  ShieldIcon,
  StarIcon,
  StoreIcon,
  TerminalIcon,
  ZapIcon,
  type LucideIcon,
} from "lucide-react"

import type { Server } from "@/lib/servers"

/** The icon keys the API accepts for a server (servers.icon). */
export const SERVER_ICONS: Record<string, LucideIcon> = {
  server: ServerIcon,
  database: DatabaseIcon,
  globe: GlobeIcon,
  cloud: CloudIcon,
  cpu: CpuIcon,
  box: BoxIcon,
  rocket: RocketIcon,
  shield: ShieldIcon,
  code: CodeIcon,
  terminal: TerminalIcon,
  layers: LayersIcon,
  zap: ZapIcon,
  flame: FlameIcon,
  leaf: LeafIcon,
  star: StarIcon,
  heart: HeartIcon,
  briefcase: BriefcaseIcon,
  home: HomeIcon,
  store: StoreIcon,
  "graduation-cap": GraduationCapIcon,
  building: Building2Icon,
  gamepad: Gamepad2Icon,
  music: MusicIcon,
  camera: CameraIcon,
}

export const SERVER_COLORS = ["#3B82F6", "#22C55E", "#EAB308", "#F97316", "#EF4444", "#EC4899", "#A855F7", "#14B8A6", "#64748B"]

/** A server's badge: its logo, else its icon in its color, else the plain server icon. */
export function ServerMark({ server, size = 8 }: { server: Pick<Server, "color" | "icon" | "logo_url">; size?: 6 | 8 | 10 | 16 | 22 }) {
  const Icon = SERVER_ICONS[server.icon ?? ""] ?? ServerIcon
  const box = size === 22 ? "size-18 rounded-xl sm:size-22 [&_svg]:size-9 sm:[&_svg]:size-11" : size === 16 ? "size-16 [&_svg]:size-8" : size === 10 ? "size-10 [&_svg]:size-5" : size === 6 ? "size-6 [&_svg]:size-3.5" : "size-8 [&_svg]:size-4"
  return (
    <span
      className={`flex ${box} shrink-0 items-center justify-center overflow-hidden border border-border bg-background text-nq-brand`}
      style={server.color && !server.logo_url ? { color: server.color, borderColor: server.color } : undefined}
    >
      {server.logo_url ? (
        // eslint-disable-next-line @next/next/no-img-element
        <img src={server.logo_url} alt="" className="size-full object-contain" referrerPolicy="no-referrer" />
      ) : (
        <Icon />
      )}
    </span>
  )
}
