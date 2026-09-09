import type { I18nText } from '@/lib/types'
import { currentLocale } from './index'

export interface LocalizedText {
  title?: string
  name?: string
  description?: string
  i18n?: I18nText
}

function canonicalLocale(locale: string): string {
  return locale.trim().replace(/_/g, '-').toLowerCase()
}

function localeCandidates(locale: string): string[] {
  const normalized = canonicalLocale(locale) || 'zh-cn'
  const base = normalized.split('-')[0] || normalized
  return [...new Set([normalized, base, 'zh-cn', 'en-us'])]
}

function i18nIndex(value: I18nText | undefined): Map<string, string> {
  const index = new Map<string, string>()
  for (const [key, text] of Object.entries(value ?? {})) {
    if (text.trim()) index.set(canonicalLocale(key), text.trim())
  }
  return index
}

function lookupField(
  index: Map<string, string>, value: LocalizedText, field: 'title' | 'name' | 'description', locale: string,
): string | undefined {
  const hasPrimaryText = Boolean(value.title?.trim() || value.name?.trim())
  for (const candidate of localeCandidates(locale)) {
    // 单字段对象使用 locale 键；Action 同时声明 title/description 时，description
    // 使用 "<locale>.description"（并兼容 "description.<locale>"）避免覆盖 title。
    const keys = field === 'description'
      ? [`${candidate}.description`, `description.${candidate}`, ...(hasPrimaryText ? [] : [candidate])]
      : [`${candidate}.${field}`, candidate]
    for (const key of keys) {
      const translated = index.get(key)
      if (translated) return translated
    }
  }
  return undefined
}

export function resolveLocalizedText(
  value: LocalizedText | undefined,
  field: 'title' | 'name' | 'description',
  locale: string = currentLocale(),
): string | undefined {
  if (!value) return undefined
  const translated = lookupField(i18nIndex(value.i18n), value, field, locale)
  if (translated) return translated
  return value[field]?.trim() || undefined
}
