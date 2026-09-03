// 鉴权态（登录态事实源）：docs/api.md §2.2 GET /api/auth/me。
// - 200 → 'in'（已登录，user 为契约 user 对象）
// - 401 → 'out'（未登录；数据层任何受保护端点 401 也会置此态，路由守卫跳 /login）
// - me 不可用（404/网络错等：BE-AUTH 未就绪或 server 不可达）→ 'open'：
//   契约未定义该情形，按开放访问放行——保持单机模式（L0）现有行为，避免集成期把全站锁死。
import { create } from 'zustand'
import { ApiError, api, setToken } from '@/lib/api'
import type { UserView } from '@/lib/types'

export type AuthStatus = 'loading' | 'in' | 'out' | 'open'

export interface AuthState {
  status: AuthStatus
  user: UserView | null
}

export const useAuth = create<AuthState>(() => ({ status: 'loading', user: null }))

/** 数据请求/实时通道是否放行（已登录或开放访问） */
export function authReady(status: AuthStatus): boolean {
  return status === 'in' || status === 'open'
}

/**
 * 管理面可见性判据（docs/api.md §3.1）：只有「已登录且 me.role === admin」。
 * 'open'（me 不可用的开放访问）与 user=null 一律不算 admin —— 用户/令牌列表宁可不渲染，
 * 也不靠 disabled 遮一层（服务端另有 requireAdmin 门禁兜底）。
 */
export function isAdmin(status: AuthStatus, user: UserView | null): boolean {
  return status === 'in' && user?.role === 'admin'
}

/** 组件用的管理面可见性 hook */
export function useIsAdmin(): boolean {
  return useAuth((s) => isAdmin(s.status, s.user))
}

/**
 * 登录成功后的**唯一判据**：用 GET /api/auth/me 复核会话，并把用户写进 store。
 *
 * 为什么必须复核：/healthz 等公开端点可达**不代表**登录成功（那正是「假登录」的成因）。
 * 账号模式下会话靠 Set-Cookie 落地，me 返回 200 才证明后续 /api/* 与 /ws 真的能鉴权通过。
 * 复核失败会把状态收敛回未登录，调用方据此呈现错误而不是跳转首页。
 */
export async function confirmSession(fallback?: UserView | null): Promise<UserView | null> {
  const me = await api.me()
  const user = me?.user ?? fallback ?? null
  useAuth.setState({ status: 'in', user })
  return user
}

let refreshing = false

/**
 * 刷新登录态：App 挂载与路由切换后调用（契约探针 me）。
 * 已有判定结果时静默重验（不回 loading，避免路由/骨架闪烁）；仅首帧探测显示一次骨架。
 */
export async function refreshAuth(): Promise<void> {
  if (refreshing) return
  refreshing = true
  try {
    const me = await api.me()
    useAuth.setState({ status: 'in', user: me.user ?? null })
  } catch (e) {
    if (e instanceof ApiError && e.status === 401) {
      useAuth.setState({ status: 'out', user: null })
    } else if (useAuth.getState().status === 'loading') {
      useAuth.setState({ status: 'open', user: null })
    }
    // 其余情形（已判定后 me 暂不可达）：保持现状，数据层 401 仍会全局收敛
  } finally {
    refreshing = false
  }
}

/** 数据层收到受保护端点 401：置未登录（路由守卫负责跳 /login，实时通道随状态断开） */
export function markUnauthenticated(): void {
  const st = useAuth.getState()
  if (st.status === 'out') return
  useAuth.setState({ status: 'out', user: null })
}

/** 登出：清 server 会话（尽力而为）+ 本机令牌，置未登录；WS 断开由 App 监听状态触发 */
export async function logout(): Promise<void> {
  try {
    await api.logout()
  } catch { /* 会话已失效或 server 不可达时，本机清理即可 */ }
  setToken('')
  useAuth.setState({ status: 'out', user: null })
}