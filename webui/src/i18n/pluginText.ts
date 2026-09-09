import { currentLocale, normalizeLocale } from './index'

export interface LocalizedText {
  title?: string
  name?: string
  description?: string
  i18n?: Record<string, string>
}

function baseLocale(locale: string): string {
  return locale.split('-')[0] ?? locale
}

export function resolveLocalizedText(value: LocalizedText | undefined, field: 'title' | 'name' | 'description'): string | undefined {
  if (!value) return undefined
  const locale = currentLocale()
  const normalized = normalizeLocale(locale)
  const candidates = [normalized, baseLocale(normalized), 'zh-CN', 'en-US']
  for (const key of candidates) {
    const translated = value.i18n?.[key]
    if (translated?.trim()) return translated
  }
  const fallback = value[field]
  return fallback?.trim() || undefined
}
