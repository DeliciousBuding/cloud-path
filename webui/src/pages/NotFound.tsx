import { useTranslation } from 'react-i18next'
import '@/i18n'
import { usePageTitle } from '@/hooks/usePageTitle'
import { Compass, ArrowLeft } from 'lucide-react'
import { ButtonLink, EmptyState } from '@/components/ui'

export default function NotFound() {
  const { t } = useTranslation('auth')
  usePageTitle(t('pageTitle.notFound'))

  return (
    <div className="py-10">
      <EmptyState
        icon={<Compass size={24} />}
        title={t('notFound.title')}
        hint={t('notFound.hint')}
      />
      <div className="mt-5 flex justify-center">
        <ButtonLink to="/" variant="ghost">
          <ArrowLeft size={14} /> {t('notFound.back')}
        </ButtonLink>
      </div>
    </div>
  )
}
