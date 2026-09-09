import { useState } from 'react'
import { Languages } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { currentLocale, setLocale, SUPPORTED_LOCALES, type Locale } from '@/i18n'
import { cn } from '@/lib/cn'

export function LocaleSwitcher({ className }: { className?: string }) {
  const { t } = useTranslation('common')
  const [locale, setLocaleState] = useState<Locale>(() => currentLocale())

  return (
    <label className={cn('inline-flex min-w-0 items-center gap-1.5', className)}>
      <Languages size={13} className="shrink-0 text-ink-3" aria-hidden="true" />
      <span className="sr-only">{t('language.label')}</span>
      <select
        aria-label={t('language.label')}
        value={locale}
        onChange={(event) => {
          const next = event.target.value as Locale
          setLocaleState(next)
          void setLocale(next)
        }}
        className="min-h-touch min-w-0 rounded-pill border border-hairline bg-surface px-2 py-1 text-meta font-medium outline-none transition-colors focus:border-accent sm:min-h-0"
      >
        {SUPPORTED_LOCALES.map((value) => (
          <option key={value} value={value}>
            {value === 'zh-CN' ? t('language.zhCN') : t('language.enUS')}
          </option>
        ))}
      </select>
    </label>
  )
}
