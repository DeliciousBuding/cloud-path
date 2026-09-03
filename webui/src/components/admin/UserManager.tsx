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
      title={<span className="flex items-center gap-1.5"><Users size={14} />用户</span>}
      right={(
        <Button
          variant={creating ? 'ghost' : 'primary'}
          aria-expanded={creating}
          onClick={() => setCreating((v) => !v)}
        >
          {!creating && <UserPlus size={14} />}{creating ? '收起表单' : '新建用户'}
        </Button>
      )}
    >
      {creating && <CreateUserForm onDone={() => setCreating(false)} />}

      {isError ? (
        <ErrorNote message={adminErrorMessage(error)} onRetry={() => void refetch()} />
      ) : isPending ? (
        <RowSkeleton rows={3} />
      ) : users.length === 0 ? (
        <p className="py-6 text-center text-sm text-ink-3">本租户还没有用户</p>
      ) : (
        <ul className="space-y-3" aria-label="用户列表">
          {users.map((u) => <UserRow key={u.id} user={u} />)}
        </ul>
      )}

      <p className="mt-4 border-t border-hairline pt-3 text-[11px] leading-relaxed text-ink-3 break-words">
        列表永不包含密码哈希；角色为 admin &gt; operator &gt; viewer 层级，跨租户用户统一不可见（服务端返回 404）。
      </p>
    </Panel>
  )
}