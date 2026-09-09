import { useState } from 'react'
import { Languages } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { currentLocale, setLocale, SUPPORTED_LOCALES, type Locale } from '@/i18n'
import { cn } from '@/lib/cn'
import { Select } from '@/components/ui'

export function LocaleSwitcher({ className }: { className?: string }) {
  const { t } = useTranslation('common')
  const [locale, setLocaleState] = useState<Locale>(() => currentLocale())

  return (
    <div className={cn('inline-flex min-w-0 items-center gap-1.5', className)}>
      <Languages size={13} className="shrink-0 text-ink-3" aria-hidden="true" />
      <Select
        aria-label={t('language.label')}
        pill
        value={locale}
        onChange={(event) => {
          const next = event.target.value as Locale
          setLocaleState(next)
          void setLocale(next)
        }}
        className="min-w-0"
      >
        {SUPPORTED_LOCALES.map((value) => (
          <option key={value} value={value}>
            {value === 'zh-CN' ? t('language.zhCN') : t('language.enUS')}
          </option>
        ))}
      </Select>
    </div>
  )
}
