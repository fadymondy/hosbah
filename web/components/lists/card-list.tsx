"use client"

import {
  Badge,
  Button,
  DataTableToolbar,
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
  Input,
} from "@fadymondy/nasaq/web"
import { useEffect, useRef, useState, type ReactNode } from "react"
import { ArrowDownUpIcon, ListFilterIcon, SearchIcon, XIcon } from "lucide-react"

import { useI18n } from "@/lib/i18n"
import { cn } from "@/lib/utils"

export type Facet = { id: string; title: string; options: { value: string; label: string; count?: number }[] }

/**
 * The toolbar above a card list, in Nasaq's EntityList style: a search box, one dashed filter menu
 * per facet (multi-select, the picked count as a badge), "Clear filters", and a sort menu at the end.
 */
export function CardListToolbar({
  q,
  onQ,
  placeholder,
  facets = [],
  chosen,
  onChosen,
  sort,
  sorts,
  onSort,
  children,
}: {
  q: string
  onQ: (q: string) => void
  placeholder: string
  facets?: Facet[]
  chosen: Record<string, string[]>
  onChosen: (next: Record<string, string[]>) => void
  sort: string
  sorts: [string, string][]
  onSort: (sort: string) => void
  children?: ReactNode
}) {
  const { t } = useI18n()
  const filtered = !!q || facets.some((f) => (chosen[f.id] ?? []).length > 0)
  return (
    <DataTableToolbar>
      <div className="relative w-full sm:w-64">
        <SearchIcon aria-hidden className="pointer-events-none absolute start-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
        <Input
          type="search"
          value={q}
          onChange={(e) => onQ(e.currentTarget.value)}
          onKeyDown={(e) => {
            if (e.key === "Escape" && q) {
              e.preventDefault()
              onQ("")
            }
          }}
          placeholder={placeholder}
          aria-label={placeholder}
          className="h-control-sm ps-8 pe-8 text-body-sm [&::-webkit-search-cancel-button]:hidden"
        />
        {q ? (
          <button
            type="button"
            onClick={() => onQ("")}
            aria-label={t("lists.clearSearch")}
            className="absolute end-1.5 top-1/2 inline-flex size-5 -translate-y-1/2 items-center justify-center rounded-full text-muted-foreground outline-none hover:bg-nq-hover hover:text-foreground focus-visible:outline-2 focus-visible:outline-nq-focus"
          >
            <XIcon className="size-3.5" />
          </button>
        ) : null}
      </div>
      {facets.map((f) => (
        <FacetMenu key={f.id} facet={f} chosen={chosen[f.id] ?? []} onChange={(v) => onChosen({ ...chosen, [f.id]: v })} />
      ))}
      {filtered ? (
        <Button
          size="sm"
          variant="ghost"
          onClick={() => {
            onQ("")
            onChosen({})
          }}
        >
          <XIcon aria-hidden />
          {t("lists.clearFilters")}
        </Button>
      ) : null}
      <div className="ms-auto flex flex-wrap items-center gap-2">
        {children}
        <DropdownMenu>
          <DropdownMenuTrigger render={<Button size="sm" />}>
            <ArrowDownUpIcon aria-hidden />
            {sorts.find(([v]) => v === sort)?.[1] ?? t("lists.sort")}
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="min-w-44">
            <DropdownMenuGroup>
              <DropdownMenuLabel>{t("lists.sortBy")}</DropdownMenuLabel>
              <DropdownMenuRadioGroup value={sort} onValueChange={(v) => onSort(String(v))}>
                {sorts.map(([v, label]) => (
                  <DropdownMenuRadioItem key={v} value={v}>
                    {label}
                  </DropdownMenuRadioItem>
                ))}
              </DropdownMenuRadioGroup>
            </DropdownMenuGroup>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </DataTableToolbar>
  )
}

function FacetMenu({ facet, chosen, onChange }: { facet: Facet; chosen: string[]; onChange: (v: string[]) => void }) {
  const { t } = useI18n()
  const set = new Set(chosen)
  const toggle = (value: string) => {
    const next = new Set(set)
    if (!next.delete(value)) next.add(value)
    onChange(facet.options.map((o) => o.value).filter((v) => next.has(v)))
  }
  return (
    <DropdownMenu>
      <DropdownMenuTrigger render={<Button size="sm" className={cn(!set.size && "border-dashed text-muted-foreground")} />}>
        <ListFilterIcon aria-hidden />
        {facet.title}
        {set.size ? (
          <Badge variant="outline" className="-me-1 tabular-nums">
            {set.size === 1 ? facet.options.find((o) => set.has(o.value))?.label : set.size}
          </Badge>
        ) : null}
      </DropdownMenuTrigger>
      <DropdownMenuContent className="max-h-80 min-w-48 overflow-y-auto">
        <DropdownMenuGroup>
          {facet.options.map((o) => (
            <DropdownMenuCheckboxItem key={o.value} checked={set.has(o.value)} onCheckedChange={() => toggle(o.value)} closeOnClick={false}>
              <span className="flex-1" dir="auto">
                {o.label}
              </span>
              {o.count !== undefined ? <span className="text-caption tabular-nums text-muted-foreground">{o.count}</span> : null}
            </DropdownMenuCheckboxItem>
          ))}
        </DropdownMenuGroup>
        {set.size ? (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuItem onClick={() => onChange([])}>{t("lists.reset")}</DropdownMenuItem>
          </>
        ) : null}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

/**
 * Infinite scroll over an in-memory list: the first `page` items, then `page` more each time the
 * sentinel (render it after the grid) nears the viewport. Starts over when `reset` changes.
 */
export function useInfinite<T>(items: T[], reset: unknown, page = 20) {
  const [n, setN] = useState(page)
  const sentinel = useRef<HTMLDivElement>(null)
  useEffect(() => setN(page), [reset, page])
  useEffect(() => {
    const el = sentinel.current
    if (!el) return
    const io = new IntersectionObserver((entries) => entries.some((e) => e.isIntersecting) && setN((x) => x + page), {
      rootMargin: "400px",
    })
    io.observe(el)
    return () => io.disconnect()
  }, [items.length, n, page])
  return { visible: items.slice(0, n), more: n < items.length, sentinel }
}
