"use client"

import { Button, Popover, PopoverContent, PopoverDescription, PopoverTitle, PopoverTrigger, Status } from "@fadymondy/nasaq/web"
import Link from "next/link"
import { ChevronRightIcon, ShieldCheckIcon, TriangleAlertIcon } from "lucide-react"

import { useTlsOf, type Cert, type Health, type Host } from "@/components/domains/data"
import { useI18n } from "@/lib/i18n"
import type { Server } from "@/lib/servers"

/** server → app (or upstream) that a hostname routes to. */
export function HostPath({ host, servers, link = false }: { host: Host; servers: Server[]; link?: boolean }) {
  const server = servers.find((s) => s.id === host.serverId)
  const sep = <ChevronRightIcon aria-hidden className="size-3.5 shrink-0 text-muted-foreground rtl:rotate-180" />
  return (
    <span className="inline-flex min-w-0 items-center gap-1.5 text-body-sm">
      {link && server ? (
        <Link href={`../servers/${server.id}`} className="truncate text-muted-foreground hover:text-foreground hover:underline">
          <bdi dir="ltr">{server.name}</bdi>
        </Link>
      ) : (
        <bdi dir="ltr" className="truncate text-muted-foreground">
          {server?.name ?? "—"}
        </bdi>
      )}
      {sep}
      {link && host.targetHref ? (
        <Link href={host.targetHref} className="truncate font-medium text-foreground hover:underline">
          <bdi dir="ltr">{host.target}</bdi>
        </Link>
      ) : (
        <bdi dir="ltr" className={host.kind === "proxy" ? "truncate font-mono text-foreground" : "truncate font-medium text-foreground"}>
          {host.target}
        </bdi>
      )}
    </span>
  )
}

/**
 * A warning icon that opens the domain's problems in a popover (unverified hostnames with their TXT
 * record, failing or expiring certificates), so the table itself stays clean.
 */
export function DomainIssues({
  hosts,
  certs,
  health,
  manage,
  onVerify,
}: {
  hosts: Host[]
  certs: Map<string, Cert>
  health: (h: Host) => Health
  manage: boolean
  onVerify: (h: Host) => void
}) {
  const { t } = useI18n()
  const tlsOf = useTlsOf(certs)
  const bad = hosts.filter((h) => health(h) === "attention")
  if (!bad.length) return null
  return (
    <span onClick={(e) => e.stopPropagation()} onKeyDown={(e) => e.stopPropagation()}>
      <Popover>
        <PopoverTrigger
          render={
            <button
              type="button"
              aria-label={t("domains2.attention", { n: bad.length })}
              className="inline-flex size-7 items-center justify-center rounded-control text-grid-warn hover:bg-secondary"
            />
          }
        >
          <TriangleAlertIcon aria-hidden className="size-4" />
        </PopoverTrigger>
        <PopoverContent align="start" className="w-96 max-w-[calc(100vw-2rem)]">
          <PopoverTitle>{t("domains2.needsAttention")}</PopoverTitle>
          <PopoverDescription>{t("domains2.attention", { n: bad.length })}</PopoverDescription>
          <ul className="mt-3 flex flex-col gap-3">
            {bad.map((h) => {
              const [tone, label] = tlsOf(h)
              return (
                <li key={h.key} className="flex flex-col gap-2">
                  <span className="flex items-center justify-between gap-2">
                    <bdi dir="ltr" className="truncate font-medium text-foreground">
                      {h.hostname}
                    </bdi>
                    <Status tone={tone}>{label}</Status>
                  </span>
                  {!h.verified ? (
                    <div className="flex flex-col gap-1 rounded-control bg-secondary p-2 text-caption">
                      <span className="text-muted-foreground">{t("domains2.recordHint")}</span>
                      <code dir="ltr" className="break-all text-foreground select-all">
                        TXT {h.verifyName}
                      </code>
                      <code dir="ltr" className="break-all text-foreground select-all">
                        {h.verifyValue}
                      </code>
                      {manage ? (
                        <Button size="sm" variant="secondary" className="mt-1 self-start" onClick={() => onVerify(h)}>
                          <ShieldCheckIcon />
                          {t("proxy.verify")}
                        </Button>
                      ) : null}
                    </div>
                  ) : null}
                </li>
              )
            })}
          </ul>
        </PopoverContent>
      </Popover>
    </span>
  )
}
