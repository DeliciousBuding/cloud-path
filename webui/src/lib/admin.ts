// 管理面（docs/api.md §3.1-3.3）的展示常量与错误映射：纯函数、无副作用、无设备语义。
// 角色与 scope 的合法性事实源始终在后端（server 校验后回 4xx）；本文件只负责两件事：
//   ① 把 HTTP 状态码说成人话（不复述、不猜测业务规则，例如「最后一个 admin」由 server 判定）
//   ② 给表单一个最小权限默认值（admin 角色 / admin·edge scope 一律不预选）
import { ApiError } from './api'
import type { Role, TokenScope } from './types'

export interface RoleOption { value: Role; label: string; hint: string }

/** 角色选项，顺序即权限由低到高（docs/api.md §3.1） */
export const ROLE_OPTIONS: RoleOption[] = [
  { value: 'viewer', label: '只读', hint: '可以查看设备状态和运行记录' },
  { value: 'operator', label: '可以操作设备', hint: '可以查看并执行设备操作' },
  { value: 'admin', label: '管理员', hint: '可以管理成员和访问令牌' },
]

/** 新建用户的默认角色：最小权限，不预选 admin */
export const DEFAULT_ROLE: Role = 'viewer'

export function roleOption(role: string): RoleOption | undefined {
  return ROLE_OPTIONS.find((r) => r.value === role)
}

export interface ScopeOption { value: TokenScope; label: string; hint: string; danger: boolean }

/** 令牌 scope 选项；danger=true 的范围在表单里必须给出显式风险说明 */
export const SCOPE_OPTIONS: ScopeOption[] = [
  { value: 'read', label: '查看', hint: '查看设备状态、运行记录和操作记录', danger: false },
  { value: 'write', label: '操作设备', hint: '在查看之外执行设备操作', danger: false },
  { value: 'admin', label: '管理', hint: '可以管理成员和访问令牌，权限最高', danger: true },
  { value: 'edge', label: '网关接入', hint: '允许网关连接平台并同步设备状态', danger: true },
]

/** 默认最小权限：只勾 read（服务端要求 scopes 非空），write/admin/edge 全部不预选 */
export const DEFAULT_SCOPES: TokenScope[] = ['read']

export interface ExpiryOption { value: string; label: string }

/** 令牌有效期选项 */
export const EXPIRY_OPTIONS: ExpiryOption[] = [
  { value: '1', label: '1 天后自动失效' },
  { value: '7', label: '7 天后自动失效' },
  { value: '30', label: '30 天后自动失效' },
  { value: '90', label: '90 天后自动失效' },
  { value: 'never', label: '不自动失效' },
]

/** 默认 30 天而不是永不过期：把暴露窗口压到最小 */
export const DEFAULT_EXPIRY = '30'

const DAY_S = 86_400

/** 有效期选项 → `expires_at`（unix 秒）；'never' 或非法值返回 undefined（该字段不进 body） */
export function expiryToUnix(value: string, now: number = Date.now()): number | undefined {
  if (value === 'never') return undefined
  const days = Number(value)
  if (!Number.isFinite(days) || days <= 0) return undefined
  return Math.floor(now / 1000) + Math.round(days * DAY_S)
}

/**
 * 管理面错误 → 人话。
 * 401/403/429 是本地可解释的语义；其余（400/404/409/5xx）直接采用服务端给的说明文本，
 * 409「不能禁用或降级最后一个可用 admin」这类规则由 server 判定，前端不做本地伪判。
 */
export function adminErrorMessage(e: unknown): string {
  if (e instanceof ApiError) {
    if (e.status === 401) return '登录已失效，请重新登录后再操作'
    if (e.status === 403) return '当前账号没有管理权限。请联系管理员。'
    if (e.status === 404) return '记录不存在或已被删除，请刷新列表后重试。'
    if (e.status === 429) {
      return e.retryAfter ? `操作过于频繁，请 ${e.retryAfter} 秒后重试` : '操作过于频繁，请稍后重试'
    }
    if (e.status >= 500) return '服务暂时不可用，请稍后重试；如果持续失败，请联系管理员。'
    if (e.status === 400) {
      return /[\u3400-\u9fff]/.test(e.message) ? e.message : '提交内容不符合要求，请检查后重试。'
    }
    // 409 等业务规则由服务端判定；说明通常已是可直接展示的人话。
    if (e.message) return e.message
    return '操作暂时没有成功，请稍后重试。'
  }
  if (e instanceof Error && /无法连接|network|fetch/i.test(e.message)) {
    return '暂时无法连接 CloudPath，请检查网络后重试。'
  }
  return e instanceof Error && e.message ? e.message : '操作暂时没有成功，请稍后重试。'
}