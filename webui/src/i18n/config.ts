export const SUPPORTED_LOCALES = ['zh-CN', 'en-US'] as const
export type Locale = typeof SUPPORTED_LOCALES[number]

export const DEFAULT_LOCALE: Locale = 'zh-CN'
export const LOCALE_STORAGE_KEY = 'cloudpath.locale'

const LOCALE_ALIASES: Record<string, Locale> = {
  zh: 'zh-CN', 'zh-cn': 'zh-CN', 'zh-hans': 'zh-CN',
  en: 'en-US', 'en-us': 'en-US', 'en-gb': 'en-US',
}

export function normalizeLocale(value: string | undefined | null): Locale {
  const raw = (value ?? '').trim()
  if (!raw) return DEFAULT_LOCALE
  const exact = SUPPORTED_LOCALES.find((locale) => locale.toLowerCase() === raw.toLowerCase())
  if (exact) return exact
  return LOCALE_ALIASES[raw.toLowerCase()] ?? DEFAULT_LOCALE
}

export function isSupportedLocale(value: string | undefined | null): value is Locale {
  if (!value) return false
  return SUPPORTED_LOCALES.some((locale) => locale.toLowerCase() === value.toLowerCase())
}

export function readStoredLocale(): Locale | undefined {
  try {
    const value = localStorage.getItem(LOCALE_STORAGE_KEY)
    return isSupportedLocale(value) ? normalizeLocale(value) : undefined
  } catch {
    return undefined
  }
}

export function storeLocale(locale: Locale): void {
  try { localStorage.setItem(LOCALE_STORAGE_KEY, locale) } catch { /* 隐私模式不影响本次切换 */ }
}
