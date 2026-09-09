import {
  columnVisibilityFeature,
  createSortedRowModel,
  metaHelper,
  rowSortingFeature,
  sortFn_alphanumeric,
  tableFeatures,
  useTable,
  type ColumnDef,
  type ReactTable,
  type RowData,
  type SortingState,
} from '@tanstack/react-table'
import type { ReactNode } from 'react'
import { cn } from '@/lib/cn'
import { Skeleton } from '@/components/Skeleton'
import { Table, TableBody, TableCell, TableFooter, TableHead, TableHeader, TableRow } from '@/components/ui/table'

export interface DataTableColumnMeta {
  label?: string
  headerClassName?: string
  cellClassName?: string
}

export const dataTableFeatures = tableFeatures({
  rowSortingFeature,
  sortedRowModel: createSortedRowModel(),
  sortFns: { alphanumeric: sortFn_alphanumeric },
  columnVisibilityFeature,
  columnMeta: metaHelper<DataTableColumnMeta>(),
})

export type DataTableColumn<TData extends RowData> = ColumnDef<typeof dataTableFeatures, TData, any>

export interface UseDataTableOptions<TData extends RowData> {
  columns: DataTableColumn<TData>[]
  data: TData[]
  getRowId?: (row: TData, index: number) => string
  initialSorting?: SortingState
}

export function useDataTable<TData extends RowData>({
  columns,
  data,
  getRowId,
  initialSorting,
}: UseDataTableOptions<TData>): ReactTable<typeof dataTableFeatures, TData> {
  return useTable({
    features: dataTableFeatures,
    columns,
    data,
    getRowId,
    initialState: initialSorting ? { sorting: initialSorting } : undefined,
  })
}

export interface StaticDataTableProps<TData extends RowData>
  extends Omit<DataTableProps<TData>, 'table'> {
  columns: DataTableColumn<TData>[]
  data: TData[]
  getRowId?: (row: TData, index: number) => string
  initialSorting?: SortingState
}

export function StaticDataTable<TData extends RowData>({
  columns,
  data,
  getRowId,
  initialSorting,
  ...props
}: StaticDataTableProps<TData>) {
  const table = useDataTable({ columns, data, getRowId, initialSorting })
  return <DataTable table={table} {...props} />
}

export interface DataTableProps<TData extends RowData> {
  table: ReactTable<typeof dataTableFeatures, TData>
  ariaLabel: string
  empty: ReactNode
  rowClassName?: (row: TData, index: number) => string | undefined
  loading?: boolean
  loadingRows?: number
  minWidthClassName?: string
  containerClassName?: string
  tableClassName?: string
  footer?: ReactNode
}

export function DataTable<TData extends RowData>({
  table,
  ariaLabel,
  empty,
  rowClassName,
  loading = false,
  loadingRows = 3,
  minWidthClassName,
  containerClassName,
  tableClassName,
  footer,
}: DataTableProps<TData>) {
  const rows = table.getRowModel().rows
  const colSpan = table.getVisibleLeafColumns().length

  return (
    <div
      className={cn('overflow-x-auto', containerClassName)}
      role="region"
      aria-label={ariaLabel}
      tabIndex={0}
    >
      <Table className={cn(minWidthClassName, tableClassName)} aria-label={ariaLabel}>
        <TableHeader>
          {table.getHeaderGroups().map((headerGroup) => (
            <TableRow key={headerGroup.id} className="hover:bg-transparent">
              {headerGroup.headers.map((header) => (
                <TableHead key={header.id} colSpan={header.colSpan} className={(header.column.columnDef.meta as DataTableColumnMeta | undefined)?.headerClassName}>
                  {header.isPlaceholder ? null : <table.FlexRender header={header} />}
                </TableHead>
              ))}
            </TableRow>
          ))}
        </TableHeader>
        <TableBody>
          {loading ? (
            <TableSkeletonRows colSpan={colSpan} rows={loadingRows} />
          ) : rows.length === 0 ? (
            <TableRow className="hover:bg-transparent">
              <TableCell colSpan={colSpan} className="py-8 text-center text-body text-ink-3">
                {empty}
              </TableCell>
            </TableRow>
          ) : (
            rows.map((row, index) => (
              <TableRow key={row.id} className={rowClassName?.(row.original, index)}>
                {row.getVisibleCells().map((cell) => (
                  <TableCell key={cell.id} className={(cell.column.columnDef.meta as DataTableColumnMeta | undefined)?.cellClassName}>
                    <table.FlexRender cell={cell} />
                  </TableCell>
                ))}
              </TableRow>
            ))
          )}
        </TableBody>
        {footer && <TableFooter>{footer}</TableFooter>}
      </Table>
    </div>
  )
}

function TableSkeletonRows({ colSpan, rows }: { colSpan: number; rows: number }) {
  return (
    <>
      {Array.from({ length: rows }, (_, rowIndex) => (
        <TableRow key={`skeleton-${rowIndex}`} className="hover:bg-transparent">
          {Array.from({ length: colSpan }, (_, cellIndex) => (
            <TableCell key={`skeleton-${rowIndex}-${cellIndex}`}>
              <Skeleton className="h-4" />
            </TableCell>
          ))}
        </TableRow>
      ))}
    </>
  )
}
