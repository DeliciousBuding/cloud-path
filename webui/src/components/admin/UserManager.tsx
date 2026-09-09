// 用户管理面板（docs/api.md §3.2）：列表 / 创建 / 改 name·role·disabled / 重置密码。
// 只有 admin 会渲染到这里（pages/Admin.tsx 判据 + hooks 的 enabled 双重收口）。
import { useState } from 'react'
import { UserPlus, Users } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button, Panel } from '@/components/ui'
import { RowSkeleton } from '@/components/Skeleton'
import { CreateUserForm } from './CreateUserForm'
import { ErrorNote } from './ErrorNote'
import { UserRow } from './UserRow'
import { useAdminUsers } from '@/hooks/useAdmin'
import { adminErrorMessage } from '@/lib/admin'

export function UserManager() {
  const { t } = useTranslation('admin')
  const { data, isPending, isError, error, refetch } = useAdminUsers()
  const users = data?.users ?? []
  const [creating, setCreating] = useState(false)

  return (
    <Panel
      title={<span className="flex items-center gap-1.5"><Users size={14} />{t('userManager.title')}</span>}
      right={(
        <Button
          variant={creating ? 'ghost' : 'primary'}
          aria-expanded={creating}
          onClick={() => setCreating((v) => !v)}
        >
          {!creating && <UserPlus size={14} />}{creating ? t('userManager.collapse') : t('userManager.add')}
        </Button>
      )}
    >
      {creating && <CreateUserForm onDone={() => setCreating(false)} />}

      {isError ? (
        <ErrorNote message={adminErrorMessage(error)} onRetry={() => void refetch()} />
      ) : isPending ? (
        <RowSkeleton rows={3} />
      ) : users.length === 0 ? (
        <p className="py-6 text-center text-body text-ink-3">{t('userManager.empty')}</p>
      ) : (
        <ul className="divide-y divide-hairline" aria-label={t('userManager.listAria')}>
          {users.map((u) => <UserRow key={u.id} user={u} />)}
        </ul>
      )}

      <p className="mt-4 border-t border-hairline pt-3 text-meta leading-relaxed text-ink-3 break-words">
        {t('userManager.hint')}
      </p>
    </Panel>
  )
}
