"use client"

import { Button, Input, Textarea } from "@fadymondy/nasaq/web"
import { useEffect, useState, type FormEvent } from "react"
import { toast } from "sonner"

import { ListError, LoadingRows } from "@/components/admin/admin-list"
import { PageBody } from "@/components/app-shell"
import { useErrorText } from "@/components/form-error"
import { ErrorBand, SectionTitle, SettingsRow } from "@/components/states"



import { useAdminResource, type PlatformSettings, type SignupMode } from "@/lib/admin"
import { apiFetch } from "@/lib/api"
import { useDocumentTitle, useFormatDate, useI18n } from "@/lib/i18n"

const SIGNUP_MODES: SignupMode[] = ["open", "invite_only", "closed"]

/** Platform settings: who can sign up, the announcement every user sees, support, and limits. */
export default function AdminSettingsPage() {
  const { t } = useI18n()
  const fmt = useFormatDate()
  const errorText = useErrorText()
  useDocumentTitle(t("admin.nav.settings"))
  const res = useAdminResource<PlatformSettings>("/api/admin/settings")
  const [form, setForm] = useState<PlatformSettings | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (res.data) setForm(res.data)
  }, [res.data])

  function set<K extends keyof PlatformSettings>(key: K, value: PlatformSettings[K]) {
    setForm((f) => (f ? { ...f, [key]: value } : f))
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    if (!form) return
    setBusy(true)
    setError(null)
    try {
      await apiFetch("/api/admin/settings", {
        method: "PUT",
        body: {
          signup_mode: form.signup_mode,
          announcement: form.announcement.trim(),
          announcement_level: form.announcement_level,
          support_email: form.support_email.trim(),
          max_teams_per_user: Number(form.max_teams_per_user) || 0,
          max_servers_per_team: Number(form.max_servers_per_team) || 0,
        },
      })
      toast.success(t("admin.settings.saved"))
      await res.reload()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  const dirty = !!form && !!res.data && JSON.stringify(form) !== JSON.stringify(res.data)

  return (
    <PageBody eyebrow={t("nav.platform")} title={t("admin.nav.settings")} description={t("admin.settings.hint")} bleed>
      {res.error ? (
        <ListError error={res.error} />
      ) : !form ? (
        <LoadingRows rows={4} />
      ) : (
        <form onSubmit={submit}>
          <ErrorBand message={error} />

          <SectionTitle title={t("admin.settings.access")} hint={t("admin.settings.accessHint")} />
          <SettingsRow label={t("admin.settings.signupMode")} hint={t(`admin.settings.signup.${form.signup_mode}Hint`)}>
            <div role="radiogroup" aria-label={t("admin.settings.signupMode")} className="grid gap-px border border-border bg-border sm:grid-cols-3">
              {SIGNUP_MODES.map((m) => {
                const on = form.signup_mode === m
                return (
                  <button
                    key={m}
                    type="button"
                    role="radio"
                    aria-checked={on}
                    onClick={() => set("signup_mode", m)}
                    className={
                      on
                        ? "bg-secondary px-3 py-2 text-sm font-medium text-foreground shadow-[inset_0_-2px_0_var(--nq-action)]"
                        : "bg-background px-3 py-2 text-sm text-nq-fg-body hover:bg-secondary"
                    }
                  >
                    {t(`admin.settings.signup.${m}`)}
                  </button>
                )
              })}
            </div>
          </SettingsRow>

          <SectionTitle title={t("admin.settings.announcementTitle")} hint={t("admin.settings.announcementHint")} />
          <SettingsRow label={t("admin.settings.announcement")} htmlFor="announcement">
            <Textarea
              id="announcement"
              rows={3}
              maxLength={500}
              value={form.announcement}
              onChange={(e) => set("announcement", e.target.value)}
              placeholder={t("admin.settings.announcementPlaceholder")}
            />
            <span className="text-xs text-muted-foreground tabular-nums">{form.announcement.length}/500</span>
          </SettingsRow>
          <SettingsRow label={t("admin.settings.level")}>
            <div role="radiogroup" aria-label={t("admin.settings.level")} className="flex gap-2">
              {(["info", "warning"] as const).map((lvl) => (
                <Button
                  key={lvl}
                  type="button"
                  variant={form.announcement_level === lvl ? "primary" : "secondary"}
                  role="radio"
                  aria-checked={form.announcement_level === lvl}
                  onClick={() => set("announcement_level", lvl)}
                >
                  {t(`admin.settings.levels.${lvl}`)}
                </Button>
              ))}
            </div>
          </SettingsRow>

          <SectionTitle title={t("admin.settings.supportTitle")} />
          <SettingsRow label={t("admin.settings.supportEmail")} htmlFor="support-email" hint={t("admin.settings.supportHint")}>
            <Input
              id="support-email"
              type="email"
              dir="ltr"
              value={form.support_email}
              onChange={(e) => set("support_email", e.target.value)}
              placeholder="support@hosbah.com"
            />
          </SettingsRow>

          <SectionTitle title={t("admin.settings.limitsTitle")} hint={t("admin.settings.limitsHint")} />
          <SettingsRow label={t("admin.settings.maxTeams")} htmlFor="max-teams">
            <Input
              id="max-teams"
              type="number"
              min={0}
              dir="ltr"
              className="w-32 font-mono"
              value={form.max_teams_per_user}
              onChange={(e) => set("max_teams_per_user", Number(e.target.value))}
            />
          </SettingsRow>
          <SettingsRow label={t("admin.settings.maxServers")} htmlFor="max-servers">
            <Input
              id="max-servers"
              type="number"
              min={0}
              dir="ltr"
              className="w-32 font-mono"
              value={form.max_servers_per_team}
              onChange={(e) => set("max_servers_per_team", Number(e.target.value))}
            />
          </SettingsRow>

          <div className="flex flex-wrap items-center justify-between gap-4 border-b border-border px-6 py-4">
            <span className="text-xs text-muted-foreground">
              {res.data?.updated_at ? (
                <>
                  {t("admin.settings.updated")} <span dir="ltr">{fmt(res.data.updated_at)}</span>
                </>
              ) : null}
            </span>
            <Button variant="primary" type="submit" disabled={busy || !dirty}>
              {busy ? t("common.working") : t("common.save")}
            </Button>
          </div>
        </form>
      )}
    </PageBody>
  )
}
