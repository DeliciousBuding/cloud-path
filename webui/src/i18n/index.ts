import i18n from 'i18next'
import LanguageDetector from 'i18next-browser-languagedetector'
import { initReactI18next } from 'react-i18next'
import {
  DEFAULT_LOCALE, LOCALE_STORAGE_KEY, normalizeLocale, readStoredLocale, storeLocale, type Locale,
} from './config'
import { resources } from './resources'

const initialLocale = readStoredLocale() ?? (import.meta.env.MODE === 'test' ? DEFAULT_LOCALE : undefined)

if (!i18n.isInitialized) {
  void i18n
    .use(LanguageDetector)
    .use(initReactI18next)
    .init({
      resources,
      lng: initialLocale,
      supportedLngs: ['zh-CN', 'en-US'],
      fallbackLng: DEFAULT_LOCALE,
      defaultNS: 'common',
      ns: ['common', 'nav', 'auth', 'overview', 'devices', 'edges', 'activity', 'plugins', 'settings', 'admin', 'errors', 'plugin'],
      detection: {
        order: ['localStorage', 'navigator', 'htmlTag'],
        caches: ['localStorage'],
        lookupLocalStorage: LOCALE_STORAGE_KEY,
      },
      interpolation: { escapeValue: false },
      initAsync: false,
      react: { useSuspense: false },
    })
}

export function currentLocale(): Locale {
  return normalizeLocale(i18n.resolvedLanguage ?? readStoredLocale() ?? i18n.language)
}

if (typeof document !== 'undefined') document.documentElement.lang = currentLocale()

export async function setLocale(locale: Locale): Promise<void> {
  const next = normalizeLocale(locale)
  storeLocale(next)
  await i18n.changeLanguage(next)
  if (typeof document !== 'undefined') document.documentElement.lang = next
}

export { i18n }
export type { Locale }
export * from './config'
