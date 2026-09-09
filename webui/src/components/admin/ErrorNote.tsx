// 管理面的错误/提示条：role=alert 让读屏立即播报，文案由 lib/admin.ts 统一映射成人话。
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/cn'
import { Button } from '@/components/ui'

export function ErrorNote({ message, onRetry, className }: {
  message: string
  onRetry?: () => void
  className?: string
}) {
  const { t } = useTranslation('admin')
  return (
    <div role="alert" className={cn('rounded-tile border border-bad/30 bg-bad/10 px-4 py-3', className)}>
      {/* 服务端错误文本长度不可控：必须可断行，否则 390px 撑出横向滚动 */}
      <p className="text-compact leading-relaxed text-bad break-words">{message}</p>
      {onRetry && (
        <Button variant="ghost" className="mt-2.5" onClick={onRetry}>{t('retry')}</Button>
      )}
    </div>
  )
}
