import type { I18nText } from '@/lib/types'
import { currentLocale } from './index'

export interface LocalizedText {
  title?: string
  name?: string
  label?: string
  description?: string
  emptyText?: string
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

function localeLike(value: string): boolean {
  return /^(zh|en)([_-][a-z0-9]+)?$/i.test(value)
}

function i18nKey(value: string): string {
  const trimmed = value.trim()
  const dot = trimmed.indexOf('.')
  if (dot < 0) return canonicalLocale(trimmed)
  const left = trimmed.slice(0, dot)
  const right = trimmed.slice(dot + 1)
  if (localeLike(left)) return `${canonicalLocale(left)}.${right.toLowerCase()}`
  if (localeLike(right)) return `${left.toLowerCase()}.${canonicalLocale(right)}`
  return trimmed.toLowerCase()
}
function i18nIndex(value: I18nText | undefined): Map<string, string> {
  const index = new Map<string, string>()
  for (const [key, text] of Object.entries(value ?? {})) {
    if (text.trim()) index.set(i18nKey(key), text.trim())
  }
  return index
}

function lookupField(
  index: Map<string, string>, value: LocalizedText,
  field: 'title' | 'name' | 'label' | 'description' | 'emptyText', locale: string,
): string | undefined {
  const hasPrimaryText = Boolean(value.title?.trim() || value.name?.trim() || value.label?.trim())
  for (const candidate of localeCandidates(locale)) {
    // 单字段对象使用 locale 键；Action 同时声明 title/description 时，description
    // 使用 "<locale>.description"（并兼容 "description.<locale>"）避免覆盖 title。
    const keys = field === 'description' || field === 'emptyText'
      ? [`${candidate}.${field}`, `${field}.${candidate}`, ...(hasPrimaryText ? [] : [candidate])]
      : [`${candidate}.${field}`, candidate]
    for (const key of keys) {
      const translated = index.get(i18nKey(key))
      if (translated) return translated
    }
  }
  return undefined
}

export function resolveLocalizedText(
  value: LocalizedText | undefined,
  field: 'title' | 'name' | 'label' | 'description' | 'emptyText',
  locale: string = currentLocale(),
): string | undefined {
  if (!value) return undefined
  const translated = lookupField(i18nIndex(value.i18n), value, field, locale)
  if (translated) return translated
  return value[field]?.trim() || undefined
}
