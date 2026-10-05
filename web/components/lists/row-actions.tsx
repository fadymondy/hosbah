"use client"

import {
  Button,
  ContextMenuActions,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
  groupActions,
  type ContextMenuAction,
} from "@fadymondy/nasaq/web"
import { Fragment, isValidElement, type ReactElement, type ReactNode } from "react"
import { MoreHorizontalIcon } from "lucide-react"

import { useI18n } from "@/lib/i18n"

export type RowAction = ContextMenuAction

/** The ⋯ button at a row's inline end: the row's actions as a menu, grouped, the dangerous ones in red. */
export function RowMenu({ actions, label }: { actions: readonly RowAction[]; label?: string }) {
  const { t } = useI18n()
  if (actions.length === 0) return null
  return (
    <DropdownMenu>
      <DropdownMenuTrigger render={<Button variant="ghost" size="icon-sm" aria-label={label ?? t("common.actions")} />}>
        <MoreHorizontalIcon />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-44">
        {groupActions(actions).map((group, i) => (
          <Fragment key={i}>
            {i > 0 ? <DropdownMenuSeparator /> : null}
            <DropdownMenuGroup>
              {group.map((a) => {
                const Icon = a.icon
                return (
                  <DropdownMenuItem key={a.id} variant={a.danger ? "danger" : "default"} disabled={a.disabled} onClick={a.onSelect}>
                    {isValidElement(Icon) ? Icon : Icon ? <Icon /> : null}
                    {a.label}
                  </DropdownMenuItem>
                )
              })}
            </DropdownMenuGroup>
          </Fragment>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

/**
 * A list row (an `<li>`, a card) with the same actions on context-click, long-press and Shift+F10, and the ⋯ menu
 * placed by the caller with RowMenu. `render` is the row element itself.
 */
export function RowWithActions({ actions, render, children }: { actions: readonly RowAction[]; render: ReactElement; children: ReactNode }) {
  return (
    <ContextMenuActions actions={actions} render={render}>
      {children}
    </ContextMenuActions>
  )
}
