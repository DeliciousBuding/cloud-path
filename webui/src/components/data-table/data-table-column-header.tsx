import type { Column, RowData } from '@tanstack/react-table'
import { ArrowDown, ArrowUp, ChevronsUpDown, EyeOff } from 'lucide-react'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/cn'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { dataTableFeatures } from './data-table'

export function DataTableColumnHeader<TData extends RowData>({
  column,
  title,
  className,
}: {
  column: Column<typeof dataTableFeatures, TData, any>
  title: ReactNode
  className?: string
}) {
  const { t } = useTranslation()
  if (!column.getCanSort()) return <div className={cn(className)}>{title}</div>

  const sorted = column.getIsSorted()
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          className={cn('btn btn-ghost btn-sm -ml-2 max-w-full gap-1.5 whitespace-nowrap', className)}
        >
          <span className="truncate">{title}</span>
          {sorted === 'desc' ? <ArrowDown className="size-3.5" /> : sorted === 'asc' ? <ArrowUp className="size-3.5" /> : <ChevronsUpDown className="size-3.5" />}
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start">
        <DropdownMenuItem onClick={() => column.toggleSorting(false)}>
          <ArrowUp className="size-3.5 text-ink-3" />{t('actions.asc')}
        </DropdownMenuItem>
        <DropdownMenuItem onClick={() => column.toggleSorting(true)}>
          <ArrowDown className="size-3.5 text-ink-3" />{t('actions.desc')}
        </DropdownMenuItem>
        {column.getCanHide() && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuItem onClick={() => column.toggleVisibility(false)}>
              <EyeOff className="size-3.5 text-ink-3" />{t('actions.hide')}
            </DropdownMenuItem>
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
