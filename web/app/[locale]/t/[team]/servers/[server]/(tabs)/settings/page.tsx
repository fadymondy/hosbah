"use client"

import { useRouter } from "next/navigation"
import { useEffect, useState } from "react"
import { DownloadIcon, PaletteIcon, RefreshCwIcon, Trash2Icon, UsersIcon } from "lucide-react"

import { ServerAppearance } from "@/components/server-tabs/appearance"
import { ServerUpdates } from "@/components/server-tabs/updates"
import { PanelAccounts } from "@/components/server-tabs/panel-accounts"
import { Import } from "@/components/server-tabs/import"
import { ConfirmButton } from "@/components/confirm-button"
import { useErrorText } from "@/components/form-error"
import { useLoadedServer } from "@/components/server-tabs/context"
import { SettingsSection, SettingsSections, type SettingsGroup } from "@fadymondy/nasaq/web"
import { apiFetch } from "@/lib/api"
import { useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

/** The server's settings tab: one page per topic, picked from the side nav. The page id is the URL
 * hash, so links like settings#import (the deploy flows) open on the right page. */
export default function ServerSettingsTab() {
  const { t, locale } = useI18n()
  const { team, manage } = useTeam()
  const router = useRouter()
  const errorText = useErrorText()
  const { server, path } = useLoadedServer()
  const [page, setPage] = useState("appearance")

  useEffect(() => {
    const hash = window.location.hash.slice(1)
    if (hash) setPage(hash)
  }, [])

  function open(id: string) {
    setPage(id)
    window.history.replaceState(null, "", `#${id}`)
  }

  const deleteHint = t(server.provider === "proxmox" ? "servers.detail.deleteHintProxmox" : "servers.detail.deleteHintByo", { name: server.name })
  const groups: SettingsGroup[] = [
    {
      id: "server",
      label: server.name,
      pages: [
        {
          id: "appearance",
          label: t("servers.appearance.title"),
          icon: <PaletteIcon />,
          content: <ServerAppearance key={`appearance-${server.id}`} manage={manage} />,
        },
        { id: "updates", label: t("servers.updates.title"), icon: <RefreshCwIcon />, content: <ServerUpdates key={`updates-${server.id}`} manage={manage} /> },
        { id: "panel", label: t("panel.accounts.title"), icon: <UsersIcon />, content: <PanelAccounts key={`panel-accounts-${server.id}`} manage={manage} /> },
        ...(server.status === "enrolled"
          ? [{ id: "import", label: t("servers.import.title"), icon: <DownloadIcon />, content: <Import key={`import-${server.id}`} serverId={server.id} manage={manage} /> }]
          : []),
      ],
    },
    ...(manage
      ? [
          {
            id: "danger",
            label: t("servers.tabs.danger"),
            pages: [
              {
                id: "danger",
                label: t("servers.detail.delete"),
                icon: <Trash2Icon />,
                tone: "danger" as const,
                content: (
                  <SettingsSection
                    tone="danger"
                    title={t("servers.tabs.danger")}
                    description={deleteHint}
                    actions={
                      <ConfirmButton
                        label={t("servers.detail.delete")}
                        title={t("servers.detail.deleteTitle")}
                        description={deleteHint}
                        confirmLabel={t("servers.detail.delete")}
                        onConfirm={async () => {
                          try {
                            await apiFetch(path, { method: "DELETE" })
                          } catch (err) {
                            throw new Error(errorText(err))
                          }
                          router.push(`/${locale}/t/${team.slug}`)
                        }}
                      />
                    }
                  />
                ),
              },
            ],
          },
        ]
      : []),
  ]
  const known = groups.some((g) => g.pages.some((p) => p.id === page))

  return (
    <SettingsSections
      className="max-w-none"
      title={t("servers.tabs.settings")}
      description={null}
      groups={groups}
      value={known ? page : "appearance"}
      onValueChange={open}
    />
  )
}
