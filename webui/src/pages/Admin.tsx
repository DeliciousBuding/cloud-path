// 管理页（docs/api.md §3.1-3.3）：用户 + 服务令牌。
// 可见性判据只有一个：auth.me.role === admin。非 admin（含 me 不可用的开放访问）
// 直接不渲染任何敏感列表与操作按钮 —— 不是 disabled，是根本不出现在 DOM 里；
// 服务端另有 requireAdmin 门禁（401/403）兜底。
import { ShieldAlert } from 'lucide-react'
import { EmptyState, PageHeader } from '@/components/ui'
import { TokenManager } from '@/components/admin/TokenManager'
import { UserManager } from '@/components/admin/UserManager'
import { useIsAdmin } from '@/store/auth'
import { usePageTitle } from '@/hooks/usePageTitle'

export default function Admin() {
  usePageTitle('成员与访问权限')

  const admin = useIsAdmin()

  if (!admin) {
    return (
      <>
        <PageHeader title="成员与访问权限" subtitle="成员、权限和访问令牌" />
        <EmptyState
          icon={<ShieldAlert size={24} />}
          title="需要管理员权限"
          hint="只有管理员可以管理成员和访问令牌。如需权限，请联系管理员调整你的角色。"
        />
      </>
    )
  }

  return (
    <>
      <PageHeader title="成员与访问权限" subtitle="管理成员、角色和访问令牌" />
      <div className="grid items-start gap-5 xl:grid-cols-2">
        <UserManager />
        <TokenManager />
      </div>
    </>
  )
}