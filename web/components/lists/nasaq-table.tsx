"use client"

import {
  DataTable,
  DataTableFacetFilter,
  DataTableSearch,
  DataTableToolbar,
  useDataTable,
  type DataTableCellEditResult,
  type DataTableCellValue,
  type DataTableColumn,
  type DataTableRowAction,
  type DataTableSort,
} from "@fadymondy/nasaq/web"
import { useCallback, useState, type ReactNode } from "react"

import { LIST_STEP, LoadMore } from "@/components/lists/infinite"
import { useCardMode } from "@/components/states"

export type TableFacet = { column: string; title: string; options: { value: string; label: string }[] }

/**
 * One Nasaq list: toolbar (search, facet filters, extra controls) and the DataTable, showing `pageSize` rows
 * and more as the end scrolls into view. A server-paged list passes `hasMore` and `onLoadMore` too.
 * The search box shows when a column has a searchValue; facets with fewer than two options are left out.
 */
export function NasaqTable<T>({
  data,
  columns,
  rowId,
  label,
  rowLabel,
  search,
  facets = [],
  toolbar,
  rowActions,
  onRowClick,
  onCellEdit,
  empty,
  pageSize = LIST_STEP,
  defaultSort,
  hasMore,
  onLoadMore,
  loadingMore,
  density,
}: {
  data: T[]
  columns: DataTableColumn<T>[]
  rowId: (row: T) => string
  label: string
  rowLabel: (row: T) => string
  search?: string
  facets?: TableFacet[]
  toolbar?: ReactNode
  rowActions?: (row: T) => DataTableRowAction[]
  onRowClick?: (row: T) => void
  onCellEdit?: (row: T, columnId: string, value: DataTableCellValue) => DataTableCellEditResult | Promise<DataTableCellEditResult>
  empty?: string
  pageSize?: number
  defaultSort?: DataTableSort
  /** The server has more rows than `data` holds; `onLoadMore` fetches the next batch. */
  hasMore?: boolean
  onLoadMore?: () => void
  loadingMore?: boolean
  density?: "compact" | "default" | "comfortable"
}) {
  const card = useCardMode()
  const [limit, setLimit] = useState(pageSize)
  const table = useDataTable({ data, columns, getRowId: rowId, pageSize: limit, perPage: { value: limit, onChange: setLimit }, defaultSort })
  const moreHere = table.rowCount > limit
  const more = useCallback(() => {
    if (moreHere) setLimit((l) => l + pageSize)
    else onLoadMore?.()
  }, [moreHere, onLoadMore, pageSize])
  const shownFacets = facets.filter((f) => f.options.length > 1)
  const hasToolbar = !!search || shownFacets.length > 0 || !!toolbar
  return (
    <div className="flex flex-col gap-3">
      {hasToolbar ? (
        <DataTableToolbar>
          {search ? <DataTableSearch table={table} placeholder={search} /> : null}
          {shownFacets.map((f) => (
            <DataTableFacetFilter key={f.column} table={table} column={f.column} title={f.title} options={f.options} />
          ))}
          {toolbar}
        </DataTableToolbar>
      ) : null}
      <DataTable
        table={table}
        label={label}
        rowLabel={rowLabel}
        rowActions={rowActions}
        onRowClick={onRowClick}
        onCellEdit={onCellEdit}
        empty={empty}
        frame={card}
        density={density}
      />
      <LoadMore hasMore={moreHere || !!hasMore} onMore={more} loading={loadingMore} />
    </div>
  )
}
