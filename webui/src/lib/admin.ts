// 管理面（docs/api.md §3.1-3.3）的展示常量与错误映射：纯函数、无副作用、无设备语义。
// 角色与 scope 的合法性事实源始终在后端（server 校验后回 4xx）；本文件只负责两件事：
//   ① 把 HTTP 状态码说成人话（不复述、不猜测业务规则，例如「最后一个 admin」由 server 判定）
//   ② 给表单一个最小权限默认值（admin 角色 / admin·edge scope 一律不预选）
import { i18n } from '@/i18n'
import { ApiError } from './api'
import type { Role, TokenScope } from './types'

function tr(key: string, ns: 'admin' | 'common' = 'admin', options?: Record<string, unknown>): string {
  return i18n.t(key, { ns, ...options })
}

export interface RoleOption { value: Role; label: string; hint: string }

/** 角色选项，顺序即权限由低到高（docs/api.md §3.1） */
export const ROLE_OPTIONS: RoleOption[] = [
  {
    value: 'viewer',
    get label() { return tr('roles.viewer', 'common') },
    get hint() { return tr('roleHints.viewer') },
  },
  {
    value: 'operator',
    get label() { return tr('roles.operator', 'common') },
    get hint() { return tr('roleHints.operator') },
  },
  {
    value: 'admin',
    get label() { return tr('roles.admin', 'common') },
    get hint() { return tr('roleHints.admin') },
  },
]

/** 新建用户的默认角色：最小权限，不预选 admin */
export const DEFAULT_ROLE: Role = 'viewer'

export function roleOption(role: string): RoleOption | undefined {
  return ROLE_OPTIONS.find((r) => r.value === role)
}

export interface ScopeOption { value: TokenScope; label: string; hint: string; danger: boolean }

/** 令牌 scope 选项；danger=true 的范围在表单里必须给出显式风险说明 */
export const SCOPE_OPTIONS: ScopeOption[] = [
  {
    value: 'read', danger: false,
    get label() { return tr('createToken.scopes.options.read.label') },
    get hint() { return tr('createToken.scopes.options.read.hint') },
  },
  {
    value: 'write', danger: false,
    get label() { return tr('createToken.scopes.options.write.label') },
    get hint() { return tr('createToken.scopes.options.write.hint') },
  },
  {
    value: 'admin', danger: true,
    get label() { return tr('createToken.scopes.options.admin.label') },
    get hint() { return tr('createToken.scopes.options.admin.hint') },
  },
  {
    value: 'edge', danger: true,
    get label() { return tr('createToken.scopes.options.edge.label') },
    get hint() { return tr('createToken.scopes.options.edge.hint') },
  },
]

/** 默认最小权限：只勾 read（服务端要求 scopes 非空），write/admin/edge 全部不预选 */
export const DEFAULT_SCOPES: TokenScope[] = ['read']

export interface ExpiryOption { value: string; label: string }

/** 令牌有效期选项 */
export const EXPIRY_OPTIONS: ExpiryOption[] = [
  { value: '1', get label() { return tr('createToken.expiry.options.1') } },
  { value: '7', get label() { return tr('createToken.expiry.options.7') } },
  { value: '30', get label() { return tr('createToken.expiry.options.30') } },
  { value: '90', get label() { return tr('createToken.expiry.options.90') } },
  { value: 'never', get label() { return tr('createToken.expiry.options.never') } },
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
    if (e.status === 401) return tr('errors.unauthorized')
    if (e.status === 403) return tr('errors.forbidden')
    if (e.status === 404) return tr('errors.notFound')
    if (e.status === 429) {
      return e.retryAfter
        ? tr('errors.rateLimitedAfter', 'admin', { seconds: e.retryAfter })
        : tr('errors.rateLimited')
    }
    if (e.status >= 500) return tr('errors.unavailable')
    if (e.status === 400) {
      return /[\u3400-\u9fff]/.test(e.message) ? e.message : tr('errors.badRequest')
    }
    // 409 等业务规则由服务端判定；说明通常已是可直接展示的人话。
    if (e.message) return e.message
    return tr('errors.generic')
  }
  if (e instanceof Error && /\u65e0\u6cd5\u8fde\u63a5|network|fetch/i.test(e.message)) {
    return tr('errors.network')
  }
  return e instanceof Error && e.message ? e.message : tr('errors.generic')
}
