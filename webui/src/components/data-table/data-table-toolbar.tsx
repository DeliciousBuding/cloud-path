import type { ReactTable, RowData } from '@tanstack/react-table'
import { Check, Columns3, PlusCircle, Search, X } from 'lucide-react'
import { useId, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/cn'
import { Input } from '@/components/ui'
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { dataTableFeatures } from './data-table'

export interface DataTableFilterOption {
  value: string
  label: string
  count?: number
}

export interface DataTableFilter {
  id: string
  label: string
  selected: string[]
  options: DataTableFilterOption[]
  onChange: (values: string[]) => void
  singleSelect?: boolean
}

export interface DataTableToolbarProps<TData extends RowData> {
  table: ReactTable<typeof dataTableFeatures, TData>
  search?: {
    value: string
    onChange: (value: string) => void
    label: string
    placeholder: string
  }
  filters?: DataTableFilter[]
  actions?: ReactNode
  resultCount?: number
  onReset?: () => void
}

export function DataTableToolbar<TData extends RowData>({
  table,
  search,
  filters = [],
  actions,
  resultCount,
  onReset,
}: DataTableToolbarProps<TData>) {
  const { t } = useTranslation()
  const searchId = useId()
  const active = Boolean(search?.value.trim()) || filters.some((filter) => filter.selected.length > 0)
  return (
    <div className="mb-3 flex flex-wrap items-center gap-2">
      {search && (
        <div className="relative min-w-[12rem] flex-1 sm:max-w-sm">
          <label className="sr-only" htmlFor={searchId}>{search.label}</label>
          <Search className="pointer-events-none absolute left-3.5 top-1/2 size-3.5 -translate-y-1/2 text-ink-3" />
          <Input
            id={searchId}
            type="search"
            value={search.value}
            placeholder={search.placeholder}
            onChange={(event) => search.onChange(event.target.value)}
            className="input-search"
          />
        </div>
      )}

      {filters.map((filter) => <DataTableFacetedFilter key={filter.id} filter={filter} />)}

      <div className="ml-auto flex flex-wrap items-center justify-end gap-2">
        {typeof resultCount === 'number' && (
          <span className="num text-meta text-ink-3">{t('table.rows', { count: resultCount })}</span>
        )}
        {active && onReset && (
          <button type="button" className="btn btn-ghost btn-sm" onClick={onReset}>
            <X className="size-3.5" />{t('actions.clear')}
          </button>
        )}
        <DataTableViewOptions table={table} />
        {actions}
      </div>
    </div>
  )
}

function DataTableFacetedFilter({ filter }: { filter: DataTableFilter }) {
  const { t } = useTranslation()
  const selected = new Set(filter.selected)
  const toggle = (value: string) => {
    if (filter.singleSelect) {
      filter.onChange(selected.has(value) ? [] : [value])
      return
    }
    const next = new Set(selected)
    if (next.has(value)) next.delete(value)
    else next.add(value)
    filter.onChange([...next])
  }

  return (
    <Popover>
      <PopoverTrigger asChild>
        <button type="button" className="btn btn-ghost btn-sm border-dashed">
          <PlusCircle className="size-3.5" />
          {filter.label}
          {selected.size > 0 && (
            <span className="num rounded-pill bg-ink-3/10 px-1.5 text-micro text-ink-2">{selected.size}</span>
          )}
        </button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-56 p-1.5">
        <div className="space-y-0.5" role="listbox" aria-label={filter.label} aria-multiselectable={!filter.singleSelect}>
          {filter.options.map((option) => {
            const checked = selected.has(option.value)
            return (
              <button
                key={option.value}
                type="button"
                role="option"
                aria-selected={checked}
                onClick={() => toggle(option.value)}
                className={cn(
                  'flex w-full items-center gap-2 rounded-tile px-2 py-1.5 text-left text-body transition-colors hover:bg-surface-2',
                  checked && 'bg-surface-2',
                )}
              >
                <span className="flex size-4 shrink-0 items-center justify-center rounded-tile border border-hairline">
                  {checked && <Check className="size-3" />}
                </span>
                <span className="min-w-0 flex-1 truncate">{option.label}</span>
                {typeof option.count === 'number' && <span className="num text-meta text-ink-3">{option.count}</span>}
              </button>
            )
          })}
        </div>
        {selected.size > 0 && (
          <button type="button" className="mt-1 w-full rounded-tile px-2 py-1.5 text-left text-meta text-ink-3 hover:bg-surface-2" onClick={() => filter.onChange([])}>
            {t('actions.clear')}
          </button>
        )}
      </PopoverContent>
    </Popover>
  )
}

function DataTableViewOptions<TData extends RowData>({
  table,
}: {
  table: ReactTable<typeof dataTableFeatures, TData>
}) {
  const { t } = useTranslation()
  const columns = table.getAllLeafColumns().filter((column) => column.getCanHide())
  if (columns.length === 0) return null

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button type="button" className="btn btn-ghost btn-sm">
          <Columns3 className="size-3.5" />{t('actions.view')}
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuLabel>{t('actions.columns')}</DropdownMenuLabel>
        <DropdownMenuSeparator />
        {columns.map((column) => {
          const meta = column.columnDef.meta as { label?: string } | undefined
          const label = meta?.label ?? (typeof column.columnDef.header === 'string' ? column.columnDef.header : column.id)
          return (
            <DropdownMenuCheckboxItem
              key={column.id}
              checked={column.getIsVisible()}
              onCheckedChange={(value) => column.toggleVisibility(value)}
            >
              {label}
            </DropdownMenuCheckboxItem>
          )
        })}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
