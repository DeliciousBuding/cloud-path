// 用户管理面板（docs/api.md §3.2）：列表 / 创建 / 改 name·role·disabled / 重置密码。
// 只有 admin 会渲染到这里（pages/Admin.tsx 判据 + hooks 的 enabled 双重收口）。
import { useState } from 'react'
import { UserPlus, Users } from 'lucide-react'
import { Button, Panel } from '@/components/ui'
import { RowSkeleton } from '@/components/Skeleton'
import { CreateUserForm } from './CreateUserForm'
import { ErrorNote } from './ErrorNote'
import { UserRow } from './UserRow'
import { useAdminUsers } from '@/hooks/useAdmin'
import { adminErrorMessage } from '@/lib/admin'

export function UserManager() {
  const { data, isPending, isError, error, refetch } = useAdminUsers()
  const users = data?.users ?? []
  const [creating, setCreating] = useState(false)

  return (
    <Panel
      title={<span className="flex items-center gap-1.5"><Users size={14} />成员</span>}
      right={(
        <Button
          variant={creating ? 'ghost' : 'primary'}
          aria-expanded={creating}
          onClick={() => setCreating((v) => !v)}
        >
          {!creating && <UserPlus size={14} />}{creating ? '收起表单' : '添加成员'}
        </Button>
      )}
    >
      {creating && <CreateUserForm onDone={() => setCreating(false)} />}

      {isError ? (
        <ErrorNote message={adminErrorMessage(error)} onRetry={() => void refetch()} />
      ) : isPending ? (
        <RowSkeleton rows={3} />
      ) : users.length === 0 ? (
        <p className="py-6 text-center text-body text-ink-3">还没有成员。添加成员后，他们就可以按角色访问平台。</p>
      ) : (
        <ul className="divide-y divide-hairline" aria-label="成员列表">
          {users.map((u) => <UserRow key={u.id} user={u} />)}
        </ul>
      )}

      <p className="mt-4 border-t border-hairline pt-3 text-meta leading-relaxed text-ink-3 break-words">
        这里显示当前组织的成员。角色决定成员可以查看还是执行操作。
      </p>
    </Panel>
  )
}