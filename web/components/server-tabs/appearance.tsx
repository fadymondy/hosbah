"use client"

import { useRef, useState, type FormEvent } from "react"
import { Trash2Icon, UploadIcon } from "lucide-react"
import { toast } from "sonner"

import { useErrorText } from "@/components/form-error"
import { SERVER_COLORS, SERVER_ICONS, ServerMark } from "@/components/server-mark"
import { useLoadedServer } from "@/components/server-tabs/context"
import { ErrorBand, SectionTitle, SettingsRow } from "@/components/states"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { apiFetch } from "@/lib/api"
import { useI18n } from "@/lib/i18n"
import type { Server } from "@/lib/servers"
import { cn } from "@/lib/utils"

/** Name, color, icon and logo: how the server looks on the home page and in its header. */
export function ServerAppearance({ manage }: { manage: boolean }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const { server, setServer, path } = useLoadedServer()
  const [name, setName] = useState(server.name)
  const [color, setColor] = useState(server.color ?? "")
  const [icon, setIcon] = useState(server.icon ?? "")
  const logo = server.logo_url ?? ""
  const file = useRef<HTMLInputElement>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function save(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const s = await apiFetch<Server>(path, {
        method: "PATCH",
        body: { name: name.trim(), color, icon },
      })
      setServer((cur) => (cur ? { ...cur, ...s } : s))
      toast.success(t("servers.appearance.saved"))
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  // The logo is an uploaded image (PNG, JPEG or WebP, up to 512 KB); it saves on its own.
  async function upload(f: File | null | undefined) {
    if (!f) return
    setError(null)
    if (!["image/png", "image/jpeg", "image/webp"].includes(f.type)) return setError(t("servers.appearance.logoType"))
    if (f.size > 512 * 1024) return setError(t("servers.appearance.logoSize"))
    setBusy(true)
    try {
      const s = await apiFetch<Server>(`${path}/logo`, { method: "PUT", body: f })
      setServer((cur) => (cur ? { ...cur, ...s } : s))
      toast.success(t("servers.appearance.saved"))
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
      if (file.current) file.current.value = ""
    }
  }

  async function removeLogo() {
    setBusy(true)
    setError(null)
    try {
      const s = await apiFetch<Server>(`${path}/logo`, { method: "DELETE" })
      setServer((cur) => (cur ? { ...cur, ...s, logo_url: s.logo_url ?? null } : s))
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={save}>
      <SectionTitle
        title={t("servers.appearance.title")}
        hint={t("servers.appearance.hint")}
        action={<ServerMark server={{ color, icon, logo_url: logo || null }} size={10} />}
      />
      <ErrorBand message={error} />
      <fieldset disabled={!manage || busy} className="contents">
        <SettingsRow label={t("servers.appearance.name")} htmlFor="srv-name">
          <Input id="srv-name" dir="ltr" required maxLength={63} value={name} onChange={(e) => setName(e.target.value)} />
        </SettingsRow>
        <SettingsRow label={t("servers.appearance.color")}>
          <div className="flex flex-wrap items-center gap-2" role="radiogroup">
            <button
              type="button"
              role="radio"
              aria-checked={!color}
              aria-label={t("servers.appearance.none")}
              onClick={() => setColor("")}
              className={cn("size-7 border border-dashed border-line", !color && "ring-2 ring-[var(--grid-brand)] ring-offset-2 ring-offset-background")}
            />
            {SERVER_COLORS.map((c) => (
              <button
                key={c}
                type="button"
                role="radio"
                aria-checked={color === c}
                aria-label={c}
                onClick={() => setColor(c)}
                className={cn("size-7 border border-line", color === c && "ring-2 ring-[var(--grid-brand)] ring-offset-2 ring-offset-background")}
                style={{ background: c }}
              />
            ))}
            <input
              type="color"
              aria-label={t("servers.appearance.custom")}
              value={color || "#3B82F6"}
              onChange={(e) => setColor(e.target.value.toUpperCase())}
              className="h-7 w-10 cursor-pointer border border-line bg-transparent"
            />
          </div>
        </SettingsRow>
        <SettingsRow label={t("servers.appearance.icon")}>
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
                  "flex size-8 items-center justify-center border border-line [&_svg]:size-4",
                  icon === key ? "bg-grid-soft text-grid-fg ring-1 ring-[var(--grid-brand)]" : "text-grid-muted hover:bg-grid-soft",
                )}
              >
                <Icon />
              </button>
            ))}
          </div>
        </SettingsRow>
        <SettingsRow label={t("servers.appearance.logo")} hint={t("servers.appearance.logoHint")}>
          <div
            className="flex flex-wrap items-center gap-3 border border-dashed border-line px-4 py-3"
            onDragOver={(e) => e.preventDefault()}
            onDrop={(e) => {
              e.preventDefault()
              if (manage) void upload(e.dataTransfer.files?.[0])
            }}
          >
            <ServerMark server={{ color, icon, logo_url: logo || null }} size={10} />
            <input
              ref={file}
              type="file"
              accept="image/png,image/jpeg,image/webp"
              className="sr-only"
              aria-label={t("servers.appearance.logo")}
              onChange={(e) => void upload(e.target.files?.[0])}
            />
            <Button type="button" variant="outline" size="sm" onClick={() => file.current?.click()}>
              <UploadIcon />
              {logo ? t("servers.appearance.logoReplace") : t("servers.appearance.logoUpload")}
            </Button>
            {logo ? (
              <Button type="button" variant="ghost" size="sm" onClick={() => void removeLogo()}>
                <Trash2Icon />
                {t("servers.appearance.logoRemove")}
              </Button>
            ) : (
              <span className="text-xs text-grid-muted">{t("servers.appearance.logoDrop")}</span>
            )}
          </div>
        </SettingsRow>
        {manage ? (
          <div className="flex justify-end border-b border-line px-6 py-4">
            <Button type="submit" disabled={busy || !name.trim()}>
              {busy ? t("common.working") : t("servers.appearance.save")}
            </Button>
          </div>
        ) : null}
      </fieldset>
    </form>
  )
}
