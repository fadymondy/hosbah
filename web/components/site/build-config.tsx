"use client"

import { useEffect, useState } from "react"
import { PlusIcon, Trash2Icon } from "lucide-react"
import { toast } from "sonner"

import { useErrorText } from "@/components/form-error"
import { useLoadedSite } from "@/components/site/context"
import { ErrorBand, LoadingRows, Notice, SectionTitle } from "@/components/states"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { apiFetch } from "@/lib/api"
import { useI18n } from "@/lib/i18n"

/** Mirrors internal/api BuildConfigOut (MH-579). */
export type BuildConfig = {
  dockerfile_path: string
  build_context: string
  build_env: string[]
  can_save: boolean
}

type Draft = { dockerfilePath: string; buildContext: string; buildEnv: string[] }
const draftOf = (c: BuildConfig): Draft => ({ dockerfilePath: c.dockerfile_path, buildContext: c.build_context, buildEnv: [...c.build_env] })
const same = (a: Draft, b: Draft) =>
  a.dockerfilePath === b.dockerfilePath && a.buildContext === b.buildContext && JSON.stringify(a.buildEnv) === JSON.stringify(b.buildEnv)

/** A relative path: no "..", no leading "/", a restricted charset (mirrors agentproto.ValidBuildPath). */
const BUILD_PATH_RE = /^[A-Za-z0-9._/-]{1,200}$/
const validPath = (s: string) => s === "" || (BUILD_PATH_RE.test(s) && !s.includes("..") && !s.startsWith("/"))

/** An env var name: letters, digits, underscore, not starting with a digit. */
const ENV_NAME_RE = /^[A-Za-z_][A-Za-z0-9_]{0,127}$/

/**
 * An app's Dockerfile path, build context, and which of its env vars are exposed at build time
 * (MH-579): the Dockerfile/build gaps found migrating production apps to Hosbah deploys.
 */
export function SiteBuildConfig() {
  const { t } = useI18n()
  const errorText = useErrorText()
  const { path } = useLoadedSite()
  const configPath = `${path}/deploy-config/build`

  const [config, setConfig] = useState<BuildConfig | null>(null)
  const [draft, setDraft] = useState<Draft | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [newVar, setNewVar] = useState("")

  useEffect(() => {
    setConfig(null)
    setDraft(null)
    setError(null)
    apiFetch<BuildConfig>(configPath)
      .then((c) => {
        setConfig(c)
        setDraft(draftOf(c))
      })
      .catch((err) => setError(errorText(err)))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [configPath])

  const dirty = !!config && !!draft && !same(draft, draftOf(config))
  const canSave = config?.can_save ?? false
  const dockerfileOk = !!draft && validPath(draft.dockerfilePath)
  const contextOk = !!draft && validPath(draft.buildContext)
  const newVarOk = !newVar || ENV_NAME_RE.test(newVar)
  const newVarTaken = !!draft && draft.buildEnv.includes(newVar)

  function removeVar(name: string) {
    setDraft((d) => (d ? { ...d, buildEnv: d.buildEnv.filter((n) => n !== name) } : d))
  }

  function addVar() {
    if (!newVar || !newVarOk || newVarTaken) return
    setDraft((d) => (d ? { ...d, buildEnv: [...d.buildEnv, newVar] } : d))
    setNewVar("")
  }

  function discard() {
    if (config) setDraft(draftOf(config))
  }

  async function save() {
    if (!draft || !dockerfileOk || !contextOk) return
    setBusy(true)
    setError(null)
    try {
      const r = await apiFetch<BuildConfig>(configPath, {
        method: "PUT",
        body: { dockerfile_path: draft.dockerfilePath, build_context: draft.buildContext, build_env: draft.buildEnv },
      })
      setConfig(r)
      setDraft(draftOf(r))
      toast.success(t("site.build.saved"))
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section aria-labelledby="build-h">
      <SectionTitle id="build-h" title={t("site.build.title")} hint={t("site.build.hint")} />
      <ErrorBand message={error} />
      {!config || !draft ? (
        <LoadingRows rows={3} />
      ) : (
        <>
          {!canSave ? <Notice>{t("common.readOnly")}</Notice> : null}
          <fieldset disabled={!canSave || busy} className="contents">
            <div className="border-b border-line px-6 py-5">
              <label className="mb-1 block text-sm font-medium text-grid-fg">{t("site.build.dockerfilePath")}</label>
              <p className="mb-3 max-w-xl text-xs text-grid-muted">{t("site.build.dockerfilePathHint")}</p>
              <Input
                dir="ltr"
                maxLength={200}
                placeholder="Dockerfile"
                value={draft.dockerfilePath}
                aria-invalid={!dockerfileOk}
                onChange={(e) => setDraft((d) => (d ? { ...d, dockerfilePath: e.target.value } : d))}
                className="w-full font-mono text-xs sm:w-96"
              />
              {!dockerfileOk ? <p className="mt-1 text-xs text-destructive">{t("site.build.invalidPath")}</p> : null}
            </div>

            <div className="border-b border-line px-6 py-5">
              <label className="mb-1 block text-sm font-medium text-grid-fg">{t("site.build.buildContext")}</label>
              <p className="mb-3 max-w-xl text-xs text-grid-muted">{t("site.build.buildContextHint")}</p>
              <Input
                dir="ltr"
                maxLength={200}
                placeholder="."
                value={draft.buildContext}
                aria-invalid={!contextOk}
                onChange={(e) => setDraft((d) => (d ? { ...d, buildContext: e.target.value } : d))}
                className="w-full font-mono text-xs sm:w-96"
              />
              {!contextOk ? <p className="mt-1 text-xs text-destructive">{t("site.build.invalidPath")}</p> : null}
            </div>

            <div className="flex flex-col gap-3 px-6 py-5">
              <p className="text-sm font-medium text-grid-fg">{t("site.build.buildEnv")}</p>
              <p className="max-w-xl text-xs text-grid-muted">{t("site.build.buildEnvHint")}</p>

              {draft.buildEnv.length === 0 ? (
                <p className="py-2 text-sm text-grid-muted">{t("site.build.buildEnvEmpty")}</p>
              ) : (
                <div className="flex flex-col gap-2">
                  {[...draft.buildEnv].sort().map((name) => (
                    <div key={name} className="flex flex-wrap items-center gap-2">
                      <span dir="ltr" className="w-full font-mono text-xs text-grid-fg sm:w-56 sm:shrink-0">
                        {name}
                      </span>
                      <Button type="button" variant="ghost" size="icon-sm" onClick={() => removeVar(name)} aria-label={t("site.build.removeVar")}>
                        <Trash2Icon />
                      </Button>
                    </div>
                  ))}
                </div>
              )}

              <div className="flex flex-wrap items-end gap-2 border-t border-line pt-4">
                <div>
                  <label className="mb-1 block text-xs font-medium text-grid-fg">{t("site.build.varName")}</label>
                  <Input
                    dir="ltr"
                    maxLength={128}
                    value={newVar}
                    aria-invalid={!newVarOk}
                    onChange={(e) => setNewVar(e.target.value)}
                    onKeyDown={(e) => {
                      if (e.key === "Enter") {
                        e.preventDefault()
                        addVar()
                      }
                    }}
                    className="w-full sm:w-56 font-mono text-xs"
                  />
                </div>
                <Button type="button" variant="outline" size="sm" disabled={!canSave || busy || !newVar || !newVarOk || newVarTaken} onClick={addVar}>
                  <PlusIcon />
                  {t("site.build.addVar")}
                </Button>
              </div>
              {!newVarOk ? <p className="text-xs text-destructive">{t("site.build.invalidVarName")}</p> : null}
            </div>
          </fieldset>
        </>
      )}

      {canSave && dirty ? (
        <div className="sticky bottom-0 z-20 flex items-center justify-between gap-3 border-t border-line bg-popover/95 px-6 py-3 backdrop-blur supports-backdrop-filter:bg-popover/80">
          <p className="text-xs text-grid-muted">{t("site.deployScript.unsaved")}</p>
          <div className="flex gap-2">
            <Button type="button" variant="outline" size="sm" disabled={busy} onClick={discard}>
              {t("common.discard")}
            </Button>
            <Button type="button" size="sm" disabled={busy || !dockerfileOk || !contextOk} onClick={() => void save()}>
              {busy ? t("common.working") : t("common.save")}
            </Button>
          </div>
        </div>
      ) : null}
    </section>
  )
}
