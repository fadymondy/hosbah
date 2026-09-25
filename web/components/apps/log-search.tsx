"use client"

import { useState, type FormEvent, type ReactNode } from "react"
import { ClockIcon, CopyIcon, SearchIcon } from "lucide-react"
import { toast } from "sonner"

import { useErrorText } from "@/components/form-error"
import { ErrorBand } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia } from "@/components/ui/empty"
import { Field, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { apiFetch } from "@/lib/api"
import { useI18n } from "@/lib/i18n"
import { cn } from "@/lib/utils"

type LogSearchLine = {
  at: string
  process: string
  stream: string
  level?: string
  line: string
}

type LogSearchResult = {
  lines: LogSearchLine[]
  truncated: boolean
  searched_containers?: string[]
}

type LogSearchOut = {
  operation: { id: string; status: string }
  result: LogSearchResult | null
}

const RANGE_PRESETS = ["15m", "1h", "6h", "24h", "7d"] as const
type Range = (typeof RANGE_PRESETS)[number]

/** Yesterday's line is "1h" ago the same way it always was: a bare duration the API resolves itself. */
function windowAround(at: string) {
  const t = new Date(at).getTime()
  return { since: new Date(t - 30_000).toISOString(), until: new Date(t + 30_000).toISOString() }
}

function escapeRegExp(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")
}

function clock(at: string): string {
  const d = new Date(at)
  return Number.isNaN(d.getTime()) ? "" : d.toISOString().slice(11, 19)
}

/**
 * H4: log search — a query (plain substring or, with Regex on, an RE2 pattern), a process and level
 * filter, and a canned time-range, over the app's current containers. Matches are highlighted;
 * clicking a line copies it; "show context" re-searches a tight ±30s window around it with no query,
 * to see what happened just before and after.
 */
export function LogSearchPanel({ path, processes }: { path: string; processes: string[] }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const names = processes.length ? processes : ["web"]

  const [query, setQuery] = useState("")
  const [regex, setRegex] = useState(false)
  const [caseInsensitive, setCaseInsensitive] = useState(false)
  const [process, setProcess] = useState("any")
  const [level, setLevel] = useState("any")
  const [range, setRange] = useState<Range>("1h")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [result, setResult] = useState<LogSearchResult | null>(null)
  const [searched, setSearched] = useState(false)

  const [contextFor, setContextFor] = useState<number | null>(null)
  const [contextLines, setContextLines] = useState<LogSearchLine[] | null>(null)
  const [contextBusy, setContextBusy] = useState(false)

  async function search(e?: FormEvent) {
    e?.preventDefault()
    setBusy(true)
    setError(null)
    setSearched(true)
    setContextFor(null)
    try {
      const params = new URLSearchParams({
        q: query,
        regex: String(regex),
        case_insensitive: String(caseInsensitive),
        since: range,
        wait: "45",
        limit: "200",
      })
      if (process !== "any") params.set("process", process)
      if (level !== "any") params.set("level", level)
      const out = await apiFetch<LogSearchOut>(`${path}/logs/search?${params}`)
      setResult(out.result)
    } catch (err) {
      setError(errorText(err))
      setResult(null)
    } finally {
      setBusy(false)
    }
  }

  async function copy(l: LogSearchLine) {
    try {
      await navigator.clipboard.writeText(l.line)
      toast.success(t("apps.logs.search.copied"))
    } catch {
      // Clipboard can be refused (insecure origin); nothing more to do.
    }
  }

  async function showContext(l: LogSearchLine, idx: number) {
    if (contextFor === idx) {
      setContextFor(null)
      return
    }
    setContextFor(idx)
    setContextLines(null)
    setContextBusy(true)
    try {
      const { since, until } = windowAround(l.at)
      const params = new URLSearchParams({ q: "", regex: "false", since, until, wait: "30", limit: "200" })
      if (l.process) params.set("process", l.process)
      const out = await apiFetch<LogSearchOut>(`${path}/logs/search?${params}`)
      setContextLines(out.result?.lines ?? [])
    } catch {
      setContextLines([])
    } finally {
      setContextBusy(false)
    }
  }

  function highlight(text: string): ReactNode {
    if (!query) return text
    let re: RegExp
    try {
      re = new RegExp(regex ? query : escapeRegExp(query), caseInsensitive ? "gi" : "g")
    } catch {
      return text
    }
    const parts: ReactNode[] = []
    let last = 0
    let n = 0
    let m: RegExpExecArray | null
    while (n < 500 && (m = re.exec(text))) {
      if (m[0] === "") {
        re.lastIndex++
        continue
      }
      if (m.index > last) parts.push(text.slice(last, m.index))
      parts.push(
        <mark key={m.index} className="rounded-sm bg-yellow-300/70 px-0.5 text-foreground dark:bg-yellow-400/30">
          {m[0]}
        </mark>,
      )
      last = m.index + m[0].length
      n++
    }
    parts.push(text.slice(last))
    return parts
  }

  function rangeLabel(r: string) {
    return t(`apps.logs.search.ranges.${r}`, undefined, r)
  }
  function levelLabel(v: string) {
    if (v === "error") return t("apps.logs.search.levelError")
    if (v === "warn") return t("apps.logs.search.levelWarn")
    return t("apps.logs.search.anyLevel")
  }

  return (
    <div data-testid="log-search">
      <form onSubmit={search} className="flex flex-wrap items-end gap-3 border-b border-line px-6 py-4">
        <Field className="min-w-56 flex-1 basis-64">
          <FieldLabel htmlFor="ls-q">{t("apps.logs.search.query")}</FieldLabel>
          <Input id="ls-q" dir="ltr" value={query} onChange={(e) => setQuery(e.target.value)} placeholder={t("apps.logs.search.placeholder")} />
        </Field>
        <Field className="w-40">
          <FieldLabel htmlFor="ls-process">{t("apps.logs.process")}</FieldLabel>
          <Select value={process} onValueChange={(v) => v && setProcess(v as string)}>
            <SelectTrigger id="ls-process" className="w-full">
              <SelectValue>{(v: string) => <span dir="ltr">{v === "any" ? t("apps.logs.search.anyProcess") : v}</span>}</SelectValue>
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="any">{t("apps.logs.search.anyProcess")}</SelectItem>
              {names.map((n) => (
                <SelectItem key={n} value={n}>
                  <span dir="ltr">{n}</span>
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        <Field className="w-36">
          <FieldLabel htmlFor="ls-level">{t("apps.logs.search.level")}</FieldLabel>
          <Select value={level} onValueChange={(v) => v && setLevel(v as string)}>
            <SelectTrigger id="ls-level" className="w-full">
              <SelectValue>{(v: string) => <span>{levelLabel(v)}</span>}</SelectValue>
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="any">{t("apps.logs.search.anyLevel")}</SelectItem>
              <SelectItem value="error">{t("apps.logs.search.levelError")}</SelectItem>
              <SelectItem value="warn">{t("apps.logs.search.levelWarn")}</SelectItem>
            </SelectContent>
          </Select>
        </Field>
        <Field className="w-36">
          <FieldLabel htmlFor="ls-range">{t("apps.logs.search.range")}</FieldLabel>
          <Select value={range} onValueChange={(v) => v && setRange(v as Range)}>
            <SelectTrigger id="ls-range" className="w-full">
              <SelectValue>{(v: string) => <span dir="ltr">{rangeLabel(v)}</span>}</SelectValue>
            </SelectTrigger>
            <SelectContent>
              {RANGE_PRESETS.map((r) => (
                <SelectItem key={r} value={r}>
                  <span dir="ltr">{rangeLabel(r)}</span>
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        <div className="flex flex-wrap items-center gap-4">
          <label className="flex items-center gap-2 text-sm">
            <Checkbox checked={regex} onCheckedChange={(v) => setRegex(v === true)} />
            {t("apps.logs.search.regex")}
          </label>
          <label className="flex items-center gap-2 text-sm">
            <Checkbox checked={caseInsensitive} onCheckedChange={(v) => setCaseInsensitive(v === true)} />
            {t("apps.logs.search.caseInsensitive")}
          </label>
        </div>
        <Button type="submit" disabled={busy}>
          <SearchIcon />
          {busy ? t("common.working") : t("apps.logs.search.run")}
        </Button>
      </form>
      <ErrorBand message={error} />
      <div className="min-h-40 border-b border-line bg-grid-card">
        {!searched ? (
          <Empty>
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <SearchIcon />
              </EmptyMedia>
              <EmptyDescription>{t("apps.logs.search.empty")}</EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : result === null ? (
          <p className="px-6 py-6 text-sm text-grid-muted">{busy ? t("common.working") : t("apps.logs.search.timedOut")}</p>
        ) : result.lines.length === 0 ? (
          <p className="px-6 py-6 text-sm text-grid-muted">{t("apps.logs.search.noMatches")}</p>
        ) : (
          <>
            {result.truncated ? <p className="border-b border-line px-6 py-2 text-xs text-grid-muted">{t("apps.logs.search.truncated")}</p> : null}
            <ul>
              {result.lines.map((l, idx) => (
                <li key={idx} className="border-b border-line px-6 py-2 text-xs">
                  <div className="flex items-start gap-3">
                    <span dir="ltr" className="shrink-0 pt-0.5 text-grid-muted tabular-nums">
                      {clock(l.at)}
                    </span>
                    <span dir="ltr" className="shrink-0 pt-0.5 text-grid-muted">
                      {l.process}
                    </span>
                    {l.level ? <Badge variant={l.level === "error" ? "destructive" : "outline"}>{l.level}</Badge> : null}
                    <pre dir="ltr" className="min-w-0 flex-1 text-start font-mono whitespace-pre-wrap break-all">
                      {highlight(l.line)}
                    </pre>
                    <div className="flex shrink-0 gap-1">
                      <Button type="button" variant="ghost" size="icon-sm" onClick={() => copy(l)} aria-label={t("common.copy")} title={t("common.copy")}>
                        <CopyIcon />
                      </Button>
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon-sm"
                        onClick={() => showContext(l, idx)}
                        aria-label={t("apps.logs.search.context")}
                        title={t("apps.logs.search.context")}
                        aria-pressed={contextFor === idx}
                      >
                        <ClockIcon />
                      </Button>
                    </div>
                  </div>
                  {contextFor === idx ? (
                    <div className="mt-2 ms-14 border-s-2 border-line ps-3">
                      {contextBusy ? (
                        <p className="text-grid-muted">{t("common.working")}</p>
                      ) : contextLines && contextLines.length ? (
                        contextLines.map((c, ci) => (
                          <div
                            key={ci}
                            dir="ltr"
                            className={cn(
                              "text-start font-mono text-[11px] whitespace-pre-wrap break-all",
                              c.at === l.at && c.line === l.line ? "rounded-sm bg-yellow-300/20 px-1" : undefined,
                            )}
                          >
                            <span className="text-grid-muted">{clock(c.at)} </span>
                            {c.line}
                          </div>
                        ))
                      ) : (
                        <p className="text-grid-muted">{t("apps.logs.search.noContext")}</p>
                      )}
                    </div>
                  ) : null}
                </li>
              ))}
            </ul>
          </>
        )}
      </div>
    </div>
  )
}
