import type { AuthState } from '@/store/auth'

/** 显式 open 保留 legacy 命令入口；来源/回环限制仍由服务端 authWrite 裁决。 */
export function commandScope({ status, user }: AuthState, deviceId: string): string | null {
  if (status === 'open') return JSON.stringify(['open', deviceId])
  // id=0 是合法的兼容/服务身份，不得用 truthiness 拦截。
  if (status !== 'in' || !user || !Number.isInteger(user.id) || user.id < 0
    || !Number.isInteger(user.tenant_id) || user.tenant_id < 0 || user.disabled
    || (user.role !== 'operator' && user.role !== 'admin')) return null
  return JSON.stringify(['in', user.id, user.username, user.tenant_id, user.tenant_slug, user.role, deviceId])
}
