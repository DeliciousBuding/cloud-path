import { currentLocale } from './index'

export function locale(): string {
  return currentLocale()
}

export function formatNumber(value: number, options?: Intl.NumberFormatOptions): string {
  return new Intl.NumberFormat(locale(), options).format(value)
}

export function formatDateTime(ts: number, options?: Intl.DateTimeFormatOptions): string {
  if (!ts) return '—'
  return new Intl.DateTimeFormat(locale(), options ?? {
    year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false,
  }).format(new Date(ts * 1000))
}

export function formatTime(ts: number, options?: Intl.DateTimeFormatOptions): string {
  return new Intl.DateTimeFormat(locale(), options ?? { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false })
    .format(new Date(ts * 1000))
}
