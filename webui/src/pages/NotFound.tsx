import { Link } from 'react-router'
import { useTranslation } from 'react-i18next'
import '@/i18n'
import { usePageTitle } from '@/hooks/usePageTitle'
import { Compass, ArrowLeft } from 'lucide-react'
import { EmptyState } from '@/components/ui'

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
        <Link to="/" className="btn btn-ghost">
          <ArrowLeft size={14} /> {t('notFound.back')}
        </Link>
      </div>
    </div>
  )
}
