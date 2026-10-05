"use client"

import { Field, FieldLabel, Input } from "@fadymondy/nasaq/web"
import { useState } from "react"

import type { Controlled } from "@/components/dialog-state"
import { SERVER_COLORS, SERVER_ICONS, ServerMark } from "@/components/server-mark"
import { EditDialog } from "@/components/server-tabs/list-tools"
import { apiFetch } from "@/lib/api"
import { useI18n } from "@/lib/i18n"
import type { Server } from "@/lib/servers"
import { cn } from "@/lib/utils"

/** Edits a server user (a nested LXC/VM): its title, colour and icon. Its slug stays fixed. */
export function EditUser({ team, user, slug, onSaved, ...controlled }: { team: string; user: Server; slug: string; onSaved: () => void } & Controlled) {
  const { t } = useI18n()
  const [name, setName] = useState(user.name)
  const [color, setColor] = useState(user.color ?? "")
  const [icon, setIcon] = useState(user.icon ?? "")

  return (
    <EditDialog
      {...controlled}
      title={t("servers.users.editTitle")}
      description={t("servers.users.editHint", { slug })}
      onSave={async () => {
        await apiFetch(`/api/teams/${team}/servers/${user.id}`, { method: "PATCH", body: { name: name.trim(), color, icon } })
        onSaved()
      }}
    >
      <div className="flex items-center gap-3">
        <ServerMark server={{ color, icon, logo_url: user.logo_url }} size={10} />
        <span dir="ltr" className="font-mono text-caption text-muted-foreground">
          {slug}
        </span>
      </div>
      <Field>
        <FieldLabel htmlFor="user-title">{t("servers.users.titleLabel")}</FieldLabel>
        <Input id="user-title" dir="auto" required value={name} onChange={(e) => setName(e.target.value)} />
      </Field>
      <Field>
        <FieldLabel>{t("servers.appearance.color")}</FieldLabel>
        <div className="flex flex-wrap items-center gap-2" role="radiogroup">
          <button
            type="button"
            role="radio"
            aria-checked={!color}
            aria-label={t("servers.appearance.none")}
            onClick={() => setColor("")}
            className={cn("size-7 rounded-full border border-dashed border-border", !color && "ring-2 ring-nq-brand ring-offset-2 ring-offset-background")}
          />
          {SERVER_COLORS.map((c) => (
            <button
              key={c}
              type="button"
              role="radio"
              aria-checked={color === c}
              aria-label={c}
              onClick={() => setColor(c)}
              className={cn("size-7 rounded-full border border-border", color === c && "ring-2 ring-nq-brand ring-offset-2 ring-offset-background")}
              style={{ background: c }}
            />
          ))}
        </div>
      </Field>
      <Field>
        <FieldLabel>{t("servers.appearance.icon")}</FieldLabel>
        <div className="flex flex-wrap gap-1" role="radiogroup">
          {Object.entries(SERVER_ICONS).map(([key, Icon]) => (
            <button
              key={key}
              type="button"
              role="radio"
              aria-checked={icon === key}
              aria-label={key}
              title={key}
              onClick={() => setIcon(icon === key ? "" : key)}
              className={cn(
                "flex size-8 items-center justify-center rounded-md border border-border [&_svg]:size-4",
                icon === key ? "bg-secondary text-foreground ring-1 ring-nq-brand" : "text-muted-foreground hover:bg-secondary",
              )}
            >
              <Icon />
            </button>
          ))}
        </div>
      </Field>
    </EditDialog>
  )
}
