"use client"

import Link from "next/link"
import { useEffect, useState, type ReactNode } from "react"
import { Dialog, DialogContent, DialogDescription, DialogTitle, Skeleton, Tabs, TabsList, TabsPanel, TabsTab } from "@fadymondy/nasaq/web"
import { ArrowRightIcon, DownloadIcon, GithubIcon, LayoutTemplateIcon, PackageIcon, PlusIcon, ServerIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import { apiFetch } from "@/lib/api"
import { useI18n } from "@/lib/i18n"
import type { Product } from "@/lib/products"
import type { Server } from "@/lib/servers"
import { useOptionalTeam } from "@/lib/team"

type Recipe = { id: string; name: string; summary: string; kind: "app" | "stack" }

const optional = <T,>(path: string) => apiFetch<T>(path).catch(() => null)

/** A catalogue recipe as a tile: its initial, name, kind and a Deploy action. */
function RecipeTile({ recipe, href, action }: { recipe: Recipe; href: string; action: string }) {
  return (
    <div className="flex flex-col items-center gap-2 rounded-2xl border border-border bg-background/60 p-3 text-center">
      <span className="flex size-10 items-center justify-center rounded-full bg-nq-brand/15 text-body font-semibold text-nq-brand">
        {recipe.name.slice(0, 1)}
      </span>
      <span className="min-w-0">
        <span className="block truncate text-body-sm font-medium text-foreground" dir="auto">
          {recipe.name}
        </span>
        <span className="line-clamp-2 text-caption text-muted-foreground" dir="auto" title={recipe.summary}>
          {recipe.summary}
        </span>
      </span>
      <Button size="sm" variant="outline" className="mt-auto w-full" nativeButton={false} render={<Link href={href} />}>
        {action}
        <ArrowRightIcon className="rtl:-scale-x-100" />
      </Button>
    </div>
  )
}

/** A source tab's body when it is one action rather than a list. */
function SourcePanel({ icon, title, body, href, cta }: { icon: ReactNode; title: string; body: string; href: string; cta: string }) {
  return (
    <div className="flex flex-wrap items-center gap-4 rounded-2xl border border-border bg-background/60 p-4">
      <span className="flex size-10 items-center justify-center rounded-xl bg-secondary text-foreground [&_svg]:size-5">{icon}</span>
      <span className="min-w-0 flex-1">
        <span className="block text-body-sm font-medium text-foreground">{title}</span>
        <span className="block text-caption text-muted-foreground">{body}</span>
      </span>
      <Button size="sm" nativeButton={false} render={<Link href={href} />}>
        {cta}
        <ArrowRightIcon className="rtl:-scale-x-100" />
      </Button>
    </div>
  )
}

/**
 * The header's "Deploy new app": a dialog asking what to deploy, with one tab per source that works today
 * (CircleXO when the team has it, one-click apps, GitHub, templates, bring your own). Sources load on the
 * first open; following any link closes it.
 */
export function DeployButton() {
  const { t, locale } = useI18n()
  const teamCtx = useOptionalTeam()
  const slug = teamCtx?.team.slug
  const manage = teamCtx?.manage ?? false
  const base = `/${locale}/t/${slug}`
  const [open, setOpen] = useState(false)
  const [products, setProducts] = useState<Product[]>([])
  const [recipes, setRecipes] = useState<Recipe[] | null>(null)
  const [servers, setServers] = useState<Server[]>([])

  useEffect(() => setRecipes(null), [slug])

  useEffect(() => {
    if (!open || !slug || recipes) return
    const api = `/api/teams/${slug}`
    void optional<{ products: Product[] }>(`${api}/products`).then((r) => setProducts(r?.products ?? []))
    void optional<{ servers: Server[] }>(`${api}/servers`).then((r) => setServers(r?.servers ?? []))
    void optional<{ recipes: Recipe[] }>(`${api}/catalog`).then((r) => setRecipes(r?.recipes ?? []))
  }, [open, slug, recipes])

  if (!slug) return null

  const circlexo = products.find((p) => /circle\s*xo/i.test(`${p.name} ${p.title ?? ""}`)) ?? null
  const recipeHref = (r: Recipe) => (r.kind === "stack" ? `${base}/apps/new?stack=${encodeURIComponent(r.id)}` : `${base}/apps`)
  const onlineServer = servers.find((s) => s.online) ?? servers[0]

  return (
    <>
      <Button size="sm" onClick={() => setOpen(true)}>
        <PlusIcon />
        <span className="max-sm:sr-only">{t("home3.deployNew")}</span>
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent
          className="flex max-h-[90vh] max-w-3xl flex-col gap-4 overflow-y-auto rounded-2xl"
          onClickCapture={(e) => {
            if ((e.target as HTMLElement).closest("a")) setOpen(false)
          }}
        >
          <div>
            <DialogTitle>{t("home2.deployTitle")}</DialogTitle>
            <DialogDescription>{t("home2.deployHint")}</DialogDescription>
          </div>
          <Tabs defaultValue="catalog" className="gap-4">
            <TabsList variant="underline" className="border-border">
              {circlexo ? <TabsTab value="circlexo">CircleXO</TabsTab> : null}
              <TabsTab value="catalog">{t("home3.tabCatalog")}</TabsTab>
              <TabsTab value="git">{t("home3.tabGit")}</TabsTab>
              <TabsTab value="template">{t("home3.tabTemplate")}</TabsTab>
              <TabsTab value="byo">{t("home3.tabByo")}</TabsTab>
            </TabsList>
            {circlexo ? (
              <TabsPanel value="circlexo">
                <SourcePanel
                  icon={<PackageIcon />}
                  title={circlexo.title || circlexo.name}
                  body={t("home2.productBody")}
                  href={`${base}/products`}
                  cta={t("home3.deploy")}
                />
              </TabsPanel>
            ) : null}
            <TabsPanel value="catalog">
              {recipes === null ? (
                <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-4">
                  {[0, 1, 2, 3].map((i) => (
                    <Skeleton key={i} className="h-40 rounded-2xl" />
                  ))}
                </div>
              ) : (
                <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-4">
                  {recipes.slice(0, 8).map((r) => (
                    <RecipeTile
                      key={r.id}
                      recipe={r}
                      href={recipeHref(r)}
                      action={r.kind === "stack" ? t("home3.deploy") : t("home3.addToSite")}
                    />
                  ))}
                </div>
              )}
            </TabsPanel>
            <TabsPanel value="git">
              <SourcePanel
                icon={<GithubIcon />}
                title={t("home2.fromGit")}
                body={t("home2.fromGitBody")}
                href={`${base}/apps/new`}
                cta={t("home3.deploy")}
              />
            </TabsPanel>
            <TabsPanel value="template">
              <SourcePanel
                icon={<LayoutTemplateIcon />}
                title={t("home2.fromTemplate")}
                body={t("home2.fromTemplateBody")}
                href={`${base}/templates`}
                cta={t("home3.browse")}
              />
            </TabsPanel>
            <TabsPanel value="byo" className="flex flex-col gap-3">
              {onlineServer ? (
                <SourcePanel
                  icon={<DownloadIcon />}
                  title={t("home2.fromServer")}
                  body={t("home2.fromServerBody")}
                  href={`${base}/servers/${onlineServer.id}/settings#import`}
                  cta={t("home3.browse")}
                />
              ) : null}
              {manage ? (
                <SourcePanel
                  icon={<ServerIcon />}
                  title={t("home2.byo")}
                  body={t("home2.byoBody")}
                  href={`${base}/servers/new`}
                  cta={t("servers.add")}
                />
              ) : null}
            </TabsPanel>
          </Tabs>
        </DialogContent>
      </Dialog>
    </>
  )
}
