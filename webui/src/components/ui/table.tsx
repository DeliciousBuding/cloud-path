import type { ComponentProps } from 'react'
import { cn } from '@/lib/cn'

export function Table({ className, ...props }: ComponentProps<'table'>) {
  return <table className={cn('w-full border-collapse text-left', className)} {...props} />
}

export function TableHeader({ className, ...props }: ComponentProps<'thead'>) {
  return <thead className={cn('border-y border-hairline bg-surface-2 text-meta text-ink-3', className)} {...props} />
}

export function TableBody({ className, ...props }: ComponentProps<'tbody'>) {
  return <tbody className={cn('divide-y divide-hairline', className)} {...props} />
}

export function TableFooter({ className, ...props }: ComponentProps<'tfoot'>) {
  return <tfoot className={cn('border-t border-hairline bg-surface-2/50', className)} {...props} />
}

export function TableRow({ className, ...props }: ComponentProps<'tr'>) {
  return <tr className={cn('transition-colors hover:bg-surface-2/50', className)} {...props} />
}

export function TableHead({ className, ...props }: ComponentProps<'th'>) {
  return <th className={cn('px-3 py-2.5 font-medium', className)} {...props} />
}

export function TableCell({ className, ...props }: ComponentProps<'td'>) {
  return <td className={cn('px-3 py-3', className)} {...props} />
}

export function TableCaption({ className, ...props }: ComponentProps<'caption'>) {
  return <caption className={cn('mt-3 text-meta text-ink-3', className)} {...props} />
}
