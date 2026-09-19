"use client"

import { useRouter } from "next/navigation"

import { ServerAppearance } from "@/components/server-tabs/appearance"
import { ConfirmButton } from "@/components/confirm-button"
import { useErrorText } from "@/components/form-error"
import { useLoadedServer } from "@/components/server-tabs/context"
import { SectionTitle } from "@/components/states"
import { apiFetch } from "@/lib/api"
import { useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

/** The server's settings tab. */
export default function ServerSettingsTab() {
  const { t, locale } = useI18n()
  const { team, manage } = useTeam()
  const router = useRouter()
  const errorText = useErrorText()
  const { server, path } = useLoadedServer()

  return (
    <>
      <ServerAppearance key={server.id} manage={manage} />
      <SectionTitle title={t("servers.tabs.danger")} />
      {manage ? (
        <div className="flex justify-end border-b border-line px-6 py-5">
          <ConfirmButton
            label={t("servers.detail.delete")}
            title={t("servers.detail.deleteTitle")}
            description={t(server.provider === "proxmox" ? "servers.detail.deleteHintProxmox" : "servers.detail.deleteHintByo", {
              name: server.name,
            })}
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
        </div>
      ) : null}
    </>
  )
}
