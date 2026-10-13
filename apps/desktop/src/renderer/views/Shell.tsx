import type { ReactNode } from "react";
import clsx from "clsx";
import { Bell, Boxes, DatabaseBackup, Globe, LayoutDashboard, Settings as SettingsIcon, WifiOff } from "lucide-react";
import { brandName } from "@hosbah/client/brand";
import mark from "../../../../shared/assets/mark.svg";
import { overallDot } from "../components/ui";
import { useApp, type Route } from "../lib/app";

const NAV: { section: Route["section"]; key: "tab.overview" | "tab.apps" | "tab.domains" | "tab.backups" | "tab.alerts" | "tab.settings"; icon: typeof Bell }[] = [
  { section: "overview", key: "tab.overview", icon: LayoutDashboard },
  { section: "apps", key: "tab.apps", icon: Boxes },
  { section: "domains", key: "tab.domains", icon: Globe },
  { section: "backups", key: "tab.backups", icon: DatabaseBackup },
  { section: "alerts", key: "tab.alerts", icon: Bell },
  { section: "settings", key: "tab.settings", icon: SettingsIcon },
];

export function Shell({ children }: { children: ReactNode }) {
  const { t, locale, session, route, go, monitor, setTeam } = useApp();
  const teams = session?.teams ?? [];
  const unread = monitor?.tray.unread ?? 0;
  const level = monitor?.tray.level ?? "green";
  const activeSection = route.section === "servers" ? "overview" : route.section;
  const team = route.team || session?.activeTeam || "";

  return (
    <div className="flex h-full">
      <aside className="flex w-60 shrink-0 flex-col border-e border-line bg-card">
        <div className="flex items-center gap-2.5 px-5 pt-5 pb-4">
          <img src={mark} alt="" className="size-8" />
          <span className="text-lg font-semibold text-fg">{brandName[locale]}</span>
        </div>

        <div className="px-4 pb-3">
          <label className="mb-1 block text-[11px] text-muted" htmlFor="team">
            {t("settings.team")}
          </label>
          <select
            id="team"
            value={team}
            onChange={(e) => void setTeam(e.target.value)}
            className="w-full rounded-lg border border-line bg-elevated px-2.5 py-1.5 text-sm text-fg"
          >
            {teams.map((tm) => (
              <option key={tm.id} value={tm.slug}>
                {tm.name}
              </option>
            ))}
          </select>
        </div>

        <nav className="flex-1 space-y-0.5 px-3" aria-label="main">
          {NAV.map(({ section, key, icon: Icon }) => {
            const active = activeSection === section;
            return (
              <button
                key={section}
                onClick={() => go({ team, section })}
                aria-current={active ? "page" : undefined}
                className={clsx(
                  "flex w-full items-center gap-3 rounded-lg px-3 py-2 text-start text-sm transition",
                  active ? "bg-action/12 font-medium text-fg" : "text-body hover:bg-soft",
                )}
              >
                <Icon className={clsx("size-[18px]", active ? "text-action" : "text-muted")} />
                <span className="flex-1">{t(key)}</span>
                {section === "alerts" && unread > 0 && <span className="ltr rounded-full bg-danger px-1.5 text-[11px] font-semibold text-white">{unread}</span>}
              </button>
            );
          })}
        </nav>

        <div className="flex items-center gap-2 border-t border-line px-5 py-3 text-xs text-muted">
          <span className={clsx("inline-block size-2.5 rounded-full", overallDot[level])} />
          <span className="truncate">{monitor?.tray.statusLine}</span>
        </div>
      </aside>

      <main className="min-w-0 flex-1 overflow-auto">
        {session?.offline && (
          <div className="flex items-center gap-2 bg-warn/20 px-6 py-2 text-sm text-warn-text">
            <WifiOff className="size-4" />
            {t("common.offline")}
          </div>
        )}
        <div className="mx-auto max-w-5xl p-6">{children}</div>
      </main>
    </div>
  );
}
